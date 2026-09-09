package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func reloginStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func reloginStats(source string) ImportStats {
	return ImportStats{SourcePath: "/fixture", SourceIdentity: "/fixture", SourceStoreIdentity: "wa-store:" + source, AccountIdentity: "wa-account:owner", FinishedAt: time.Unix(1800000000, 0).UTC()}
}

func reloginMessage(pk int64, id string) Message {
	return Message{SourcePK: pk, ChatJID: "chat@g.us", MessageID: id, SenderJID: "sender@s.whatsapp.net", Timestamp: time.Unix(1750000000, 0).UTC(), Text: "body " + id, RawType: 0, MessageType: "text"}
}

func mergeRelogin(t *testing.T, st *Store, source string, messages ...Message) {
	t.Helper()
	stats := reloginStats(source)
	stats.MediaRoot = filepath.Join(filepath.Dir(st.Path()), "media")
	if err := st.ValidateImport(context.Background(), stats, messages, false); err != nil {
		t.Fatal(err)
	}
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
}

func snapshotRelogin(t *testing.T, st *Store) SnapshotData {
	t.Helper()
	data, err := st.ExportAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := data.Validate(); err != nil {
		t.Fatal(err)
	}
	return data
}

func assertRepeat(t *testing.T, st *Store, source string, messages ...Message) {
	t.Helper()
	before := snapshotRelogin(t, st)
	mergeRelogin(t, st, source, messages...)
	after := snapshotRelogin(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("repeat import changed canonical snapshot: messages %d/%d revisions %d/%d sources %d/%d observations %d/%d", len(before.Messages), len(after.Messages), len(before.Revisions), len(after.Revisions), len(before.Sources), len(after.Sources), len(before.Observations), len(after.Observations))
	}
}

