package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func assertExactContactFilters(t *testing.T, st *Store, jids ...string) {
	t.Helper()
	for _, jid := range jids {
		for _, filter := range []MessageFilter{{ChatJID: jid, Limit: 100}, {Sender: jid, Limit: 100}} {
			rows, err := st.Messages(context.Background(), filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range rows {
				if filter.ChatJID != "" && m.ChatJID != jid || filter.Sender != "" && m.SenderJID != jid {
					t.Fatal("conflicting filter expanded")
				}
			}
			filter.Query = "body"
			rows, err = st.Search(context.Background(), filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range rows {
				if filter.ChatJID != "" && m.ChatJID != jid || filter.Sender != "" && m.SenderJID != jid {
					t.Fatal("conflicting search expanded")
				}
			}
		}
	}
}

func TestContactEvidenceIsolatesChangesAcrossFutureImports(t *testing.T) {
	ctx := context.Background()
	st := reloginStore(t)
	old := aliasMessage(1, "shared", aliasPN)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: "900", FullName: "Original"}}, old)
	original := snapshotRelogin(t, st).Messages[0]
	incoming := aliasMessage(20, "shared", "901@lid")
	unrelated := aliasMessage(21, "unrelated", "300@s.whatsapp.net")
	changed := []Contact{{JID: aliasPN, LID: "901@lid", FullName: "Current"}}
	mergeContactLogin(t, st, "second", changed, incoming, unrelated)
	data := snapshotRelogin(t, st)
	if len(data.Messages) != 3 || len(data.Contacts[0].LIDEvidence) != 2 || !reflect.DeepEqual(original, data.Messages[0]) {
		t.Fatal("conflict lost evidence/history or blocked unrelated message")
	}
	for _, e := range data.Contacts[0].LIDEvidence {
		if e.SourceStoreIdentity == "" || !e.FirstObservedAt.Equal(reloginStats("first").FinishedAt) {
			t.Fatal("native evidence lacks actual origin")
		}
	}
	assertExactContactFilters(t, st, aliasPN, aliasLID, "901@lid")
	// A later empty, old-only, or removed-contact snapshot cannot forget the conflict.
	for i, c := range []Contact{{JID: aliasPN}, {JID: aliasPN, LID: "900"}, {JID: aliasPN, LID: "900", Tombstone: Tombstone{DeletedAt: reloginStats("third").FinishedAt}}} {
		mergeContactLogin(t, st, "second", []Contact{c}, incoming, unrelated)
		before := snapshotRelogin(t, st)
		mergeContactLogin(t, st, "second", []Contact{c}, incoming, unrelated)
		if after := snapshotRelogin(t, st); !reflect.DeepEqual(before, after) {
			t.Fatalf("repeat %d grew evidence", i)
		}
		assertExactContactFilters(t, st, aliasPN, aliasLID, "901@lid")
	}
	// A removed LID parent must not delete PN history through the quarantined link.
	stats := reloginStats("third")
	if err := st.MergeAll(ctx, stats, changed, []Chat{{JID: "901@lid", Removed: true}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := st.MessageBySourcePK(ctx, 1)
	if err != nil || !saved.DeletedAt.IsZero() {
		t.Fatal("conflicting parent propagated deletion")
	}
	before := snapshotRelogin(t, st)
	path := st.Path()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if got := snapshotRelogin(t, reopened); !reflect.DeepEqual(before, got) {
		t.Fatal("reopen changed retained evidence")
	}
	restored := reloginStore(t)
	if err := restored.ImportSnapshot(ctx, before, "backup:/fixture", stats.FinishedAt); err != nil {
		t.Fatal(err)
	}
	if got := snapshotRelogin(t, restored); !reflect.DeepEqual(before, got) {
		t.Fatal("canonical roundtrip changed evidence")
	}
	assertExactContactFilters(t, restored, aliasPN, aliasLID, "901@lid")
}

func TestSharedLIDConflictSurvivesDeletionOfOtherContact(t *testing.T) {
	st := reloginStore(t)
	other := "200@s.whatsapp.net"
	contacts := []Contact{{JID: aliasPN, LID: aliasLID}, {JID: other, LID: aliasLID}}
	mergeContactLogin(t, st, "first", contacts, aliasMessage(1, "pn", aliasPN), aliasMessage(2, "lid", aliasLID))
	contacts[1].DeletedAt = reloginStats("second").FinishedAt
	contacts[1].LID = ""
	mergeContactLogin(t, st, "second", contacts)
	mergeContactLogin(t, st, "third", contacts[:1], aliasMessage(3, "new", aliasLID))
	assertExactContactFilters(t, st, aliasPN, aliasLID, other)
	if got := snapshotRelogin(t, st); len(got.Messages) != 3 || len(got.Contacts[1].LIDEvidence) != 1 {
		t.Fatal("deleted contact evidence forgotten")
	}
}

func TestNativeDuplicateContactEvidencePolicy(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			var expected SnapshotData
			for _, reverse := range []bool{false, true} {
				st := reloginStore(t)
				if existing {
					mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: aliasLID, FullName: "Retained", Phone: "100"}})
				}
				contacts := []Contact{{JID: aliasPN, LID: "900", FullName: "One"}, {JID: aliasPN, LID: "901@lid", FullName: "Two"}}
				if reverse {
					contacts[0], contacts[1] = contacts[1], contacts[0]
				}
				messages := []Message{aliasMessage(1, "pn", aliasPN), aliasMessage(2, "lid", "901@lid"), aliasMessage(3, "unrelated", "300@s.whatsapp.net")}
				mergeContactLogin(t, st, "second", contacts, messages...)
				data := snapshotRelogin(t, st)
				if len(data.Contacts) != 1 || len(data.Messages) != 3 {
					t.Fatal("duplicate input blocked unrelated data")
				}
				c := data.Contacts[0]
				wantName, wantLID := "", ""
				wantEvidence := 2
				if existing {
					wantName, wantLID = "Retained", aliasLID
					wantEvidence = 3
				}
				if c.FullName != wantName || c.LID != wantLID || len(c.LIDEvidence) != wantEvidence {
					t.Fatalf("duplicate display/evidence policy: %+v", c)
				}
				assertExactContactFilters(t, st, aliasPN, aliasLID, "901@lid")
				mergeContactLogin(t, st, "second", contacts, messages...)
				if got := snapshotRelogin(t, st); !reflect.DeepEqual(data, got) {
					t.Fatal("duplicate replay grew evidence")
				}
				if reverse && !reflect.DeepEqual(expected, data) {
					t.Fatal("duplicate contact result depends on input order")
				}
				expected = data
			}
		})
	}
	st := reloginStore(t)
	equivalent := []Contact{{JID: aliasPN, LID: "900"}, {JID: aliasPN, LID: aliasLID}}
	mergeContactLogin(t, st, "first", equivalent, aliasMessage(1, "pn", aliasPN), aliasMessage(2, "lid", aliasLID))
	c := snapshotRelogin(t, st).Contacts[0]
	if len(c.LIDEvidence) != 2 || c.LIDEvidence[0].LID != "900" || c.LIDEvidence[1].LID != aliasLID {
		t.Fatal("raw bare/full evidence not retained")
	}
	rows, err := st.Messages(context.Background(), MessageFilter{ChatJID: aliasPN, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("equivalent raw representations treated as conflict")
	}
	duplicate := snapshotRelogin(t, st)
	duplicate.Contacts = append(duplicate.Contacts, duplicate.Contacts[0])
	if err := st.ImportSnapshot(context.Background(), duplicate, "backup:/fixture", time.Now()); err == nil || !strings.Contains(err.Error(), "duplicate contact") {
		t.Fatal("duplicate canonical keys accepted")
	}
}

