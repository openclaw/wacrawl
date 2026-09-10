package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

const (
	aliasPN  = "100@s.whatsapp.net"
	aliasLID = "900@lid"
)

func mergeContactLogin(t *testing.T, st *Store, source string, contacts []Contact, messages ...Message) {
	t.Helper()
	stats := reloginStats(source)
	if err := st.ValidateImport(context.Background(), stats, messages, false, contacts...); err != nil {
		t.Fatal(err)
	}
	if err := st.MergeAll(context.Background(), stats, contacts, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
}

func aliasMessage(pk int64, id, jid string) Message {
	m := reloginMessage(pk, id)
	m.ChatJID = jid
	m.SenderJID = jid
	return m
}

func TestContactAliasesUnifyReloginsFiltersAndPreserveRawOrigins(t *testing.T) {
	ctx := context.Background()
	st := reloginStore(t)
	contacts := []Contact{{JID: aliasPN, LID: aliasLID}}
	original := aliasMessage(1, "shared", aliasPN)
	oldOnly := aliasMessage(2, "old", aliasPN)
	mergeContactLogin(t, st, "first", contacts, original, oldOnly)
	incoming := aliasMessage(40, "shared", aliasLID)
	newOnly := aliasMessage(1, "new", aliasLID)
	mergeContactLogin(t, st, "second", contacts, incoming, newOnly)
	data := snapshotRelogin(t, st)
	if len(data.Messages) != 3 || len(data.Revisions) != 0 || len(data.Sources) != 4 {
		t.Fatalf("alias overlap not unified: messages %d revisions %d sources %d", len(data.Messages), len(data.Revisions), len(data.Sources))
	}
	saved, err := st.MessageBySourcePK(ctx, 1)
	if err != nil || saved.EventID != "wa:1" || saved.ChatJID != aliasPN || saved.SenderJID != aliasPN {
		t.Fatalf("canonical origin changed: %+v %v", saved, err)
	}
	rawFound := false
	for _, observation := range data.Observations {
		if observation.SourceStoreIdentity == "wa-store:second" && observation.EventID == "wa:1" {
			var payload struct {
				Message Message `json:"message"`
			}
			if err := json.Unmarshal([]byte(observation.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Message.ChatJID != aliasLID || payload.Message.SenderJID != aliasLID || observation.SourceRowPK != 40 || observation.MatchKind != "unique-contact" {
				t.Fatal("raw incoming identity was replaced by canonical identity")
			}
			rawFound = true
		}
	}
	if !rawFound {
		t.Fatal("missing raw alias source observation")
	}
	mergeContactLogin(t, st, "second", contacts, incoming, newOnly)
	if after := snapshotRelogin(t, st); !reflect.DeepEqual(data, after) {
		t.Fatal("alias repeat grew or revised archive")
	}
	for _, jid := range []string{aliasPN, aliasLID} {
		for _, filter := range []MessageFilter{{ChatJID: jid, Limit: 20}, {Sender: jid, Limit: 20}, {ChatJID: jid, Sender: jid, Limit: 20}} {
			messages, err := st.Messages(ctx, filter)
			if err != nil || len(messages) != 3 {
				t.Fatalf("alias history incomplete for %+v: %d %v", filter, len(messages), err)
			}
			filter.Query = "body"
			messages, err = st.Search(ctx, filter)
			if err != nil || len(messages) != 3 {
				t.Fatalf("alias search incomplete for %+v: %d %v", filter, len(messages), err)
			}
		}
	}
	incoming.SourcePK = 100
	newOnly.SourcePK = 101
	mergeContactLogin(t, st, "third", contacts, incoming, newOnly)
	if got := snapshotRelogin(t, st); len(got.Messages) != 3 || len(got.Revisions) != 0 {
		t.Fatal("third relogin lost alias bindings")
	}
	// Canonical backup data retains the contact links and original observations.
	exported := snapshotRelogin(t, st)
	restored := reloginStore(t)
	if err := restored.ImportSnapshot(ctx, exported, "backup:/fixture", reloginStats("third").FinishedAt); err != nil {
		t.Fatal(err)
	}
	if got := snapshotRelogin(t, restored); !reflect.DeepEqual(exported, got) {
		t.Fatal("restore changed alias provenance")
	}
	messages, err := restored.Messages(ctx, MessageFilter{ChatJID: aliasLID, Sender: aliasPN, Limit: 20})
	if err != nil || len(messages) != 3 {
		t.Fatal("restored alias filters lost history")
	}
	mergeContactLogin(t, restored, "third", contacts, incoming, newOnly)
	if got := snapshotRelogin(t, restored); !reflect.DeepEqual(exported, got) {
		t.Fatal("restored alias repeat changed history")
	}
}

func TestConflictingAndMissingContactLinksStayExact(t *testing.T) {
	for name, contacts := range map[string][]Contact{
		"missing":            nil,
		"phone-or-name-only": {{JID: aliasPN, Phone: aliasLID, FullName: aliasLID}},
		"many-pn-one-lid":    {{JID: aliasPN, LID: aliasLID}, {JID: "200@s.whatsapp.net", LID: aliasLID}},
	} {
		t.Run(name, func(t *testing.T) {
			st := reloginStore(t)
			old := aliasMessage(1, "shared", aliasPN)
			mergeContactLogin(t, st, "first", nil, old)
			incoming := aliasMessage(2, "shared", aliasLID)
			mergeContactLogin(t, st, "second", contacts, incoming)
			before := snapshotRelogin(t, st)
			for _, expected := range contacts {
				for _, saved := range before.Contacts {
					if expected.JID == saved.JID && expected.LID != saved.LID {
						t.Fatal("source contact LID was altered")
					}
				}
			}
			if len(before.Messages) != 2 || len(before.Revisions) != 0 {
				t.Fatal("uncertain contact links merged events")
			}
			for _, jid := range []string{aliasPN, aliasLID} {
				rows, err := st.Messages(context.Background(), MessageFilter{ChatJID: jid, Sender: jid, Limit: 20})
				if err != nil || len(rows) != 1 || rows[0].ChatJID != jid || rows[0].SenderJID != jid {
					t.Fatalf("ambiguous filter expanded %q: %d %v", jid, len(rows), err)
				}
				rows, err = st.Search(context.Background(), MessageFilter{ChatJID: jid, Sender: jid, Query: "body", Limit: 20})
				if err != nil || len(rows) != 1 {
					t.Fatal("ambiguous search filter expanded")
				}
			}
			mergeContactLogin(t, st, "second", contacts, incoming)
			if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
				t.Fatal("ambiguous repeat changed history")
			}
		})
	}
}

func TestContactLinkContradictingRetainedContactStaysSeparate(t *testing.T) {
	st := reloginStore(t)
	old := aliasMessage(1, "shared", aliasPN)
	mergeContactLogin(t, st, "first", []Contact{{JID: "200@s.whatsapp.net", LID: aliasLID}}, old)
	incoming := aliasMessage(2, "shared", aliasLID)
	mergeContactLogin(t, st, "second", []Contact{{JID: aliasPN, LID: aliasLID}}, incoming)
	if len(snapshotRelogin(t, st).Messages) != 2 {
		t.Fatal("input link conflicting with retained contacts merged messages")
	}
	rows, err := st.Messages(context.Background(), MessageFilter{ChatJID: aliasLID, Limit: 20})
	if err != nil || len(rows) != 1 {
		t.Fatal("contradictory retained link expanded filters")
	}
}

func TestNormalizedCandidateMustStillBeUnique(t *testing.T) {
	st := reloginStore(t)
	// Before authoritative links appear, both raw identities are separate events.
	mergeContactLogin(t, st, "first", nil, aliasMessage(1, "same", aliasPN), aliasMessage(2, "same", aliasLID))
	mergeContactLogin(t, st, "second", []Contact{{JID: aliasPN, LID: aliasLID}}, aliasMessage(30, "same", aliasLID))
	if len(snapshotRelogin(t, st).Messages) != 3 {
		t.Fatal("ambiguous normalized identity forcibly merged")
	}
}

func TestContactAliasLinksAreSymmetricWithoutTransitiveGuessing(t *testing.T) {
	aliases := verifiedContactAliases([]Contact{{JID: aliasPN, LID: aliasLID}, {JID: aliasPN, LID: aliasLID}, {JID: "group@g.us", LID: "1000@lid"}, {JID: aliasLID, LID: "2000@lid"}})
	if len(aliases) != 2 || aliases[aliasPN] != aliasLID || aliases[aliasLID] != aliasPN {
		t.Fatalf("unexpected verified links: %v", aliases)
	}
}

func TestBareAuthoritativeContactLIDUnifiesWithoutChangingRawContact(t *testing.T) {
	st := reloginStore(t)
	contacts := []Contact{{JID: aliasPN, LID: "900"}}
	mergeContactLogin(t, st, "first", contacts, aliasMessage(1, "shared", aliasPN), aliasMessage(2, "old", aliasPN))
	incoming := []Message{aliasMessage(10, "shared", aliasLID), aliasMessage(11, "new", aliasLID)}
	mergeContactLogin(t, st, "second", contacts, incoming...)
	before := snapshotRelogin(t, st)
	if len(before.Messages) != 3 || len(before.Revisions) != 0 || before.Contacts[0].LID != "900" {
		t.Fatal("bare contact alias failed or raw value changed")
	}
	for _, jid := range []string{aliasPN, aliasLID} {
		rows, err := st.Messages(context.Background(), MessageFilter{ChatJID: jid, Sender: jid, Limit: 20})
		if err != nil || len(rows) != 3 {
			t.Fatalf("bare contact filter failed: %d %v", len(rows), err)
		}
	}
	mergeContactLogin(t, st, "second", contacts, incoming...)
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
		t.Fatal("bare contact repeat changed snapshot")
	}
	// Different raw encodings of the same authoritative link are not conflicts.
	full := []Contact{{JID: aliasPN, LID: aliasLID}}
	mergeContactLogin(t, st, "third", full, incoming...)
	if len(snapshotRelogin(t, st).Messages) != 3 {
		t.Fatal("equivalent bare/full contact rejected or duplicated")
	}
}

func TestEmptyContactUpdateRetainsEvidenceButCannotAuthorizeMatching(t *testing.T) {
	st := reloginStore(t)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: "900"}}, aliasMessage(1, "shared", aliasPN))
	empty := []Contact{{JID: aliasPN}}
	mergeContactLogin(t, st, "second", empty, aliasMessage(2, "shared", aliasLID))
	before := snapshotRelogin(t, st)
	if len(before.Messages) != 2 || before.Contacts[0].LID != "900" {
		t.Fatal("empty source link erased evidence or authorized alias matching")
	}
	changed := []Contact{{JID: aliasPN, LID: "901@lid"}}
	mergeContactLogin(t, st, "third", changed)
	if got := snapshotRelogin(t, st); len(got.Contacts[0].LIDEvidence) != 2 {
		t.Fatal("empty intermediate update lost earlier evidence")
	}
	deleted := empty
	deleted[0].DeletedAt = reloginStats("third").FinishedAt
	mergeContactLogin(t, st, "third", deleted)
	rows, err := st.Messages(context.Background(), MessageFilter{ChatJID: aliasLID, Limit: 20})
	if err != nil || len(rows) != 1 || rows[0].ChatJID != aliasLID {
		t.Fatal("deleted contact still authorized alias filtering")
	}
}