func TestRecurringReloginsKeepEventsAndProvenance(t *testing.T) {
	st := reloginStore(t)
	old := reloginMessage(1, "old-only")
	shared := reloginMessage(2, "shared")
	shared.MediaType = "image"
	shared.MediaPath = filepath.Join(filepath.Dir(st.Path()), "media", "old.jpg")
	if err := os.MkdirAll(filepath.Dir(shared.MediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared.MediaPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared.MediaSize = 42
	mergeRelogin(t, st, "first", old, shared)
	secondShared := shared
	secondShared.SourcePK = 1
	secondShared.MediaPath = "/new/unavailable.jpg"
	secondShared.ChatName = "New name"
	secondShared.SenderName = "New sender name"
	fresh := reloginMessage(2, "second-only")
	mergeRelogin(t, st, "second", secondShared, fresh)
	data := snapshotRelogin(t, st)
	if len(data.Messages) != 3 || len(data.Revisions) != 0 || len(data.Sources) != 4 {
		t.Fatalf("unexpected counts: %d %d %d", len(data.Messages), len(data.Revisions), len(data.Sources))
	}
	preserved, err := st.MessageBySourcePK(context.Background(), 2)
	if err != nil || preserved.EventID != "wa:2" || preserved.MediaPath != shared.MediaPath || preserved.MediaSize != 42 {
		t.Fatalf("lost stable key/media: %+v %v", preserved, err)
	}
	assertRepeat(t, st, "second", secondShared, fresh)
	thirdShared := secondShared
	thirdShared.SourcePK = 300
	thirdShared.Text = "edited shared"
	thirdFresh := fresh
	thirdFresh.SourcePK = 1
	newest := reloginMessage(2, "third-only")
	mergeRelogin(t, st, "third", thirdShared, thirdFresh, newest)
	data = snapshotRelogin(t, st)
	if len(data.Messages) != 4 || len(data.Revisions) != 1 || len(data.Sources) != 7 {
		t.Fatalf("third counts: %d %d %d", len(data.Messages), len(data.Revisions), len(data.Sources))
	}
	revision := data.Revisions[0]
	if revision.EventID != "wa:2" || revision.SourceStoreIdentity != "wa-store:third" || revision.AccountIdentity != "wa-account:owner" || revision.SourceRowPK != 300 || !strings.Contains(revision.PayloadJSON, shared.Text) {
		t.Fatalf("revision provenance lost: %+v", revision)
	}
	assertRepeat(t, st, "third", thirdShared, thirdFresh, newest)
	results, err := st.Search(context.Background(), MessageFilter{Query: "old", Limit: 20})
	if err != nil || len(results) != 1 || results[0].EventID != "wa:1" {
		t.Fatalf("old history not searchable: %v %d", err, len(results))
	}
	// Returning to an earlier source is also idempotent after its updated payload
	// has been observed. Bindings are not limited to the latest store.
	blob, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SnapshotData
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatal(err)
	}
	restored := reloginStore(t)
	if err := restored.ImportSnapshot(context.Background(), decoded, "backup:/fixture", reloginStats("third").FinishedAt); err != nil {
		t.Fatal(err)
	}
	if got := snapshotRelogin(t, restored); !reflect.DeepEqual(data, got) {
		t.Fatal("canonical export/restore changed provenance")
	}
	// Restore into a different media root has no attachment bytes yet. The
	// first refresh selects the incoming source path; subsequent imports are stable.
	mergeRelogin(t, restored, "third", thirdShared, thirdFresh, newest)
	assertRepeat(t, restored, "third", thirdShared, thirdFresh, newest)
}

func TestReloginReusedRowsReactionsAndTombstones(t *testing.T) {
	st := reloginStore(t)
	original := reloginMessage(1, "original")
	mergeRelogin(t, st, "first", original)
	reaction := reloginMessage(1, "reaction")
	reaction.RawType = 14
	reaction.MessageType = "reaction"
	reaction.MediaTitle = original.MessageID
	reaction.Text = ""
	reaction.SourceTextNull = true
	mergeRelogin(t, st, "first", reaction)
	assertRepeat(t, st, "first", reaction)
	revoke := reaction
	revoke.MessageID = "revoke"
	revoke.Text = "123@lid"
	revoke.SourceTextNull = false
	mergeRelogin(t, st, "first", revoke)
	assertRepeat(t, st, "first", revoke)
	reaction.SourcePK = 30
	revoke.SourcePK = 31
	original.SourcePK = 32
	mergeRelogin(t, st, "second", original, reaction, revoke)
	if got := snapshotRelogin(t, st); len(got.Messages) != 3 || len(got.Revisions) != 0 {
		t.Fatalf("reactions duplicated or revised: %d %d", len(got.Messages), len(got.Revisions))
	}
	original.Text = ""
	original.SourceTextNull = true
	mergeRelogin(t, st, "second", original, reaction, revoke)
	data := snapshotRelogin(t, st)
	if len(data.Revisions) != 1 {
		t.Fatalf("cleared body revision count %d", len(data.Revisions))
	}
	var saved Message
	for _, m := range data.Messages {
		if m.EventID == "wa:1" {
			saved = m
		}
	}
	if saved.DeletedAt.IsZero() {
		t.Fatal("missing tombstone")
	}
	assertRepeat(t, st, "second", original, reaction, revoke)
	original.SourcePK = 99
	reaction.SourcePK = 98
	revoke.SourcePK = 97
	mergeRelogin(t, st, "third", original, reaction, revoke)
	assertRepeat(t, st, "third", original, reaction, revoke)
	if len(snapshotRelogin(t, st).Observations) != 10 {
		t.Fatalf("sticky tombstone source observations lost: %d", len(snapshotRelogin(t, st).Observations))
	}
	if len(snapshotRelogin(t, st).Messages) != 3 {
		t.Fatal("third login lost reaction identity")
	}
}

func TestReloginAmbiguityAndIncompleteIdentityStaySeparate(t *testing.T) {
	for _, mode := range []string{"old-duplicate", "new-duplicate", "missing-sender", "changed-sender", "changed-type", "changed-target"} {
		t.Run(mode, func(t *testing.T) {
			st := reloginStore(t)
			old := reloginMessage(1, "shared")
			incoming := old
			incoming.SourcePK = 20
			olds := []Message{old}
			news := []Message{incoming}
			switch mode {
			case "old-duplicate":
				duplicate := old
				duplicate.SourcePK = 2
				olds = append(olds, duplicate)
			case "new-duplicate":
				duplicate := incoming
				duplicate.SourcePK = 21
				news = append(news, duplicate)
			case "missing-sender":
				olds[0].SenderJID = ""
				news[0].SenderJID = ""
			case "changed-sender":
				news[0].SenderJID = "other@s.whatsapp.net"
			case "changed-type":
				news[0].RawType = 1
			case "changed-target":
				olds[0].RawType = 14
				olds[0].MediaTitle = "one"
				news[0].RawType = 14
				news[0].MediaTitle = "two"
			}
			mergeRelogin(t, st, "first", olds...)
			mergeRelogin(t, st, "second", news...)
			if len(snapshotRelogin(t, st).Messages) != len(olds)+len(news) {
				t.Fatal("uncertain identities forcibly merged")
			}
			assertRepeat(t, st, "second", news...)
		})
	}
}

func TestReloginRejectsUnknownDifferentAccountAndRollsBack(t *testing.T) {
	st := reloginStore(t)
	original := reloginMessage(1, "shared")
	mergeRelogin(t, st, "first", original)
	before := snapshotRelogin(t, st)
	for _, account := range []string{"", "wa-account:other"} {
		stats := reloginStats("second")
		stats.AccountIdentity = account
		stats.LegacyAccountIDs = []string{"wa-account:owner"}
		stats.AdoptSource = true
		if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{original}); err == nil {
			t.Fatal("account guard bypassed")
		}
		if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
			t.Fatal("rejected import changed archive")
		}
	}
	// Fail after mappings have been written to prove the entire merge rolls back.
	if _, err := st.DB().Exec(`create trigger fail_message before insert on messages begin select raise(abort,'fixture failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err := st.MergeAll(context.Background(), reloginStats("second"), nil, nil, nil, nil, []Message{reloginMessage(1, "new")}); err == nil {
		t.Fatal("expected injected failure")
	}
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
		t.Fatal("failed merge left source mappings")
	}
}

func TestV3SourceMigrationPreservesIDsRevisionsAndIsAtomic(t *testing.T) {
	ctx := context.Background()
	st := reloginStore(t)
	original := reloginMessage(10, "original")
	mergeRelogin(t, st, "first", original)
	original.Text = "edited"
	mergeRelogin(t, st, "first", original)
	before := snapshotRelogin(t, st)
	if _, err := st.DB().Exec(`drop table source_observations; drop table message_sources; update message_revisions set account_identity='',source_store_identity='',source_row_pk=0; pragma user_version=3`); err != nil {
		t.Fatal(err)
	}
	if err := st.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	migrated := snapshotRelogin(t, st)
	if !reflect.DeepEqual(before.Messages, migrated.Messages) || len(migrated.Sources) != 1 || len(migrated.Observations) != 1 || migrated.Revisions[0].PayloadJSON != before.Revisions[0].PayloadJSON || migrated.Revisions[0].SourceRowPK != 10 {
		t.Fatal("migration changed historical events/revisions")
	}
	assertRepeat(t, st, "first", original)
	if _, err := st.DB().Exec(`drop table source_observations; drop table message_sources; pragma user_version=3; update message_revisions set account_identity=''; create trigger fail_migration before update on message_revisions begin select raise(abort,'fixture migration failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err := st.migrate(ctx); err == nil {
		t.Fatal("expected failed migration")
	}
	var version int
	if err := st.DB().QueryRow(`pragma user_version`).Scan(&version); err != nil || version != 3 {
		t.Fatal("migration version not rolled back")
	}
	var name string
	if err := st.DB().QueryRow(`select name from sqlite_master where name='message_sources'`).Scan(&name); err != sql.ErrNoRows {
		t.Fatalf("migration schema not rolled back: %v", err)
	}
}