func TestContactEvidenceMigrationFromThreeAndFour(t *testing.T) {
	for _, version := range []int{3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			st := reloginStore(t)
			mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: "900"}, {JID: "200@s.whatsapp.net", LID: aliasLID}}, aliasMessage(1, "old", aliasPN))
			before := snapshotRelogin(t, st)
			if _, err := st.DB().Exec(fmt.Sprintf(`alter table contacts drop column lid_evidence; pragma user_version=%d`, version)); err != nil {
				t.Fatal(err)
			}
			if err := st.migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			migrated := snapshotRelogin(t, st)
			if !reflect.DeepEqual(before.Messages, migrated.Messages) {
				t.Fatal("migration changed old event")
			}
			for _, c := range migrated.Contacts {
				if len(c.LIDEvidence) != 1 || c.LIDEvidence[0].SourceStoreIdentity != "" || !c.LIDEvidence[0].FirstObservedAt.IsZero() || c.LIDEvidence[0].LID != c.LID {
					t.Fatal("migration fabricated provenance")
				}
			}
			if err := st.migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := snapshotRelogin(t, st); !reflect.DeepEqual(migrated, got) {
				t.Fatal("migration repeated evidence")
			}
			mergeContactLogin(t, st, "second", []Contact{{JID: aliasPN, LID: "901@lid"}}, aliasMessage(2, "new", "901@lid"))
			assertExactContactFilters(t, st, aliasPN, aliasLID, "901@lid")
			if len(snapshotRelogin(t, st).Contacts[0].LIDEvidence) != 2 {
				t.Fatal("overwrite lost migrated source evidence")
			}
		})
	}
}