func TestAliasChatDeletionCoversArchivedOnlyAndLaterLifecycle(t *testing.T) {
	ctx := context.Background()
	st := reloginStore(t)
	contacts := []Contact{{JID: aliasPN, LID: "900"}}
	old := aliasMessage(1, "shared", aliasPN)
	absent := aliasMessage(2, "archived-only", aliasPN)
	stats := reloginStats("first")
	if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: aliasPN, Kind: "dm", LastMessageAt: old.Timestamp}}, nil, nil, []Message{old, absent}); err != nil {
		t.Fatal(err)
	}
	incoming := aliasMessage(50, "shared", aliasLID)
	stats = reloginStats("second")
	stats.FinishedAt = stats.FinishedAt.Add(time.Minute)
	if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: aliasLID, Kind: "dm", Removed: true}}, nil, nil, []Message{incoming}); err != nil {
		t.Fatal(err)
	}
	deleted := snapshotRelogin(t, st)
	if len(deleted.Messages) != 2 || len(deleted.Revisions) != 2 {
		t.Fatal("alias parent deletion lost history/revisions")
	}
	for _, m := range deleted.Messages {
		if m.DeletedAt.IsZero() || m.DeletionReason != "parent_chat_deleted" || m.ChatJID != aliasPN {
			t.Fatal("alias parent missed observed or archive-only message")
		}
	}
	for _, jid := range []string{aliasPN, aliasLID} {
		rows, err := st.Messages(ctx, MessageFilter{ChatJID: jid, Limit: 20})
		if err != nil || len(rows) != 0 {
			t.Fatal("deleted alias chat still exposes messages")
		}
	}
	stats.FinishedAt = stats.FinishedAt.Add(time.Minute)
	if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: aliasLID, Kind: "dm", Removed: true}}, nil, nil, []Message{incoming}); err != nil {
		t.Fatal(err)
	}
	repeated := snapshotRelogin(t, st)
	if len(repeated.Revisions) != len(deleted.Revisions) || len(repeated.Observations) != len(deleted.Observations) {
		t.Fatal("reobserving removed alias manufactured history")
	}
	stats = reloginStats("third")
	stats.FinishedAt = stats.FinishedAt.Add(3 * time.Minute)
	fresh := aliasMessage(1, "new-lifecycle", aliasLID)
	fresh.Timestamp = stats.FinishedAt
	if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: aliasLID, Kind: "dm", LastMessageAt: fresh.Timestamp}}, nil, nil, []Message{fresh}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Messages(ctx, MessageFilter{ChatJID: aliasPN, Limit: 20})
	if err != nil || len(rows) != 1 || rows[0].MessageID != "new-lifecycle" {
		t.Fatal("alias parent revival failed or resurrected sticky tombstones")
	}
}