func TestReloginIncomingMediaUsesArchiveRoot(t *testing.T) {
	st := reloginStore(t)
	message := reloginMessage(1, "media")
	message.MediaType, message.MediaSize = "image", 5
	message.MediaPath = filepath.Join(filepath.Dir(st.Path()), "media", "old.jpg")
	mergeRelogin(t, st, "first", message)
	path := filepath.Join(filepath.Dir(st.Path()), "media", "new.jpg")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	message.SourcePK = 22
	message.MediaPath = path
	mergeRelogin(t, st, "second", message)
	saved, err := st.MessageBySourcePK(context.Background(), 1)
	if err != nil || saved.MediaPath != message.MediaPath {
		t.Fatalf("valid archive path not selected: %q %v", saved.MediaPath, err)
	}
	if len(snapshotRelogin(t, st).Revisions) != 0 {
		t.Fatal("path change manufactured revision")
	}
	// Missing incoming cache bytes retain the verified archived attachment.
	message.MediaPath = "/source/missing.jpg"
	mergeRelogin(t, st, "second", message)
	saved, err = st.MessageBySourcePK(context.Background(), 1)
	if err != nil || saved.MediaPath != path {
		t.Fatal("missing downloaded media erased old path")
	}
}

func TestReloginRetainsCanonicalPathGuard(t *testing.T) {
	st := reloginStore(t)
	message := reloginMessage(1, "one")
	mergeRelogin(t, st, "first", message)
	before := snapshotRelogin(t, st)
	stats := reloginStats("second")
	stats.SourceIdentity = "/different-desktop"
	stats.SourcePath = stats.SourceIdentity
	if err := st.ValidateImport(context.Background(), stats, []Message{message}, false); err == nil || !strings.Contains(err.Error(), "bound to WhatsApp source") {
		t.Fatalf("same-account path guard: %v", err)
	}
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err == nil {
		t.Fatal("different path merge accepted")
	}
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
		t.Fatal("rejected path changed archive")
	}
}

