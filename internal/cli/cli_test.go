package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steipete/wacrawl/internal/store"
	"github.com/steipete/wacrawl/internal/whatsappdb"
	_ "modernc.org/sqlite"
)

func TestRunEndToEnd(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"--db", dbPath, "help"}, "wacrawl reads local WhatsApp"},
		{"version", []string{"--version"}, version},
		{"metadata", []string{"--json", "metadata"}, `"id": "wacrawl"`},
		{"doctor", []string{"--db", dbPath, "--source", source, "doctor"}, "message_rows"},
		{"import", []string{"--db", dbPath, "--source", source, "import"}, "messages=3"},
		{"import copy media", []string{"--db", dbPath, "--source", source, "import", "--copy-media"}, "media_copied=1"},
		{"status", []string{"--db", dbPath, "status"}, "unread_messages=2"},
		{"chats", []string{"--db", dbPath, "chats", "--limit", "5"}, "UNREAD"},
		{"contacts export", []string{"--db", dbPath, "--json", "--sync", "never", "contacts", "export"}, `"display_name": "Alice Contact"`},
		{"chats unread", []string{"--db", dbPath, "chats", "--unread", "--limit", "5"}, "Launch Group"},
		{"unread", []string{"--db", dbPath, "unread", "--limit", "5"}, "Launch Group"},
		{"messages", []string{"--db", dbPath, "messages", "--chat", "123@g.us", "--asc"}, "launch now"},
		{"search", []string{"--db", dbPath, "search", "--limit", "5", "launch"}, "[launch] now"},
		{"search flags after query", []string{"--db", dbPath, "search", "launch", "--limit", "5"}, "[launch] now"},
		{"json", []string{"--db", dbPath, "--json", "search", "launch"}, `"message_id"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(ctx, tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run() error = %v stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
}

func TestContactsExportUsesContractShapeAndSkipsUnsafeNames(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	contacts := []store.Contact{
		{JID: "safe@s.whatsapp.net", Phone: "+15550100", FullName: "Safe Person"},
		{JID: "business@s.whatsapp.net", Phone: "+15550101", BusinessName: "Business Name"},
		{JID: "first-last@s.whatsapp.net", Phone: "+15550102", FirstName: "First", LastName: "Last"},
		{JID: "username@s.whatsapp.net", Phone: "+15550103", Username: "handle", FullName: "@handle"},
		{JID: "phone@s.whatsapp.net", Phone: "+15550104", FullName: "+15550104"},
		{JID: "jid@s.whatsapp.net", Phone: "+15550105", FullName: "jid@s.whatsapp.net"},
		{JID: "blank@s.whatsapp.net", Phone: "+15550106"},
		{JID: "missing-phone@s.whatsapp.net", FullName: "Missing Phone"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, contacts, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "--sync", "never", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		Contacts []struct {
			DisplayName  string   `json:"display_name"`
			PhoneNumbers []string `json:"phone_numbers"`
			JID          string   `json:"jid"`
			Username     string   `json:"username"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", stdout.String(), err)
	}
	assertContactExportKeys(t, stdout.Bytes())
	gotNames := make([]string, 0, len(payload.Contacts))
	for _, contact := range payload.Contacts {
		gotNames = append(gotNames, contact.DisplayName)
		if contact.JID != "" || contact.Username != "" {
			t.Fatalf("leaked source fields = %#v", contact)
		}
		if len(contact.PhoneNumbers) != 1 {
			t.Fatalf("bad phone numbers = %#v", contact)
		}
	}
	wantNames := []string{"Business Name", "First Last", "Safe Person"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %#v, want %#v", gotNames, wantNames)
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(ctx, []string{"--db", dbPath, "--source", filepath.Join(t.TempDir(), "missing"), "--sync", "always", "contacts", "export"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "source unavailable") {
		t.Fatalf("expected --sync always to fail without source, got %v", err)
	}
}

func assertContactExportKeys(t *testing.T, data []byte) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	contactsJSON, ok := root["contacts"]
	if !ok || len(root) != 1 {
		t.Fatalf("root keys = %#v, want only contacts", root)
	}
	var contacts []map[string]json.RawMessage
	if err := json.Unmarshal(contactsJSON, &contacts); err != nil {
		t.Fatal(err)
	}
	for _, contact := range contacts {
		if _, ok := contact["display_name"]; !ok {
			t.Fatalf("contact keys = %#v, missing display_name", contact)
		}
		if _, ok := contact["phone_numbers"]; !ok {
			t.Fatalf("contact keys = %#v, missing phone_numbers", contact)
		}
		if len(contact) != 2 {
			t.Fatalf("contact keys = %#v, want only display_name and phone_numbers", contact)
		}
	}
}

