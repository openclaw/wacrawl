package whatsappdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/steipete/wacrawl/internal/store"
	_ "modernc.org/sqlite"
)

func TestImportDesktopCoreDataShape(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)

	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	stats, err := Import(ctx, archive, source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chats != 2 || stats.Contacts != 2 || stats.Groups != 1 || stats.Participants != 1 || stats.Messages != 4 || stats.MediaMessages != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	status, err := archive.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 4 || status.MediaMessages != 1 || status.UnreadChats != 1 || status.UnreadMessages != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}

	results, err := archive.Search(ctx, store.MessageFilter{Query: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}
	if results[0].SenderJID != "222@lid" || results[0].SenderName != "Alice" {
		t.Fatalf("group sender not resolved from member row: %+v", results[0])
	}
	if results[0].ChatJID != "123@g.us" || results[0].MediaType != "image" {
		t.Fatalf("group/media fields wrong: %+v", results[0])
	}

	dms, err := archive.Messages(ctx, store.MessageFilter{ChatJID: "111@s.whatsapp.net", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dms) != 3 {
		t.Fatalf("expected 3 dm messages, got %d", len(dms))
	}
	if dms[0].SenderJID != "111@s.whatsapp.net" || dms[0].SenderName != "Bob" {
		t.Fatalf("incoming dm sender wrong: %+v", dms[0])
	}
	if !dms[1].FromMe || dms[1].SenderName != "me" {
		t.Fatalf("outgoing dm sender wrong: %+v", dms[1])
	}
}

func TestImportDesktopDuplicateSourceRows(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)

	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, chatDB, `
insert into ZWACHATSESSION values (3, '111@s.whatsapp.net', 'Bob New', 700000030, 5, 1, 0, 0, 0);
insert into ZWAMESSAGE values (5, 3, null, null, 'dm-new', 0, 700000030, 'newest message', 0, 0, '111@s.whatsapp.net', '', 'Bob New');
insert into ZWAGROUPINFO values (2, 2, 'owner-new@s.whatsapp.net', 699998000);
insert into ZWAGROUPMEMBER values (2, 2, '222@lid', 'Alice Duplicate', 'Alicia', 0, 0);
`)
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}

	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	stats, err := Import(ctx, archive, source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chats != 2 || stats.Groups != 1 || stats.Participants != 1 || stats.Messages != 5 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	chats, err := archive.ListChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}
	if chats[0].JID != "111@s.whatsapp.net" || chats[0].Name != "Bob New" || chats[0].UnreadCount != 5 || !chats[0].Archived {
		t.Fatalf("duplicate chat rows were not merged correctly: %+v", chats[0])
	}

	exported, err := archive.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Groups) != 1 || exported.Groups[0].OwnerJID != "owner@s.whatsapp.net" {
		t.Fatalf("duplicate group rows were not merged correctly: %+v", exported.Groups)
	}
	if len(exported.Participants) != 1 || !exported.Participants[0].IsAdmin || !exported.Participants[0].IsActive {
		t.Fatalf("duplicate participant rows were not merged correctly: %+v", exported.Participants)
	}
}