func TestContactEvidenceRollbackAndCorruption(t *testing.T) {
	st := reloginStore(t)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: aliasLID}}, aliasMessage(1, "old", aliasPN))
	before := snapshotRelogin(t, st)
	if _, err := st.DB().Exec(`create trigger fail_contact_evidence before update on contacts begin select raise(abort,'fixture evidence failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err := st.MergeAll(context.Background(), reloginStats("second"), []Contact{{JID: aliasPN, LID: "901@lid"}}, nil, nil, nil, []Message{aliasMessage(2, "new", "901@lid")}); err == nil {
		t.Fatal("expected rollback")
	}
	if got := snapshotRelogin(t, st); !reflect.DeepEqual(before, got) {
		t.Fatal("failed evidence write changed history")
	}
	if _, err := st.DB().Exec(`drop trigger fail_contact_evidence`); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{}`, `[{"lid":""}]`, `[{"lid":"900"},{"lid":"900"}]`, `[{"lid":"900","source_store_identity":"bad"}]`} {
		if _, err := st.DB().Exec(`update contacts set lid_evidence=?`, payload); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Messages(context.Background(), MessageFilter{ChatJID: aliasPN}); err == nil {
			t.Fatal("corrupt evidence authorized filter")
		}
		if _, err := st.ExportAll(context.Background()); err == nil {
			t.Fatal("corrupt evidence exported")
		}
	}
}

func TestContactEvidenceSnapshotValidationAndLegacyRestore(t *testing.T) {
	st := reloginStore(t)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: "900"}})
	legacy := SnapshotData{Contacts: []Contact{{JID: aliasPN, LID: "901@lid"}}}
	if err := st.ImportSnapshot(context.Background(), legacy, "backup:/fixture", time.Now()); err != nil {
		t.Fatal(err)
	}
	c := snapshotRelogin(t, st).Contacts[0]
	if len(c.LIDEvidence) != 1 || c.LIDEvidence[0].LID != "901@lid" || !c.LIDEvidence[0].FirstObservedAt.IsZero() || c.LIDEvidence[0].SourceStoreIdentity != "" {
		t.Fatal("restore inherited destination or invented provenance")
	}
	for _, evidence := range [][]ContactLIDEvidence{{{}}, {{LID: "900"}, {LID: "900"}}, {{LID: "900", FirstObservedAt: time.Unix(-1, 0)}}, {{LID: "900", SourceStoreIdentity: "bad"}}} {
		legacy.Contacts[0].LIDEvidence = evidence
		if err := st.ImportSnapshot(context.Background(), legacy, "backup:/fixture", time.Now()); err == nil {
			t.Fatal("invalid snapshot evidence accepted")
		}
	}
	// Encoding older Contact types ignores the field: schema4 backup clients must upgrade.
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var legacyContact struct{ JID, LID string }
	if err := json.Unmarshal(encoded, &legacyContact); err != nil {
		t.Fatal(err)
	}
	oldEncoded, err := json.Marshal(legacyContact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(oldEncoded), "lid_evidence") {
		t.Fatal("legacy compatibility assumption changed")
	}
}

func TestContactEvidenceMigrationRollsBackSchemaAndRows(t *testing.T) {
	st := reloginStore(t)
	mergeContactLogin(t, st, "first", []Contact{{JID: aliasPN, LID: aliasLID}})
	if _, err := st.DB().Exec(`alter table contacts drop column lid_evidence; pragma user_version=4;
create trigger fail_legacy_contact before update on contacts begin select raise(abort,'fixture backfill failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err := st.migrate(context.Background()); err == nil {
		t.Fatal("expected backfill failure")
	}
	var version, columns int
	if err := st.DB().QueryRow(`pragma user_version`).Scan(&version); err != nil || version != 4 {
		t.Fatal("migration advanced version after rollback")
	}
	if err := st.DB().QueryRow(`select count(*) from pragma_table_info('contacts') where name='lid_evidence'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("migration retained partial schema")
	}
}

func TestContactConflictDoesNotReassignKnownSourceMapping(t *testing.T) {
	st := reloginStore(t)
	contacts := []Contact{{JID: aliasPN, LID: aliasLID}}
	mergeContactLogin(t, st, "first", contacts, aliasMessage(1, "same", aliasPN))
	incoming := aliasMessage(2, "same", aliasLID)
	mergeContactLogin(t, st, "second", contacts, incoming)
	before := snapshotRelogin(t, st)
	contacts[0].LID = "901@lid"
	mergeContactLogin(t, st, "second", contacts, incoming)
	after := snapshotRelogin(t, st)
	if !reflect.DeepEqual(before.Messages, after.Messages) || !reflect.DeepEqual(before.Sources, after.Sources) || !reflect.DeepEqual(before.Observations, after.Observations) {
		t.Fatal("new conflict split/reassigned established event")
	}
	assertExactContactFilters(t, st, aliasPN, aliasLID, "901@lid")
}
