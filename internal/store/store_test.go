package store

import (
	"context"
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
	if err := exported.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(exported.Contacts) != 1 || len(exported.Chats) != 1 || len(exported.Groups) != 1 || len(exported.Participants) != 1 || len(exported.Messages) != 2 {
		t.Fatalf("unexpected export: %+v", exported)
	}
	if stats := exported.ImportStats("backup", st.Path(), now); stats.Messages != 2 || stats.MediaMessages != 1 || stats.SourcePath != "backup" {
		t.Fatalf("unexpected export stats: %+v", stats)
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

func TestReplaceAllDuplicateSourcePKFails(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	err = st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, nil,
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
