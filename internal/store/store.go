package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/wacrawl/internal/sqlitedsn"
	"github.com/openclaw/wacrawl/internal/store/storedb"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	q    *storedb.Queries
	path string
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	dsn, err := sqlitedsn.File(
		path,
		sqlitedsn.P("_pragma", "foreign_keys(1)"),
		sqlitedsn.P("_pragma", "journal_mode(WAL)"),
		sqlitedsn.P("_pragma", "synchronous(NORMAL)"),
		sqlitedsn.P("_pragma", "busy_timeout(5000)"),
	)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db, q: storedb.New(db), path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Path() string {
	return s.path
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