func TestLegacyNormalizationPreservesSourcesButCannotAuthorizeRelogin(t *testing.T) {
	st := reloginStore(t)
	message := reloginMessage(1, "shared")
	mergeRelogin(t, st, "first", message)
	message.Text = "edited before normalization"
	mergeRelogin(t, st, "first", message)
	before := snapshotRelogin(t, st)
	stats := reloginStats("second")
	stats.AccountIdentity = "wa-account:normalized"
	stats.LegacyAccountIDs = []string{"wa-account:owner"}
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err == nil {
		t.Fatal("legacy candidate authorized new store")
	}
	stats.SourceStoreIdentity = "wa-store:first"
	if err := st.ValidateImport(context.Background(), stats, []Message{message}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err != nil {
		t.Fatal(err)
	}
	after := snapshotRelogin(t, st)
	if len(after.Messages) != len(before.Messages) || len(after.Revisions) != 1 || len(after.Sources) != 1 || len(after.Observations) != 2 || after.Sources[0].AccountIdentity != stats.AccountIdentity {
		t.Fatal("normalization changed historical events or lost mappings")
	}
	var orphaned int
	if err := st.DB().QueryRow(`select count(*) from message_revisions r where not exists (select 1 from message_sources s where s.account_identity=r.account_identity and s.source_store_identity=r.source_store_identity and s.source_row_pk=r.source_row_pk and s.event_id=r.event_id)`).Scan(&orphaned); err != nil || orphaned != 0 {
		t.Fatalf("normalized revision cannot join provenance: %d %v", orphaned, err)
	}
	if after.Revisions[0].AccountIdentity != stats.AccountIdentity || after.Revisions[0].PayloadJSON != before.Revisions[0].PayloadJSON {
		t.Fatal("revision provenance did not follow normalization")
	}
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err != nil {
		t.Fatal(err)
	}
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(after, got) {
		t.Fatal("normalization not idempotent")
	}
	stats.SourceStoreIdentity = "wa-store:second"
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err != nil {
		t.Fatal(err)
	}
	if len(snapshotRelogin(t, st).Messages) != 1 {
		t.Fatal("verified normalized account failed subsequent relogin")
	}
}

func TestLegacyNormalizationRollsBackRevisionProvenanceFailure(t *testing.T) {
	st := reloginStore(t)
	message := reloginMessage(1, "original")
	mergeRelogin(t, st, "first", message)
	message.Text = "edited"
	mergeRelogin(t, st, "first", message)
	before := snapshotRelogin(t, st)
	if _, err := st.DB().Exec(`create trigger fail_revision_identity before update of account_identity on message_revisions begin select raise(abort,'fixture provenance failure'); end`); err != nil {
		t.Fatal(err)
	}
	stats := reloginStats("first")
	stats.LegacyAccountIDs = []string{stats.AccountIdentity}
	stats.AccountIdentity = "wa-account:normalized"
	if err := st.MergeAll(context.Background(), stats, nil, nil, nil, nil, []Message{message}); err == nil {
		t.Fatal("expected injected revision normalization failure")
	}
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
		t.Fatal("revision failure left partially normalized provenance")
	}
}
