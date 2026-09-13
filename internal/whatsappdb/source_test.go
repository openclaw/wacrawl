package whatsappdb

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"
)

func TestDiscoverMessageTimeBounds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		values         []any
		oldest, newest string
	}{
		{"valid", []any{700000000, 700000005}, "2023-03-08T20:26:40Z", "2023-03-08T20:26:45Z"},
		{"mixed", []any{nil, -1, 0, 700000000, 700000005, 1e100}, "2023-03-08T20:26:40Z", "2023-03-08T20:26:45Z"},
		{"invalid only", []any{nil, -1, 0, float64(maxJSONAppleSecondExclusive), 1e100, math.Inf(1)}, "", ""},
		{"empty", nil, "", ""},
		{"upper bound", []any{float64(maxJSONAppleSecondExclusive - 1), float64(maxJSONAppleSecondExclusive)}, "9999-12-31T23:59:59Z", "9999-12-31T23:59:59Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(source, chatDBName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mustExec(t, db, `create table ZWAMESSAGE (ZMESSAGEDATE real);
create table ZWACHATSESSION (Z_PK integer);
create table ZWAMEDIAITEM (Z_PK integer);`)
			for _, value := range tc.values {
				if _, err := db.Exec(`insert into ZWAMESSAGE values (?)`, value); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Discover(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			if got.OldestMessage != tc.oldest || got.NewestMessage != tc.newest {
				t.Fatalf("range = %q..%q, want %q..%q", got.OldestMessage, got.NewestMessage, tc.oldest, tc.newest)
			}
			if !got.MessageRowsKnown || got.MessageRows != len(tc.values) {
				t.Fatalf("row count changed: %+v", got)
			}
		})
	}
}
