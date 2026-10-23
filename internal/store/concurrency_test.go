package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Separate wacrawl processes (parallel read commands, a scheduled sync) each
// open their own connection to one archive. Their write transactions must wait
// for each other instead of failing with SQLITE_BUSY.
func TestConcurrentProcessesShareArchive(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	chat := Chat{JID: "alice@s.whatsapp.net", Kind: "dm", Name: "Alice", LastMessageAt: now}
	messages := []Message{{SourcePK: 1, ChatJID: chat.JID, ChatName: chat.Name, MessageID: "a", SenderJID: chat.JID, Timestamp: now, Text: "hello", MessageType: "text"}}
	stats := func() ImportStats {
		return ImportStats{SourcePath: "/fixture", SourceIdentity: "fixture-store", AccountIdentity: "fixture-account", Chats: 1, Messages: 1, FinishedAt: time.Now().UTC()}
	}
	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.MergeAll(ctx, stats(), nil, []Chat{chat}, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	for round := range 3 {
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for worker := range workers {
			wg.Go(func() {
				st, err := Open(ctx, path)
				if err != nil {
					errs <- err
					return
				}
				defer func() { _ = st.Close() }()
				if worker%2 == 0 {
					errs <- st.MergeAll(ctx, stats(), nil, []Chat{chat}, nil, nil, messages)
					return
				}
				_, err = st.Search(ctx, MessageFilter{Query: "hello", Limit: 1})
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("round %d: concurrent archive access: %v", round, err)
			}
		}
	}
}