func TestDiscoverAndHelpers(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)

	discovered, err := Discover(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if !discovered.Available || discovered.MessageRows != 4 || discovered.ChatRows != 2 || discovered.ContactRows != 2 || discovered.MediaRows != 1 {
		t.Fatalf("unexpected discovery: %+v", discovered)
	}
	if discovered.OldestMessage == "" || discovered.NewestMessage == "" || len(discovered.SchemaNotes) == 0 {
		t.Fatalf("discovery missing metadata: %+v", discovered)
	}

	missing, err := Discover(ctx, filepath.Join(source, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Available {
		t.Fatalf("missing source should not be available: %+v", missing)
	}

	if runtime.GOOS == "darwin" && DefaultPath() == "" {
		t.Fatal("default path should be set on darwin")
	}
	if defaultedPath(source) != source {
		t.Fatal("explicit path should win")
	}
	if runtime.GOOS == "darwin" && defaultedPath("") == "" {
		t.Fatal("empty path should default")
	}

	if _, err := SnapshotPath(filepath.Join(source, "missing")); err == nil {
		t.Fatal("expected snapshot error for missing source")
	}
	filePath := filepath.Join(source, "file")
	mustExecFile(t, filePath)
	if _, err := Discover(ctx, filePath); err == nil {
		t.Fatal("expected file source error")
	}
	if _, _, err := openReadOnly(filepath.Join(source, "missing.sqlite")); err == nil {
		t.Fatal("expected read-only open error")
	}
	if err := copyFileIfExists(filepath.Join(source, "missing.sqlite"), filepath.Join(t.TempDir(), "missing.sqlite")); err != nil {
		t.Fatal(err)
	}

	if !appleNullTime(sql.NullFloat64{}).IsZero() {
		t.Fatal("invalid apple null time should be zero")
	}
	want := time.Unix(appleEpoch+42, 0).UTC()
	if got := appleTime(42); !got.Equal(want) {
		t.Fatalf("appleTime = %s, want %s", got, want)
	}
}

func TestExtractWithoutContactsDB(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	if err := os.Remove(filepath.Join(source, contactsDBName)); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(snap.Root) }()
	data, err := Extract(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Contacts) != 0 || len(data.Messages) == 0 {
		t.Fatalf("unexpected data without contacts: %+v", data)
	}
}

func TestExtractReportsBrokenChatSchema(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("create table nope(v integer)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(snap.Root) }()
	if _, err := Extract(ctx, snap); err == nil {
		t.Fatal("expected broken schema error")
	}
}

func TestClassifiers(t *testing.T) {
	chatKinds := map[string]string{
		"123@g.us":           "group",
		"123@newsletter":     "newsletter",
		"123@status":         "status",
		"status@broadcast":   "status",
		"123@s.whatsapp.net": "dm",
	}
	for jid, want := range chatKinds {
		if got := chatKind(jid, 0); got != want {
			t.Fatalf("chatKind(%q) = %q, want %q", jid, got, want)
		}
	}
	if got := chatKind("123@s.whatsapp.net", 3); got != "status" {
		t.Fatalf("raw status chatKind = %q", got)
	}

	messageTypes := map[int]string{
		0: "text", 1: "image", 2: "video", 3: "audio", 4: "location", 5: "contact",
		6: "system", 7: "link", 8: "document", 10: "group_event", 11: "gif",
		14: "reaction", 15: "sticker", 99: "type_99",
	}
	for raw, want := range messageTypes {
		if got := messageType(raw); got != want {
			t.Fatalf("messageType(%d) = %q, want %q", raw, got, want)
		}
	}
	mediaTypes := map[int]string{1: "image", 2: "video", 3: "audio", 7: "link", 8: "document", 11: "gif", 15: "sticker", 99: ""}
	for raw, want := range mediaTypes {
		if got := mediaType(raw); got != want {
			t.Fatalf("mediaType(%d) = %q, want %q", raw, got, want)
		}
	}
}

func createFixtureDBs(t *testing.T, dir string) {
	t.Helper()
	chat, err := sql.Open("sqlite", filepath.Join(dir, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = chat.Close() }()
	mustExec(t, chat, `
create table ZWACHATSESSION (Z_PK integer primary key, ZCONTACTJID varchar, ZPARTNERNAME varchar, ZLASTMESSAGEDATE timestamp, ZUNREADCOUNT integer, ZARCHIVED integer, ZREMOVED integer, ZHIDDEN integer, ZSESSIONTYPE integer);
create table ZWAGROUPINFO (Z_PK integer primary key, ZCHATSESSION integer, ZOWNERJID varchar, ZCREATIONDATE timestamp);
create table ZWAGROUPMEMBER (Z_PK integer primary key, ZCHATSESSION integer, ZMEMBERJID varchar, ZCONTACTNAME varchar, ZFIRSTNAME varchar, ZISADMIN integer, ZISACTIVE integer);
create table ZWAMEDIAITEM (Z_PK integer primary key, ZMESSAGE integer, ZMEDIALOCALPATH varchar, ZMEDIAURL varchar, ZTITLE varchar, ZVCARDNAME varchar, ZFILESIZE integer);
create table ZWAMESSAGE (Z_PK integer primary key, ZCHATSESSION integer, ZGROUPMEMBER integer, ZMEDIAITEM integer, ZSTANZAID varchar, ZISFROMME integer, ZMESSAGEDATE timestamp, ZTEXT varchar, ZMESSAGETYPE integer, ZSTARRED integer, ZFROMJID varchar, ZTOJID varchar, ZPUSHNAME varchar);
insert into ZWACHATSESSION values (1, '111@s.whatsapp.net', 'Bob', 700000020, 0, 0, 0, 0, 0);
insert into ZWACHATSESSION values (2, '123@g.us', 'Launch Group', 700000010, 2, 0, 0, 0, 1);
insert into ZWAGROUPINFO values (1, 2, 'owner@s.whatsapp.net', 699999000);
insert into ZWAGROUPMEMBER values (1, 2, '222@lid', 'Alice', 'Alice', 1, 1);
insert into ZWAMEDIAITEM values (1, 3, 'Media/123@g.us/a/test.jpg', 'https://example.invalid/media.enc', 'launch image', '', 42);
insert into ZWAMESSAGE values (1, 1, null, null, 'dm-in', 0, 700000000, 'hello', 0, 0, '111@s.whatsapp.net', '', 'Bob');
insert into ZWAMESSAGE values (2, 1, null, null, 'dm-out', 1, 700000001, 'roger', 0, 0, '', '111@s.whatsapp.net', '');
insert into ZWAMESSAGE values (3, 2, 1, 1, 'group-image', 0, 700000002, 'launch now', 1, 1, '123@g.us', '', 'Alice');
insert into ZWAMESSAGE values (4, 1, null, null, 'dm-in', 0, 700000003, 'duplicate stanza id', 0, 0, '111@s.whatsapp.net', '', 'Bob');
`)

	contacts, err := sql.Open("sqlite", filepath.Join(dir, contactsDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contacts.Close() }()
	mustExec(t, contacts, `
create table ZWAADDRESSBOOKCONTACT (ZWHATSAPPID varchar, ZPHONENUMBER varchar, ZFULLNAME varchar, ZGIVENNAME varchar, ZLASTNAME varchar, ZBUSINESSNAME varchar, ZUSERNAME varchar, ZLID varchar, ZABOUTTEXT varchar, ZLASTUPDATED timestamp);
insert into ZWAADDRESSBOOKCONTACT values ('111@s.whatsapp.net', '+111', 'Bob', 'Bob', '', '', '', '', '', 700000000);
insert into ZWAADDRESSBOOKCONTACT values ('222@s.whatsapp.net', '+222', 'Alice Contact', 'Alice', '', '', '', '222', '', 700000000);
`)
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func mustExecFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("create table t(v integer)"); err != nil {
		t.Fatal(err)
	}
}
