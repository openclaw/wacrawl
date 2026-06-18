package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/wacrawl/internal/store"
)

func TestValidateReadOnlySQLAllowsCommentsQuotesAndCTEs(t *testing.T) {
	for _, query := range []string{
		"-- leading comment\nSELECT 1",
		"/* leading comment */ SELECT ';' AS literal",
		"SELECT 'it''s;fine' AS quoted; -- trailing comment",
		`SELECT "semi;colon" AS quoted; /* trailing block */`,
		"WITH rows AS (SELECT 1 AS n) SELECT n FROM rows",
	} {
		t.Run(query, func(t *testing.T) {
			if err := validateReadOnlySQL(query); err != nil {
				t.Fatalf("validateReadOnlySQL() error = %v", err)
			}
		})
	}
}

func TestValidateReadOnlySQLRejectsWritesAndAdditionalStatements(t *testing.T) {
	for _, query := range []string{
		"",
		"DELETE FROM messages",
		"SELECTED 1",
		"INSERT INTO messages(text) VALUES ('nope')",
		"WITH rows AS (SELECT 1) DELETE FROM messages RETURNING 1",
	} {
		t.Run(query, func(t *testing.T) {
			if err := validateReadOnlySQL(query); err == nil || !strings.Contains(err.Error(), readOnlySelectError) {
				t.Fatalf("validateReadOnlySQL() error = %v, want read-only select error", err)
			}
		})
	}
	for _, query := range []string{
		"SELECT 1; SELECT 2",
		"SELECT 1; /* comment */ SELECT 2",
	} {
		t.Run(query, func(t *testing.T) {
			if err := validateReadOnlySQL(query); err == nil || !strings.Contains(err.Error(), "single read-only select") {
				t.Fatalf("validateReadOnlySQL() error = %v, want single statement error", err)
			}
		})
	}
}

func TestQueryReadOnlySQLNormalizesValuesAndEmptyResults(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := queryReadOnlySQL(ctx, dbPath, "SELECT x'6869' AS blob_value, NULL AS missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.rows) != 1 || result.rows[0]["blob_value"] != "hi" || result.rows[0]["missing"] != nil {
		t.Fatalf("rows = %#v", result.rows)
	}

	empty, err := queryReadOnlySQL(ctx, dbPath, "SELECT source_pk FROM messages WHERE 0")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.rows) != 0 || len(empty.columns) != 1 || empty.columns[0] != "source_pk" {
		t.Fatalf("empty result = %#v", empty)
	}
}

func TestSQLResultJSONAndFormatting(t *testing.T) {
	data, err := json.Marshal(sqlQueryResult{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Fatalf("json = %s, want []", data)
	}
	if got := formatSQLValue(nil); got != "NULL" {
		t.Fatalf("nil format = %q", got)
	}
	if got := formatSQLValue("a\tb\rc\nd"); got != "a b c d" {
		t.Fatalf("escaped format = %q", got)
	}
}
