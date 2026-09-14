package store

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaVersion = 5

func (s *Store) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, "pragma user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this wacrawl build supports (%d)", current, schemaVersion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	for _, table := range []string{"contacts", "chats", "groups", "group_participants", "messages"} {
		for _, column := range []struct{ name, definition string }{
			{"deleted_at", "integer"},
			{"deletion_source", "text"},
			{"deletion_reason", "text"},
			{"last_seen_at", "integer not null default 0"},
		} {
			if err := ensureColumn(ctx, tx, table, column.name, column.definition); err != nil {
				return err
			}
		}
	}
	if err := ensureColumn(ctx, tx, "messages", "event_id", "text"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "messages", "source_row_pk", "integer not null default 0"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update messages set source_row_pk = source_pk where source_row_pk = 0`); err != nil {
		return fmt.Errorf("backfill message source-row provenance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `update messages set event_id = printf('wa:%lld', source_pk) where event_id is null or trim(event_id) = ''`); err != nil {
		return fmt.Errorf("backfill message event identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `create unique index if not exists idx_messages_event_id on messages(event_id)`); err != nil {
		return fmt.Errorf("index message event identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
create trigger if not exists messages_event_id_required_insert
before insert on messages when new.event_id is null or trim(new.event_id) = ''
begin select raise(abort, 'messages.event_id is required'); end;
create trigger if not exists messages_event_id_required_update
before update of event_id on messages when new.event_id is null or trim(new.event_id) = ''
begin select raise(abort, 'messages.event_id is required'); end;`); err != nil {
		return fmt.Errorf("enforce message event identity: %w", err)
	}
	for _, table := range []string{"contacts", "chats", "groups", "group_participants", "messages"} {
		statement := fmt.Sprintf(`update %s set last_seen_at = coalesce((select max(updated_at) from sync_state), 0) where last_seen_at = 0`, table) // #nosec G201 -- table is from the fixed list above.
		if _, err := tx.ExecContext(ctx, statement); err != nil {                                                                                    //nolint:gosec // table is from the fixed list above.
			return fmt.Errorf("backfill %s last_seen_at: %w", table, err)
		}
	}
	// Completed migrations must not block exact restore on destination evidence.
	if current < 5 {
		if err := migrateContactEvidence(ctx, tx); err != nil {
			return err
		}
	}
	if err := migrateSources(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return tx.Commit()
}

func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, definition string) error {
	rows, err := tx.QueryContext(ctx, "pragma table_info("+table+")") //nolint:gosec // fixed migration identifiers only.
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "alter table "+table+" add column "+column+" "+definition); err != nil { //nolint:gosec // fixed migration identifiers only.
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}
