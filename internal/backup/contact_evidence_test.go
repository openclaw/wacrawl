package backup

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
)

func TestContactEvidenceEncryptedRoundTripAndStablePublication(t *testing.T) {
	ctx := context.Background()
	st := openFixtureStore(t, "source.db")
	now := time.Unix(1750000000, 0).UTC()
	stats := store.ImportStats{SourceIdentity: "/fixture", SourceStoreIdentity: "wa-store:first", AccountIdentity: "wa-account:owner", FinishedAt: now}
	pn, lid := "100@s.whatsapp.net", "900@lid"
	contacts := []store.Contact{{JID: pn, LID: lid}}
	messages := []store.Message{{SourcePK: 1, ChatJID: pn, SenderJID: pn, MessageID: "one", Timestamp: now, Text: "body"}, {SourcePK: 2, ChatJID: "901@lid", SenderJID: "901@lid", MessageID: "two", Timestamp: now, Text: "body"}}
	if err := st.MergeAll(ctx, stats, contacts, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	contacts[0].LID = "901@lid"
	if err := st.MergeAll(ctx, stats, contacts, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	before, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts := zeroCountOptions(t)
	if _, err := Push(ctx, st, opts); err != nil {
		t.Fatal(err)
	}
	published := zeroCountPublished(t, opts)
	restored := openFixtureStore(t, "restored.db")
	if _, err := Pull(ctx, restored, opts); err != nil {
		t.Fatal(err)
	}
	after, err := restored.ExportAll(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("encrypted contact evidence changed: %v", err)
	}
	for _, jid := range []string{pn, lid, "901@lid"} {
		rows, err := restored.Messages(ctx, store.MessageFilter{ChatJID: jid, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rows {
			if m.ChatJID != jid {
				t.Fatal("backup restore rehabilitated conflicting link")
			}
		}
	}
	for range 2 {
		if err := restored.MergeAll(ctx, stats, contacts, nil, nil, nil, messages); err != nil {
			t.Fatal(err)
		}
		if result, err := Push(ctx, restored, opts); err != nil || result.Changed {
			t.Fatalf("repeat contact evidence created generation: %+v %v", result, err)
		}
		if !reflect.DeepEqual(published, zeroCountPublished(t, opts)) {
			t.Fatal("repeat changed manifest/artifacts/Git HEAD")
		}
	}
}