func TestMetadataAdvertisesContactExport(t *testing.T) {
	manifest := controlManifest()
	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"wacrawl", "--json", "--sync", "never", "contacts", "export"}
	if !reflect.DeepEqual(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
}

func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("empty args should print help: %v", err)
	}
	err := Run(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage exit, got err=%v code=%d", err, ExitCode(err))
	}
	if ExitCode(nil) != 0 {
		t.Fatal("nil exit code should be zero")
	}
	if ExitCode(errors.New("plain")) != 1 {
		t.Fatal("plain error exit code should be one")
	}

	err = Run(context.Background(), []string{"messages", "--from-me", "--from-them"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
	err = Run(context.Background(), []string{"messages", "--after", "nope"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid time") {
		t.Fatalf("expected invalid time error, got %v", err)
	}
	err = Run(context.Background(), []string{"search"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "exactly one query") {
		t.Fatalf("expected query error, got %v", err)
	}
	err = Run(context.Background(), []string{"doctor", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Fatalf("expected doctor args error, got %v", err)
	}
	for _, args := range [][]string{
		{"import", "extra"},
		{"sync", "extra"},
		{"chats", "extra"},
		{"messages", "extra"},
		{"unread", "extra"},
	} {
		err = Run(context.Background(), args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "flags only") {
			t.Fatalf("expected flags-only error for %v, got %v", args, err)
		}
	}
	err = Run(context.Background(), []string{"--db", "", "status"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "db path is required") {
		t.Fatalf("expected db path error, got %v", err)
	}
	err = Run(context.Background(), []string{"--sync", "sometimes", "status"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--sync must be one of") {
		t.Fatalf("expected sync mode error, got %v", err)
	}
	err = Run(context.Background(), []string{"status", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Fatalf("expected status args error, got %v", err)
	}
}

func TestRunHelpMenus(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"global short", []string{"--help"}, "Examples:"},
		{"backup help", []string{"backup", "help"}, "wacrawl backup <init|push|pull|status>"},
		{"backup topic", []string{"help", "backup"}, "wacrawl backup <init|push|pull|status>"},
		{"doctor topic", []string{"help", "doctor"}, "wacrawl doctor [--source PATH]"},
		{"command help topic", []string{"help", "messages"}, "wacrawl messages [flags]"},
		{"doctor flag", []string{"doctor", "--help"}, "wacrawl doctor [--source PATH]"},
		{"status flag", []string{"status", "--help"}, "unread counts"},
		{"chats flag", []string{"chats", "--help"}, "wacrawl chats [--limit N] [--unread]"},
		{"unread flag", []string{"unread", "--help"}, "wacrawl unread [--limit N]"},
		{"command flag", []string{"messages", "--help"}, "--has-media"},
		{"search flag", []string{"search", "--help"}, "wacrawl search [flags] <query>"},
		{"import flag", []string{"import", "--help"}, "--copy-media"},
		{"sync topic", []string{"help", "sync"}, "wacrawl sync [--source PATH]"},
		{"backup flag", []string{"backup", "--help"}, "wacrawl backup <init|push|pull|status>"},
		{"backup nested flag", []string{"backup", "init", "--help"}, "wacrawl backup init [flags]"},
		{"backup nested topic", []string{"backup", "help", "push"}, "wacrawl backup push [flags]"},
		{"backup pull topic", []string{"help", "backup", "pull"}, "wacrawl backup pull [flags]"},
		{"backup status topic", []string{"help", "backup", "status"}, "wacrawl backup status [flags]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run() error = %v stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
	var discard bytes.Buffer
	if printCommandUsage(&discard, "missing") {
		t.Fatal("unknown help topic should return false")
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"help", "missing"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown help topic") {
		t.Fatalf("expected unknown help topic error, got %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err = Run(context.Background(), []string{"backup", "help", "missing"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown backup help topic") {
		t.Fatalf("expected unknown backup help topic error, got %v", err)
	}
}

func TestReadCommandsSyncArchive(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", source, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "messages=3") || !strings.Contains(stdout.String(), "last_import=20") {
		t.Fatalf("status should auto-sync archive:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "sync: syncing WhatsApp Desktop snapshot") {
		t.Fatalf("stderr should note sync, got %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "archive.db"), "--source", source, "--sync", "never", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status --sync never error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "messages=0") || !strings.Contains(stdout.String(), "last_import=-") {
		t.Fatalf("status should stay archive-only with --sync never:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "archive.db"), "--source", source, "--sync", "always", "search", "launch"}, &stdout, &stderr); err != nil {
		t.Fatalf("search --sync always error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[launch] now") {
		t.Fatalf("search should sync before reading:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "archive.db"), "--source", filepath.Join(source, "missing"), "--sync", "always", "status"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "source unavailable") {
		t.Fatalf("expected --sync always to fail without source, got %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "contacts.db"), "--source", source, "--sync", "always", "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export --sync always error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"display_name": "Alice Contact"`) {
		t.Fatalf("contacts export should sync before reading:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "sync: syncing WhatsApp Desktop snapshot") {
		t.Fatalf("contacts export should report sync before reading, got %q", stderr.String())
	}

	addDesktopContact(t, source, "333@s.whatsapp.net", "+333", "Charlie Contact")
	autoDB := filepath.Join(t.TempDir(), "auto-contacts.db")
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", autoDB, "--source", source, "--sync", "always", "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("seed contact auto-sync archive: %v stderr=%s", err, stderr.String())
	}
	addDesktopContact(t, source, "444@s.whatsapp.net", "+444", "Delta Contact")
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", autoDB, "--source", source, "--sync", "auto", "--sync-max-age", "0s", "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export --sync auto error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"display_name": "Delta Contact"`) {
		t.Fatalf("contacts export should auto-sync contact count drift:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "sync: syncing WhatsApp Desktop snapshot") {
		t.Fatalf("contacts export should report contact drift sync, got %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "contacts.db"), "--source", source, "--sync", "never", "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export --sync never error = %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), `"display_name"`) {
		t.Fatalf("contacts export should stay archive-only with --sync never:\n%s", stdout.String())
	}
}

func addDesktopContact(t *testing.T, dir, jid, phone, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ContactsV2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`insert into ZWAADDRESSBOOKCONTACT values (?, ?, ?, '', '', '', '', '', '', 700000000)`, jid, phone, name)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := exec.Command("git", "init", "--bare", remote).Run(); err != nil { // #nosec G204 -- test creates a temp bare Git remote.
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "backup.json")
	repo := filepath.Join(t.TempDir(), "backup")
	identity := filepath.Join(t.TempDir(), "age.key")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", source, "sync"}, &stdout, &stderr); err != nil {
		t.Fatalf("sync error = %v stderr=%s", err, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "init", "--config", config, "--repo", repo, "--remote", remote, "--identity", identity, "--no-push"}, &stdout, &stderr); err != nil {
		t.Fatalf("backup init error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recipient=age1") {
		t.Fatalf("backup init did not print recipient:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--sync", "never", "backup", "push", "--config", config, "--no-push"}, &stdout, &stderr); err != nil {
		t.Fatalf("backup push error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "encrypted=true") || !strings.Contains(stdout.String(), "messages=3") {
		t.Fatalf("backup push output mismatch:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "status", "--config", config}, &stdout, &stderr); err != nil {
		t.Fatalf("backup status error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "encrypted=true") || !strings.Contains(stdout.String(), "repo="+repo) {
		t.Fatalf("backup status output mismatch:\n%s", stdout.String())
	}
	restoredDB := filepath.Join(t.TempDir(), "restored.db")
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", restoredDB, "backup", "pull", "--config", config}, &stdout, &stderr); err != nil {
		t.Fatalf("backup pull error = %v stderr=%s", err, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", restoredDB, "--sync", "never", "search", "launch"}, &stdout, &stderr); err != nil {
		t.Fatalf("restored search error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[launch] now") {
		t.Fatalf("restored search mismatch:\n%s", stdout.String())
	}
}

func TestSyncArchiveKeepsExistingArchiveWhenSourceUnavailable(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC().Add(-time.Hour)
	if err := st.ReplaceAll(ctx,
		store.ImportStats{SourcePath: "/missing", DBPath: st.Path(), FinishedAt: now},
		nil,
		[]store.Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		nil,
		nil,
		[]store.Message{{SourcePK: 1, ChatJID: "chat", MessageID: "a", Timestamp: now, RawType: 0, Text: "cached"}},
	); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	a := app{stderr: &stderr, source: filepath.Join(t.TempDir(), "missing"), syncMode: archiveSyncAuto, syncMaxAge: 0}
	if err := a.syncArchive(ctx, st); err != nil {
		t.Fatalf("syncArchive should keep existing archive, got %v", err)
	}
	if !strings.Contains(stderr.String(), "using existing archive") {
		t.Fatalf("expected fallback warning, got %q", stderr.String())
	}
}

func TestSyncDecisionHelpers(t *testing.T) {
	now := time.Now().UTC()
	status := store.Status{Messages: 3, NewestMessage: now, LastImportAt: now.Add(-time.Hour)}
	if !archiveNeedsSyncCheck(status, 15*time.Minute) {
		t.Fatal("old import should need sync check")
	}
	if archiveNeedsSyncCheck(status, -1) {
		t.Fatal("negative max age should disable auto checks")
	}
	if !sourceAheadOfArchive(whatsappdb.Source{MessageRows: 3, NewestMessage: now.Add(time.Second).Format(time.RFC3339)}, status) {
		t.Fatal("newer source timestamp should be ahead")
	}
	if !sourceAheadOfArchive(whatsappdb.Source{MessageRows: 4, NewestMessage: now.Format(time.RFC3339)}, status) {
		t.Fatal("different source row count should be ahead")
	}
	if sourceAheadOfArchive(whatsappdb.Source{MessageRows: 3, NewestMessage: now.Format(time.RFC3339)}, status) {
		t.Fatal("equal source should not be ahead")
	}
	if sourceAheadOfArchive(whatsappdb.Source{MessageRows: 3, NewestMessage: "not-time"}, status) {
		t.Fatal("invalid source timestamp should not be ahead")
	}
	if !sourceAheadOfArchive(whatsappdb.Source{}, store.Status{}) {
		t.Fatal("empty archive should sync")
	}
}

func TestCLIHelpers(t *testing.T) {
	if _, err := parseTime("2026-04-25"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTime("2026-04-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if formatTime(timeZero()) != "-" {
		t.Fatal("zero time should format as dash")
	}
	if firstNonEmpty("", "x") != "x" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty mismatch")
	}
	args, query, err := splitSearchArgs([]string{"launch", "--limit", "5", "--from-them"})
	if err != nil {
		t.Fatal(err)
	}
	if query != "launch" || strings.Join(args, " ") != "--limit 5 --from-them" {
		t.Fatalf("unexpected split args=%v query=%q", args, query)
	}
	if _, _, err := splitSearchArgs([]string{"one", "two"}); err == nil {
		t.Fatal("expected multi-query split error")
	}
	if _, _, err := splitSearchArgs([]string{"--limit"}); err == nil {
		t.Fatal("expected missing flag value error")
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := bindMessageFlags(fs)
	if err := fs.Parse([]string{"--from-them", "--after", "2026-04-25", "--before", "2026-04-26"}); err != nil {
		t.Fatal(err)
	}
	filter, err := flags.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if filter.FromMe == nil || *filter.FromMe || filter.After == nil || filter.Before == nil {
		t.Fatalf("unexpected resolved filter: %+v", filter)
	}
	if mode, err := parseArchiveSyncMode("auto"); err != nil || mode != archiveSyncAuto {
		t.Fatalf("unexpected sync mode parse: %q %v", mode, err)
	}
	if _, err := parseArchiveSyncMode("nope"); err == nil {
		t.Fatal("expected invalid sync mode error")
	}
}

func timeZero() (out time.Time) { return out }

func createDesktopFixture(t *testing.T, dir string) {
	t.Helper()
	chat, err := sql.Open("sqlite", filepath.Join(dir, "ChatStorage.sqlite"))
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
`)
	contacts, err := sql.Open("sqlite", filepath.Join(dir, "ContactsV2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contacts.Close() }()
	mustExec(t, contacts, `
create table ZWAADDRESSBOOKCONTACT (ZWHATSAPPID varchar, ZPHONENUMBER varchar, ZFULLNAME varchar, ZGIVENNAME varchar, ZLASTNAME varchar, ZBUSINESSNAME varchar, ZUSERNAME varchar, ZLID varchar, ZABOUTTEXT varchar, ZLASTUPDATED timestamp);
insert into ZWAADDRESSBOOKCONTACT values ('111@s.whatsapp.net', '+111', 'Bob', 'Bob', '', '', '', '', '', 700000000);
insert into ZWAADDRESSBOOKCONTACT values ('222@s.whatsapp.net', '+222', 'Alice Contact', 'Alice', '', '', '', '222', '', 700000000);
`)
	mediaPath := filepath.Join(dir, "Media", "123@g.us", "a", "test.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