func TestAliasReopeningIgnoresStaleSiblingParentTombstone(t *testing.T) {
	for removed, reopened := range map[string]string{aliasPN: aliasLID, aliasLID: aliasPN} {
		t.Run(removed, func(t *testing.T) {
			ctx := context.Background()
			st := reloginStore(t)
			contacts := []Contact{{JID: aliasPN, LID: "900"}}
			old := aliasMessage(1, "original", removed)
			stats := reloginStats("first")
			if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: removed, Kind: "dm", LastMessageAt: old.Timestamp}}, nil, nil, []Message{old}); err != nil {
				t.Fatal(err)
			}
			stats.FinishedAt = stats.FinishedAt.Add(time.Minute)
			if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: removed, Kind: "dm", Removed: true}}, nil, nil, []Message{old}); err != nil {
				t.Fatal(err)
			}
			stats = reloginStats("second")
			stats.FinishedAt = stats.FinishedAt.Add(2 * time.Minute)
			fresh := aliasMessage(1, "later", reopened)
			fresh.Timestamp = stats.FinishedAt
			reappeared := aliasMessage(2, "original", reopened)
			if err := st.MergeAll(ctx, stats, contacts, []Chat{{JID: reopened, Kind: "dm", LastMessageAt: fresh.Timestamp}}, nil, nil, []Message{fresh, reappeared}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{removed, reopened} {
				rows, err := st.Messages(ctx, MessageFilter{ChatJID: id, Limit: 20})
				if err != nil || len(rows) != 1 || rows[0].MessageID != "later" {
					t.Fatalf("stale %s parent blocked reopened %s: %d %v", removed, id, len(rows), err)
				}
			}
			before := snapshotRelogin(t, st)
			for _, m := range before.Messages {
				if m.EventID == "wa:1" && m.DeletedAt.IsZero() {
					t.Fatal("reopening resurrected original")
				}
			}
			if len(before.Messages) != 2 || len(before.Revisions) != 1 {
				t.Fatal("reopening duplicated/lost original history")
			}
			// Later imports may contain messages without a parent row in that slice.
			stats.FinishedAt = stats.FinishedAt.Add(time.Minute)
			if err := st.MergeAll(ctx, stats, contacts, nil, nil, nil, []Message{fresh, reappeared}); err != nil {
				t.Fatal(err)
			}
			after := snapshotRelogin(t, st)
			if len(after.Messages) != len(before.Messages) || len(after.Revisions) != len(before.Revisions) || len(after.Observations) != len(before.Observations) {
				t.Fatal("reopened repeat grew history")
			}
			live, err := st.Messages(ctx, MessageFilter{ChatJID: reopened, Limit: 20})
			if err != nil || len(live) != 1 {
				t.Fatal("stored alias parent re-deleted later message")
			}
		})
	}
}

func TestAliasParentDeletionUsesRetainedContactEvidence(t *testing.T) {
	st := reloginStore(t)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: "900"}}, aliasMessage(1, "archived-only", aliasPN))
	stats := reloginStats("second")
	if err := st.MergeAll(context.Background(), stats, []Contact{{JID: aliasPN}}, []Chat{{JID: aliasLID, Kind: "dm", Removed: true}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotRelogin(t, st)
	if snapshot.Contacts[0].LID != "900" || len(snapshot.Messages) != 1 || snapshot.Messages[0].DeletedAt.IsZero() {
		t.Fatal("empty source link blocked parent deletion through retained evidence")
	}
}
