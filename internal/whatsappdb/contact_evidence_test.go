package whatsappdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/openclaw/wacrawl/internal/store"
)

func TestDesktopRestoreIgnoresDestinationContactEvidence(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createFixtureDBs(t, source)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := Import(ctx, st, source); err != nil {
		t.Fatal(err)
	}
	// Even unreadable destination evidence must not enter exact replacement preflight.
	if _, err := st.DB().Exec(`update contacts set lid='777@lid',lid_evidence='broken' where jid='111@s.whatsapp.net'`); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(source, contactsDBName))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `update ZWAADDRESSBOOKCONTACT set ZLID='901' where ZWHATSAPPID='111@s.whatsapp.net'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithOptions(ctx, st, ImportOptions{SourcePath: source, Restore: true}); err != nil {
		t.Fatal(err)
	}
	data, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Messages) != 4 {
		t.Fatal("native exact restore lost messages")
	}
	for _, c := range data.Contacts {
		if c.JID == "111@s.whatsapp.net" {
			if c.LID != "901" || len(c.LIDEvidence) != 1 || c.LIDEvidence[0].LID != "901" || c.LIDEvidence[0].SourceStoreIdentity != data.SourceStoreIdentity || c.LIDEvidence[0].FirstObservedAt.IsZero() {
				t.Fatalf("destination evidence leaked or native origin lost: %+v", c)
			}
		}
	}
}

func TestDesktopDuplicateContactLIDsDoNotBlockHistory(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			ctx := context.Background()
			source := t.TempDir()
			createFixtureDBs(t, source)
			st, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			if existing {
				if _, err := Import(ctx, st, source); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", filepath.Join(source, contactsDBName))
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `update ZWAADDRESSBOOKCONTACT set ZLID='900' where ZWHATSAPPID='111@s.whatsapp.net';
insert into ZWAADDRESSBOOKCONTACT (ZWHATSAPPID,ZFULLNAME,ZLID) values ('111@s.whatsapp.net','Conflicting','901@lid');`)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Import(ctx, st, source); err != nil {
				t.Fatal(err)
			}
			data, err := st.ExportAll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Messages) != 4 || len(data.Contacts) != 2 {
				t.Fatal("native duplicate lost unrelated data")
			}
			for _, c := range data.Contacts {
				if c.JID == "111@s.whatsapp.net" {
					name := ""
					if existing {
						name = "Bob"
					}
					if c.FullName != name || c.LID != "" || len(c.LIDEvidence) != 2 {
						t.Fatalf("native duplicate display/evidence: %+v", c)
					}
				}
			}
			if _, err := Import(ctx, st, source); err != nil {
				t.Fatal(err)
			}
			repeated, err := st.ExportAll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i := range data.Contacts {
				if !reflect.DeepEqual(data.Contacts[i].LIDEvidence, repeated.Contacts[i].LIDEvidence) {
					t.Fatal("native replay grew evidence")
				}
			}
			if len(data.Messages) != len(repeated.Messages) || len(data.Revisions) != len(repeated.Revisions) {
				t.Fatal("native replay grew history")
			}
		})
	}
}
