package whatsappdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
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
	if stats.SourceIdentity == "" || stats.SourceStoreIdentity == "" || stats.AccountIdentity == "" || stats.SourceSnapshotAt.IsZero() || stats.SourceSnapshotAt.After(stats.FinishedAt) || status.LastSourceSnapshot.IsZero() || status.LastSourceNewest.IsZero() || !status.LastSourceNewest.Equal(time.Unix(appleEpoch+700000003, 0).UTC()) {
		t.Fatalf("missing source identity or watermark: stats=%+v status=%+v", stats, status)
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

func TestImportDesktopTurnsNullTextTransitionIntoTombstone(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatal(err)
	}

	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatDB.ExecContext(ctx, `update ZWAMESSAGE set ZTEXT=null where Z_PK=1`); err != nil {
		_ = chatDB.Close()
		t.Fatal(err)
	}
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatal(err)
	}
	status, err := archive.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 3 || status.DeletedMessages != 1 || status.MessageRevisions != 1 {
		t.Fatalf("null-text import status = %+v", status)
	}
	var reason, revision string
	if err := archive.DB().QueryRowContext(ctx, `select deletion_reason from messages where source_pk=1`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if err := archive.DB().QueryRowContext(ctx, `select payload_json from message_revisions where event_id='wa:1'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if reason != "whatsapp_payload_cleared" || !strings.Contains(revision, `"text":"hello"`) {
		t.Fatalf("reason=%q revision=%s", reason, revision)
	}
}

func TestImportDesktopRejectsAccountSwitchAtSameStore(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatal(err)
	}
	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, chatDB, `
delete from ZWAMESSAGE;
insert into ZWAMEDIAITEM values (2, 10, 'foreign.bin', '', 'foreign', '', 7);
insert into ZWAMESSAGE values (10, 1, null, 2, 'account-b', 0, 700000010, 'other account', 1, 0, '111@s.whatsapp.net', '', 'Other');`)
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}
	axolotlDB, err := sql.Open("sqlite", filepath.Join(source, axolotlDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, axolotlDB, `update ZWAZMDACCOUNT set ZACCOUNTJIDSTRING='other-owner@s.whatsapp.net', ZUSERJIDSTRING='other-owner@s.whatsapp.net'`)
	if err := axolotlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "foreign.bin"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ctx, archive, source); err == nil || !strings.Contains(err.Error(), "different WhatsApp account") {
		t.Fatalf("same-path account switch error = %v", err)
	}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	if _, err := ImportWithOptions(ctx, archive, ImportOptions{SourcePath: source, CopyMedia: true, MediaRoot: mediaRoot}); err == nil || !strings.Contains(err.Error(), "different WhatsApp account") {
		t.Fatalf("copy-media account switch error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "foreign.bin")); !os.IsNotExist(err) {
		t.Fatalf("rejected import wrote foreign media: %v", err)
	}
	if _, err := ImportWithOptions(ctx, archive, ImportOptions{SourcePath: source, Restore: true}); err != nil {
		t.Fatalf("explicit restore should switch accounts: %v", err)
	}
	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatalf("merge after restore should have event continuity: %v", err)
	}
}

func TestReadSourceIdentity(t *testing.T) {
	ctx := context.Background()
	t.Run("message churn does not rotate", func(t *testing.T) {
		source := t.TempDir()
		createFixtureDBs(t, source)
		before, err := readSourceIdentity(ctx, filepath.Join(source, chatDBName))
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `update ZWAMESSAGE set ZFROMJID='owner@s.whatsapp.net' where ZISFROMME=1`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readSourceIdentity(ctx, filepath.Join(source, chatDBName))
		if err != nil || identity != before || !strings.HasPrefix(identity, "wa-store:") {
			t.Fatalf("identity rotated: before=%q after=%q err=%v", before, identity, err)
		}
	})
	t.Run("missing marker", func(t *testing.T) {
		source := t.TempDir()
		createFixtureDBs(t, source)
		db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `drop table Z_METADATA`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readSourceIdentity(ctx, filepath.Join(source, chatDBName))
		if err != nil || identity != "" {
			t.Fatalf("missing identity = %q, %v", identity, err)
		}
	})
	t.Run("empty store uuid", func(t *testing.T) {
		source := t.TempDir()
		createFixtureDBs(t, source)
		db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `update Z_METADATA set Z_UUID=''`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readSourceIdentity(ctx, filepath.Join(source, chatDBName))
		if err != nil || identity != "" {
			t.Fatalf("empty identity = %q, %v", identity, err)
		}
	})
	t.Run("missing metadata row", func(t *testing.T) {
		source := t.TempDir()
		createFixtureDBs(t, source)
		db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `delete from Z_METADATA`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readSourceIdentity(ctx, filepath.Join(source, chatDBName))
		if err != nil || identity != "" {
			t.Fatalf("missing metadata identity = %q, %v", identity, err)
		}
	})
}

func TestReadAccountIdentity(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	path := filepath.Join(source, axolotlDBName)
	before, err := readAccountIdentity(ctx, path)
	if err != nil || !strings.HasPrefix(before, "wa-account:") {
		t.Fatalf("account identity = %q, %v", before, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `update ZWAZMDACCOUNT set ZUSERJIDSTRING='other-owner@s.whatsapp.net'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := readAccountIdentity(ctx, path)
	if err != nil || after == before || !strings.HasPrefix(after, "wa-account:") {
		t.Fatalf("account switch identity: before=%q after=%q err=%v", before, after, err)
	}
	if identity, err := readAccountIdentity(ctx, filepath.Join(t.TempDir(), axolotlDBName)); err != nil || identity != "" {
		t.Fatalf("missing account database identity = %q, %v", identity, err)
	}

	ambiguous := t.TempDir()
	createFixtureDBs(t, ambiguous)
	db, err = sql.Open("sqlite", filepath.Join(ambiguous, axolotlDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `insert into ZWAZMDACCOUNT values (2, 'second@s.whatsapp.net', 'second@s.whatsapp.net')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readAccountIdentity(ctx, filepath.Join(ambiguous, axolotlDBName)); err == nil || !strings.Contains(err.Error(), "multiple WhatsApp account identities") || strings.Contains(err.Error(), "--restore") {
		t.Fatalf("ambiguous account identity error = %v", err)
	}
}

func TestReadAccountIdentityFallbacks(t *testing.T) {
	ctx := context.Background()
	t.Run("ZMD account JID", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), axolotlDBName)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `create table ZWAZMDACCOUNT (ZACCOUNTJIDSTRING varchar, ZUSERJIDSTRING varchar); insert into ZWAZMDACCOUNT values ('owner@s.whatsapp.net', '')`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readAccountIdentity(ctx, path)
		if err != nil || !strings.HasPrefix(identity, "wa-account:") {
			t.Fatalf("ZMD account fallback = %q, %v", identity, err)
		}
	})
	t.Run("signal account columns", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), axolotlDBName)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `
create table ZWAAXOLOTLIDENTITY (ZACCOUNTJIDSTRING varchar);
create table ZWAAXOLOTLSESSION (ZACCOUNTJIDSTRING varchar);
create table ZWASENDERKEY (ZACCOUNTJIDSTRING varchar);
insert into ZWAAXOLOTLIDENTITY values ('OWNER@S.WHATSAPP.NET');
insert into ZWAAXOLOTLSESSION values ('owner@s.whatsapp.net');
insert into ZWASENDERKEY values ('owner@s.whatsapp.net');`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readAccountIdentity(ctx, path)
		if err != nil || !strings.HasPrefix(identity, "wa-account:") {
			t.Fatalf("signal fallback = %q, %v", identity, err)
		}
	})
	t.Run("empty schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), axolotlDBName)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `create table unrelated (value text)`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := readAccountIdentity(ctx, path)
		if err != nil || identity != "" {
			t.Fatalf("empty schema identity = %q, %v", identity, err)
		}
	})
}

func TestExtractRejectsAccountChangeDuringSnapshot(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	snap, err := SnapshotPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(snap.Root) }()
	db, err := sql.Open("sqlite", filepath.Join(snap.Root, axolotlDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `update ZWAZMDACCOUNT set ZUSERJIDSTRING='other@s.whatsapp.net'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(ctx, snap); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("snapshot account change error = %v", err)
	}
}

func TestImportDesktopUpgradesPathBindingToStoreFingerprint(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `drop table Z_METADATA`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	stats, err := Import(ctx, archive, source)
	if err != nil || stats.SourceStoreIdentity != "" {
		t.Fatalf("path-bound import = %+v, %v", stats, err)
	}
	db, err = sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `create table Z_METADATA (Z_VERSION integer primary key, Z_UUID varchar(255), Z_PLIST blob); insert into Z_METADATA values (1, 'late-store-marker', null)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stats, err = Import(ctx, archive, source)
	if err != nil || stats.SourceStoreIdentity == "" {
		t.Fatalf("store fingerprint upgrade = %+v, %v", stats, err)
	}
	var binding string
	if err := archive.DB().QueryRowContext(ctx, `select value from sync_state where key='merge_source_store_identity'`).Scan(&binding); err != nil || binding != stats.SourceStoreIdentity {
		t.Fatalf("store fingerprint binding = %q, %v", binding, err)
	}
	db, err = sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `drop table Z_METADATA`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ctx, archive, source); err == nil || !strings.Contains(err.Error(), "different WhatsApp Desktop store") {
		t.Fatalf("missing established store marker error = %v", err)
	}
}

func TestImportDesktopWithoutAccountIdentityCannotMergeNonemptyArchive(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	if err := os.Remove(filepath.Join(source, axolotlDBName)); err != nil {
		t.Fatal(err)
	}
	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatalf("initial unbound import: %v", err)
	}
	if _, err := Import(ctx, archive, source); err == nil || !strings.Contains(err.Error(), "--adopt-source") {
		t.Fatalf("unbound merge error = %v", err)
	}
	if _, err := ImportWithOptions(ctx, archive, ImportOptions{SourcePath: source, AdoptSource: true}); err == nil || !strings.Contains(err.Error(), "--adopt-source") {
		t.Fatalf("adoption without account identity error = %v", err)
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

func TestImportDesktopReadsMediaLinkedByMessage(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)

	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, chatDB, `
insert into ZWAMEDIAITEM values (2, 4, 'Media/111@s.whatsapp.net/fallback.pdf', 'https://example.invalid/fallback.enc', 'fallback title', '', 99);
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
	if stats.MediaMessages != 2 {
		t.Fatalf("expected media linked by ZMESSAGE to count, got %+v", stats)
	}
	messages, err := archive.Messages(ctx, store.MessageFilter{ChatJID: "111@s.whatsapp.net", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	var found store.Message
	for _, msg := range messages {
		if msg.SourcePK == 4 {
			found = msg
			break
		}
	}
	if found.MediaPath != filepath.Join(source, "Message", "Media", "111@s.whatsapp.net", "fallback.pdf") ||
		found.MediaURL != "https://example.invalid/fallback.enc" ||
		found.MediaTitle != "fallback title" ||
		found.MediaSize != 99 {
		t.Fatalf("media linked only through ZMESSAGE was not imported: %+v", found)
	}
}

func TestImportDesktopUsesProfilePushNames(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)

	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, chatDB, `
create table ZWAPROFILEPUSHNAME (Z_PK integer primary key, ZJID varchar, ZPUSHNAME varchar);
insert into ZWAPROFILEPUSHNAME values (1, '333@s.whatsapp.net', 'Profile Pat');
insert into ZWAGROUPMEMBER values (2, 2, '333@s.whatsapp.net', '', '+EAA=', 0, 1);
insert into ZWAMESSAGE values (5, 2, 2, null, 'profile-name', 0, 700000004, 'profile-backed sender', 0, 0, '123@g.us', '', '+EAA=');
`)
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}

	archive, err := store.Open(ctx, filepath.Join(t.TempDir(), "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	if _, err := Import(ctx, archive, source); err != nil {
		t.Fatal(err)
	}

	msgs, err := archive.Messages(ctx, store.MessageFilter{ChatJID: "123@g.us", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	var found store.Message
	for _, msg := range msgs {
		if msg.MessageID == "profile-name" {
			found = msg
			break
		}
	}
	if found.SenderJID != "333@s.whatsapp.net" || found.SenderName != "Profile Pat" {
		t.Fatalf("profile push name was not used for sender: %+v", found)
	}

	results, err := archive.Search(ctx, store.MessageFilter{Query: "Profile Pat", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "profile-name" {
		t.Fatalf("profile push name was not indexed for search: %+v", results)
	}
}

func TestSenderSkipsResolvedJIDFallback(t *testing.T) {
	jid, name := sender(false, "123@g.us", "444@s.whatsapp.net", "", "Readable Push", "", "", "", map[string]string{
		"444@s.whatsapp.net": "444@s.whatsapp.net",
	})
	if jid != "444@s.whatsapp.net" || name != "Readable Push" {
		t.Fatalf("sender used JID fallback before readable push name: jid=%q name=%q", jid, name)
	}
}

func TestCleanDesktopMediaRel(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"blank", "", ""},
		{"current", ".", ""},
		{"parent", "..", ""},
		{"parent prefix", filepath.Join("..", "..", "Media", "photo.jpg"), "photo.jpg"},
		{"absolute", filepath.Join(string(os.PathSeparator), "Media", "photo.jpg"), filepath.Join("Media", "photo.jpg")},
		{"normal", filepath.Join("Media", "chat", "photo.jpg"), filepath.Join("Media", "chat", "photo.jpg")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanDesktopMediaRel(tc.path); got != tc.want {
				t.Fatalf("cleanDesktopMediaRel(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestImportDesktopCopyMedia(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	mediaPath := filepath.Join(source, "Message", "Media", "123@g.us", "a", "test.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}

	chatDB, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, chatDB, `
insert into ZWAMEDIAITEM values (2, 5, 'Media/123@g.us/a/missing.jpg', 'https://example.invalid/missing.enc', 'missing image', '', 7);
insert into ZWAMESSAGE values (5, 2, 1, 2, 'missing-media', 0, 700000004, 'missing media', 1, 0, '123@g.us', '', 'Alice');
`)
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "archive.db")
	archive, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	stats, err := ImportWithOptions(ctx, archive, ImportOptions{SourcePath: source, CopyMedia: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.MediaCopied != 1 || stats.MediaMissing != 1 || stats.MediaMessages != 2 {
		t.Fatalf("unexpected media stats: %+v", stats)
	}

	msgs, err := archive.Messages(ctx, store.MessageFilter{ChatJID: "123@g.us", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	var copiedPath, missingPath string
	for _, msg := range msgs {
		switch msg.MessageID {
		case "group-image":
			copiedPath = msg.MediaPath
		case "missing-media":
			missingPath = msg.MediaPath
		}
	}
	wantCopied := filepath.Join(filepath.Dir(archivePath), "media", "Message", "Media", "123@g.us", "a", "test.jpg")
	if copiedPath != wantCopied {
		t.Fatalf("copied media path = %q, want %q", copiedPath, wantCopied)
	}
	if data, err := os.ReadFile(copiedPath); err != nil || string(data) != "image" { // #nosec G304 -- copiedPath is asserted against the expected temp archive path above.
		t.Fatalf("copied media content = %q err=%v", data, err)
	}
	wantMissing := filepath.Join(source, "Message", "Media", "123@g.us", "a", "missing.jpg")
	if missingPath != wantMissing {
		t.Fatalf("missing media path = %q, want original %q", missingPath, wantMissing)
	}
}

func TestResolveDesktopMediaPathPrefersMessageMedia(t *testing.T) {
	source := t.TempDir()
	messageMedia := filepath.Join(source, "Message", "Media", "chat", "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(messageMedia), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(messageMedia, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveDesktopMediaPath(source, "Media/chat/photo.jpg"); got != messageMedia {
		t.Fatalf("resolved media path = %q, want %q", got, messageMedia)
	}

	legacyMedia := filepath.Join(source, "Media", "chat", "legacy.jpg")
	if err := os.MkdirAll(filepath.Dir(legacyMedia), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyMedia, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveDesktopMediaPath(source, "Media/chat/legacy.jpg"); got != legacyMedia {
		t.Fatalf("legacy media path = %q, want %q", got, legacyMedia)
	}

	missing := filepath.Join(source, "Message", "Media", "chat", "missing.jpg")
	if got := resolveDesktopMediaPath(source, "Media/chat/missing.jpg"); got != missing {
		t.Fatalf("missing media path = %q, want %q", got, missing)
	}

	absolute := filepath.Join(string(os.PathSeparator), "tmp", "outside.jpg")
	confined := filepath.Join(source, "tmp", "outside.jpg")
	if got := resolveDesktopMediaPath(source, absolute); got != confined {
		t.Fatalf("absolute media path = %q, want confined %q", got, confined)
	}

	traversal := filepath.Join(source, "outside.jpg")
	if got := resolveDesktopMediaPath(source, "../outside.jpg"); got != traversal {
		t.Fatalf("traversal media path = %q, want confined %q", got, traversal)
	}
}

func TestCopyArchiveMediaDeduplicatesAndConfinesPaths(t *testing.T) {
	source := t.TempDir()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mediaPath := filepath.Join(source, "Message", "Media", "chat", "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(source, "Message", "Media", "chat", "missing.jpg")
	messages := []store.Message{
		{MediaPath: mediaPath},
		{MediaPath: mediaPath},
		{MediaPath: missingPath},
		{MediaPath: missingPath},
	}

	copied, missing, err := copyArchiveMedia(messages, source, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 || missing != 1 {
		t.Fatalf("copy stats = %d/%d, want 1/1", copied, missing)
	}
	wantCopied := filepath.Join(mediaRoot, "Message", "Media", "chat", "photo.jpg")
	if messages[0].MediaPath != wantCopied || messages[1].MediaPath != wantCopied {
		t.Fatalf("duplicate copied media paths not rewritten: %+v", messages[:2])
	}
	if messages[2].MediaPath != missingPath || messages[3].MediaPath != missingPath {
		t.Fatalf("duplicate missing media paths should stay original: %+v", messages[2:])
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.jpg")
	dest, err := archiveMediaPath(source, mediaRoot, outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(mediaRoot, "outside.jpg") {
		t.Fatalf("outside path fallback = %q", dest)
	}
	if _, err := archiveMediaPath(source, mediaRoot, source); err == nil {
		t.Fatal("expected source root path to be rejected")
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

func TestReadProfilePushNamesReportsBrokenOptionalSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), chatDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mustExec(t, db, `create table ZWAPROFILEPUSHNAME (Z_PK integer primary key);`)
	if _, err := readProfilePushNameRows(ctx, db); err == nil {
		t.Fatal("expected broken profile push name schema error")
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

func TestCanonicalSourcePath(t *testing.T) {
	if path, err := canonicalSourcePath(""); err != nil || path != "" {
		t.Fatalf("empty canonical path = %q, %v", path, err)
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "source-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path, err := canonicalSourcePath(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("canonical path = %q, want %q", path, want)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	path, err = canonicalSourcePath(missing)
	if err != nil {
		t.Fatal(err)
	}
	if path != missing {
		t.Fatalf("missing canonical path = %q, want %q", path, missing)
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
create table Z_METADATA (Z_VERSION integer primary key, Z_UUID varchar(255), Z_PLIST blob);
insert into Z_METADATA values (1, 'fixture-account-a', null);
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
	axolotl, err := sql.Open("sqlite", filepath.Join(dir, axolotlDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = axolotl.Close() }()
	mustExec(t, axolotl, `
create table ZWAZMDACCOUNT (Z_PK integer primary key, ZACCOUNTJIDSTRING varchar, ZUSERJIDSTRING varchar);
insert into ZWAZMDACCOUNT values (1, 'fixture-owner@s.whatsapp.net', 'fixture-owner@s.whatsapp.net');
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
