package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/wacrawl/internal/sqlitedsn"
	"github.com/openclaw/wacrawl/internal/store"

	_ "modernc.org/sqlite"
)

const readOnlySelectError = "only read-only select statements are allowed"

type sqlQueryResult struct {
	columns []string
	rows    []map[string]any
}

func (r sqlQueryResult) MarshalJSON() ([]byte, error) {
	if r.rows == nil {
		return json.Marshal([]map[string]any{})
	}
	return json.Marshal(r.rows)
}

func (a *app) runSQL(ctx context.Context, args []string) error {
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "sql")
		return nil
	}
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return usageErr(errors.New("sql query required"))
	}
	if err := validateReadOnlySQL(query); err != nil {
		return err
	}
	if a.syncMode != archiveSyncNever {
		if err := a.withStore(ctx, func(st *store.Store) error {
			return a.syncArchive(ctx, st)
		}); err != nil {
			return err
		}
	}
	result, err := queryReadOnlySQL(ctx, a.dbPath, query)
	if err != nil {
		return err
	}
	return a.print(result)
}

func queryReadOnlySQL(ctx context.Context, dbPath string, query string) (sqlQueryResult, error) {
	if strings.TrimSpace(dbPath) == "" {
		return sqlQueryResult{}, errors.New("db path is required")
	}
	if err := validateReadOnlySQL(query); err != nil {
		return sqlQueryResult{}, err
	}
	dsn := sqlitedsn.File(
		dbPath,
		sqlitedsn.P("mode", "ro"),
		sqlitedsn.P("_pragma", "query_only(1)"),
		sqlitedsn.P("_pragma", "busy_timeout(5000)"),
		sqlitedsn.P("_pragma", "temp_store(MEMORY)"),
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return sqlQueryResult{}, fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return sqlQueryResult{}, fmt.Errorf("ping sqlite: %w", err)
	}
	rows, err := db.QueryContext(ctx, query) // #nosec G201 -- sql executes user-provided SELECT text against a local read-only archive DB.
	if err != nil {
		return sqlQueryResult{}, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return sqlQueryResult{}, err
	}
	columns = uniqueSQLColumnNames(columns)
	result := sqlQueryResult{
		columns: columns,
		rows:    make([]map[string]any, 0),
	}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return sqlQueryResult{}, err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			row[col] = normalizeSQLValue(values[i])
		}
		result.rows = append(result.rows, row)
	}
	if err := rows.Err(); err != nil {
		return sqlQueryResult{}, err
	}
	return result, nil
}

func uniqueSQLColumnNames(columns []string) []string {
	unique := make([]string, len(columns))
	used := make(map[string]struct{}, len(columns))
	for i, column := range columns {
		base := column
		name := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[name]; !exists {
				break
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		unique[i] = name
		used[name] = struct{}{}
	}
	return unique
}

func validateReadOnlySQL(query string) error {
	trimmed := stripSQLLeadingComments(strings.TrimSpace(query))
	if !readOnlySQLStartsWithSelect(trimmed) {
		return errors.New(readOnlySelectError)
	}
	if hasAdditionalSQLStatement(trimmed) {
		return errors.New("only a single read-only select statement is allowed")
	}
	return nil
}

func readOnlySQLStartsWithSelect(query string) bool {
	switch {
	case startsWithSQLKeyword(query, "select"):
		return true
	case startsWithSQLKeyword(query, "with"):
		return withClauseEndsInSelect(query)
	default:
		return false
	}
}

func withClauseEndsInSelect(query string) bool {
	for i, depth := len("with"), 0; i < len(query); i++ {
		switch query[i] {
		case '\'':
			i = scanSQLQuoted(query, i, '\'')
		case '"':
			i = scanSQLQuoted(query, i, '"')
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = scanSQLLineComment(query, i+2)
			}
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = scanSQLBlockComment(query, i+2)
			}
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth != 0 || !isSQLIdentStart(query[i]) {
				continue
			}
			start := i
			for i+1 < len(query) && isSQLIdentChar(query[i+1]) {
				i++
			}
			switch strings.ToLower(query[start : i+1]) {
			case "select":
				return true
			case "insert", "update", "delete", "replace", "create", "drop", "alter", "pragma", "vacuum", "attach", "detach", "reindex", "analyze", "explain":
				return false
			}
		}
	}
	return false
}

func startsWithSQLKeyword(query, keyword string) bool {
	if len(query) < len(keyword) {
		return false
	}
	if !strings.EqualFold(query[:len(keyword)], keyword) {
		return false
	}
	return len(query) == len(keyword) || !isSQLIdentChar(query[len(keyword)])
}

func hasAdditionalSQLStatement(query string) bool {
	for i := 0; i < len(query); i++ {
		switch query[i] {
		case '\'':
			i = scanSQLQuoted(query, i, '\'')
		case '"':
			i = scanSQLQuoted(query, i, '"')
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = scanSQLLineComment(query, i+2)
			}
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = scanSQLBlockComment(query, i+2)
			}
		case ';':
			return strings.TrimSpace(stripSQLLeadingComments(query[i+1:])) != ""
		}
	}
	return false
}

func scanSQLQuoted(query string, start int, quote byte) int {
	for i := start + 1; i < len(query); i++ {
		if query[i] != quote {
			continue
		}
		if i+1 < len(query) && query[i+1] == quote {
			i++
			continue
		}
		return i
	}
	return len(query) - 1
}

func scanSQLLineComment(query string, start int) int {
	for i := start; i < len(query); i++ {
		if query[i] == '\n' || query[i] == '\r' {
			return i
		}
	}
	return len(query) - 1
}

func scanSQLBlockComment(query string, start int) int {
	for i := start; i+1 < len(query); i++ {
		if query[i] == '*' && query[i+1] == '/' {
			return i + 1
		}
	}
	return len(query) - 1
}

func stripSQLLeadingComments(query string) string {
	for {
		query = strings.TrimSpace(query)
		switch {
		case strings.HasPrefix(query, "--"):
			end := strings.IndexAny(query, "\r\n")
			if end < 0 {
				return ""
			}
			query = query[end+1:]
		case strings.HasPrefix(query, "/*"):
			end := strings.Index(query[2:], "*/")
			if end < 0 {
				return ""
			}
			query = query[end+4:]
		default:
			return query
		}
	}
}

func isSQLIdentChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSQLIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	default:
		return typed
	}
}

func formatSQLValue(value any) string {
	if value == nil {
		return "NULL"
	}
	text := fmt.Sprint(value)
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}
