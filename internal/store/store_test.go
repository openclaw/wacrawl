package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreReplaceStatusListSearch(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	contacts := []Contact{{JID: "alice@s.whatsapp.net", FullName: "Alice", UpdatedAt: now}}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now, UnreadCount: 2, MessageCount: 2}}
	groups := []Group{{JID: "chat@g.us", Name: "Chat", OwnerJID: "owner@s.whatsapp.net", CreatedAt: now.Add(-time.Hour)}}
	participants := []GroupParticipant{{GroupJID: "chat@g.us", UserJID: "alice@s.whatsapp.net", ContactName: "Alice", IsAdmin: true, IsActive: true}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "a", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now.Add(-time.Minute), Text: "hello launch", RawType: 0, MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "b", SenderJID: "me", SenderName: "me", Timestamp: now, FromMe: true, Text: "photo", RawType: 1, MessageType: "image", MediaType: "image", MediaTitle: "launch image", MediaPath: "/tmp/image.jpg", MediaSize: 123},
	}
	if err := st.ReplaceAll(ctx, stats, contacts, chats, groups, participants, messages); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 || status.MediaMessages != 1 || status.UnreadChats != 1 || status.UnreadMessages != 2 || status.LastSource != "/tmp/source" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if st.DB() == nil {
		t.Fatal("DB should be available")
	}

	listed, err := st.Messages(ctx, MessageFilter{ChatJID: "chat@g.us", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].MessageID != "a" || listed[1].MessageID != "b" {
		t.Fatalf("unexpected messages: %+v", listed)
	}

	onlyMine := true
	filtered, err := st.Messages(ctx, MessageFilter{FromMe: &onlyMine, HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "b" {
		t.Fatalf("unexpected filtered messages: %+v", filtered)
	}

	results, err := st.Search(ctx, MessageFilter{Query: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 search results, got %d", len(results))
	}
	if _, err := st.Search(ctx, MessageFilter{}); err == nil {
		t.Fatal("expected empty search query error")
	}

	after := now.Add(-2 * time.Minute)
	before := now.Add(time.Minute)
	results, err = st.Messages(ctx, MessageFilter{After: &after, Before: &before, Sender: "alice@s.whatsapp.net", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "a" {
		t.Fatalf("unexpected ranged sender results: %+v", results)
	}

	chatsOut, err := st.ListChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chatsOut) != 1 || chatsOut[0].JID != "chat@g.us" {
		t.Fatalf("unexpected chats: %+v", chatsOut)
	}
	unreadChats, err := st.ListUnreadChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadChats) != 1 || unreadChats[0].UnreadCount != 2 {
		t.Fatalf("unexpected unread chats: %+v", unreadChats)
	}

	exported, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contactsOut, err := st.Contacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(contactsOut) != 1 || contactsOut[0].JID != "alice@s.whatsapp.net" {
		t.Fatalf("unexpected contacts: %+v", contactsOut)
	}
	if err := exported.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(exported.Contacts) != 1 || len(exported.Chats) != 1 || len(exported.Groups) != 1 || len(exported.Participants) != 1 || len(exported.Messages) != 2 {
		t.Fatalf("unexpected export: %+v", exported)
	}
	if stats := exported.ImportStats("backup", st.Path(), now); stats.Messages != 2 || stats.MediaMessages != 1 || stats.SourcePath != "backup" {
		t.Fatalf("unexpected export stats: %+v", stats)
	}
	if stats := exported.ImportStats("backup", st.Path(), time.Time{}); stats.FinishedAt.IsZero() || stats.StartedAt.IsZero() {
		t.Fatalf("zero finished time was not defaulted: %+v", stats)
	}
	restored, err := Open(ctx, filepath.Join(t.TempDir(), "restored.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.ImportSnapshot(ctx, exported, "backup", now); err != nil {
		t.Fatal(err)
	}
	restoredStatus, err := restored.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restoredStatus.Messages != 2 || restoredStatus.LastSource != "backup" {
		t.Fatalf("unexpected restored status: %+v", restoredStatus)
	}
	if err := (SnapshotData{Messages: []Message{{SourcePK: 1}, {SourcePK: 1}}}).Validate(); err == nil {
		t.Fatal("expected duplicate source_pk validation error")
	}
	if err := (SnapshotData{Messages: []Message{{}}}).Validate(); err == nil {
		t.Fatal("expected empty source_pk validation error")
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Open(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected opening directory as db to fail")
	}
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if unix(time.Time{}) != 0 {
		t.Fatal("zero time unix should be zero")
	}
	if !fromUnix(0).IsZero() {
		t.Fatal("zero unix should be zero time")
	}
}

func TestFromUnixJSONBounds(t *testing.T) {
	got := fromUnix(maxJSONUnixSecond)
	want := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if !got.Equal(want) || got.Location().String() != "UTC" {
		t.Fatalf("fromUnix(max) = %s (%s), want %s UTC", got, got.Location(), want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("max JSON-safe timestamp should marshal: %v", err)
	}
	if got := fromUnix(maxJSONUnixSecond + 1); !got.IsZero() {
		t.Fatalf("out-of-range unix should clamp to zero, got %v", got)
	}
	if got := fromUnix(-1); !got.IsZero() {
		t.Fatalf("negative unix should clamp to zero, got %v", got)
	}
}

func TestReplaceAllDuplicateSourcePKFails(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	err = st.ReplaceAll(
		ctx, ImportStats{FinishedAt: now}, nil,
		[]Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "chat", MessageID: "a", Timestamp: now, RawType: 0},
			{SourcePK: 1, ChatJID: "chat", MessageID: "b", Timestamp: now, RawType: 0},
		},
	)
	if err == nil {
		t.Fatal("expected duplicate source_pk error")
	}
	status, statusErr := st.Status(ctx)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Messages != 0 {
		t.Fatalf("failed replace should roll back, got %+v", status)
	}
}

func TestImportSnapshotRefreshesFTS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	base := SnapshotData{
		Chats: []Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		Messages: []Message{{
			SourcePK:  1,
			ChatJID:   "chat",
			ChatName:  "Chat",
			MessageID: "a",
			Timestamp: now,
			Text:      "old import text",
			RawType:   0,
		}},
	}
	if err := st.ImportSnapshot(ctx, base, "first", now); err != nil {
		t.Fatal(err)
	}
	results, err := st.Search(ctx, MessageFilter{Query: "old", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected old FTS result, got %d", len(results))
	}

	updated := base
	updated.Messages[0].Text = "new import text"
	updated.Messages[0].MediaTitle = "fresh media title"
	if err := st.ImportSnapshot(ctx, updated, "second", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	results, err = st.Search(ctx, MessageFilter{Query: "old", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected old FTS text to be removed, got %+v", results)
	}
	results, err = st.Search(ctx, MessageFilter{Query: "fresh", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "a" {
		t.Fatalf("expected updated media title FTS result, got %+v", results)
	}
}

func TestListChatsClampsOutOfRangePersistedTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values
	('0@status', 'status', 'Status', ?, 1, 0, 0, 0, 0),
	('valid@s.whatsapp.net', 'dm', 'Valid', ?, 1, 0, 0, 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		list func() ([]Chat, error)
	}{
		{"ListChats", func() ([]Chat, error) { return st.ListChats(ctx, 10) }},
		{"ListUnreadChats", func() ([]Chat, error) { return st.ListUnreadChats(ctx, 10) }},
	} {
		got, err := tc.list()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: want 2 chats, got %d", tc.name, len(got))
		}
		if got[0].JID != "valid@s.whatsapp.net" || !got[0].LastMessageAt.Equal(valid) {
			t.Fatalf("%s: valid chat should sort before clamped poison, got %+v", tc.name, got)
		}
		if got[1].JID != "0@status" || !got[1].LastMessageAt.IsZero() {
			t.Fatalf("%s: poisoned chat should clamp to zero and sort oldest, got %+v", tc.name, got)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("%s: JSON marshal of already-populated archive failed: %v", tc.name, err)
		}
	}
}

func TestMessagesClampsOutOfRangePersistedTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values('c@s.whatsapp.net', 'dm', 'C', ?, 0, 0, 0, 0, 0)
`, valid.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
insert into messages(source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred)
values
	(1, 'c@s.whatsapp.net', 'C', 'poison', '', '', ?, 0, 'poison', 0, 'text', '', '', '', '', 0, 0),
	(2, 'c@s.whatsapp.net', 'C', 'valid', '', '', ?, 0, 'valid', 0, 'text', '', '', '', '', 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	desc, err := st.Messages(ctx, MessageFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 2 || desc[0].MessageID != "valid" || desc[1].MessageID != "poison" || !desc[1].Timestamp.IsZero() {
		t.Fatalf("poisoned message should clamp to zero and sort oldest in desc order: %+v", desc)
	}
	if _, err := json.Marshal(desc); err != nil {
		t.Fatalf("messages JSON marshal failed on poisoned messages.ts: %v", err)
	}

	asc, err := st.Messages(ctx, MessageFilter{Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(asc) != 2 || asc[0].MessageID != "poison" || asc[1].MessageID != "valid" {
		t.Fatalf("poisoned message should sort as oldest in asc order: %+v", asc)
	}

	after := valid.Add(-time.Hour)
	filtered, err := st.Messages(ctx, MessageFilter{After: &after, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "valid" {
		t.Fatalf("date filters should exclude unknown poisoned timestamps, got %+v", filtered)
	}
}

func TestStatusClampsOutOfRangeMessageTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values('c@s.whatsapp.net', 'dm', 'C', ?, 0, 0, 0, 0, 0)
`, valid.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
insert into messages(source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred)
values
	(1, 'c@s.whatsapp.net', 'C', 'poison', '', '', ?, 0, 'poison', 0, 'text', '', '', '', '', 0, 0),
	(2, 'c@s.whatsapp.net', 'C', 'valid', '', '', ?, 0, 'valid', 0, 'text', '', '', '', '', 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.OldestMessage.Equal(valid) || !status.NewestMessage.Equal(valid) {
		t.Fatalf("status bounds should ignore poisoned messages.ts and keep valid bounds: %+v", status)
	}
	if _, err := json.Marshal(status); err != nil {
		t.Fatalf("status JSON marshal failed on poisoned messages.ts: %v", err)
	}

	if _, err := st.DB().ExecContext(ctx, `delete from messages where source_pk = 2`); err != nil {
		t.Fatal(err)
	}
	status, err = st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.OldestMessage.IsZero() || !status.NewestMessage.IsZero() {
		t.Fatalf("all-invalid status bounds should clamp to zero: %+v", status)
	}
}
