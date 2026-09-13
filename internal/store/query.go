package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/openclaw/wacrawl/internal/store/storedb"
)

const (
	messageSelectColumns = `source_pk, source_row_pk, event_id, chat_jid, coalesce(chat_name,'') as chat_name, msg_id, coalesce(sender_jid,'') as sender_jid, coalesce(sender_name,'') as sender_name, ts, from_me, coalesce(text,'') as text, raw_type, coalesce(message_type,'') as message_type, coalesce(media_type,'') as media_type, coalesce(media_title,'') as media_title, coalesce(media_path,'') as media_path, coalesce(media_url,'') as media_url, coalesce(media_size,0) as media_size, starred, coalesce(deleted_at,0) as deleted_at, coalesce(deletion_source,'') as deletion_source, coalesce(deletion_reason,'') as deletion_reason, last_seen_at, '' as snippet`
	messageScanColumns   = `source_pk, source_row_pk, event_id, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, deleted_at, deletion_source, deletion_reason, last_seen_at, snippet`
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{DBPath: s.path}
	var err error
	if out.Chats, err = countInt(ctx, s.q.CountChats); err != nil {
		return out, err
	}
	if out.UnreadChats, err = countInt(ctx, s.q.CountUnreadChats); err != nil {
		return out, err
	}
	if out.UnreadMessages, err = countInt(ctx, s.q.CountUnreadMessages); err != nil {
		return out, err
	}
	if out.Contacts, err = countInt(ctx, s.q.CountContacts); err != nil {
		return out, err
	}
	if out.Groups, err = countInt(ctx, s.q.CountGroups); err != nil {
		return out, err
	}
	if out.Participants, err = countInt(ctx, s.q.CountParticipants); err != nil {
		return out, err
	}
	if out.Messages, err = countInt(ctx, s.q.CountMessages); err != nil {
		return out, err
	}
	if out.MediaMessages, err = countInt(ctx, s.q.CountMediaMessages); err != nil {
		return out, err
	}
	for query, destination := range map[string]*int{
		`select count(*) from chats where deleted_at is not null`:              &out.DeletedChats,
		`select count(*) from contacts where deleted_at is not null`:           &out.DeletedContacts,
		`select count(*) from groups where deleted_at is not null`:             &out.DeletedGroups,
		`select count(*) from group_participants where deleted_at is not null`: &out.DeletedParticipants,
		`select count(*) from messages where deleted_at is not null`:           &out.DeletedMessages,
		`select count(*) from message_revisions`:                               &out.MessageRevisions,
	} {
		if err := s.db.QueryRowContext(ctx, query).Scan(destination); err != nil {
			return out, err
		}
	}
	bounds, err := s.q.GetMessageTimeBounds(ctx)
	if err != nil {
		return out, err
	}
	out.OldestMessage = fromUnix(bounds.OldestTs)
	out.NewestMessage = fromUnix(bounds.NewestTs)
	var newestObserved int64
	if err := s.db.QueryRowContext(ctx, `select cast(coalesce(max(case when ts > 0 and ts <= 253402300799 then ts end),0) as integer) from messages`).Scan(&newestObserved); err != nil {
		return out, err
	}
	out.NewestObserved = fromUnix(newestObserved)
	lastImport, _ := s.q.GetSyncState(ctx, "last_import_at")
	if t, err := time.Parse(time.RFC3339Nano, lastImport); err == nil {
		out.LastImportAt = t
	}
	if value, err := s.q.GetSyncState(ctx, "source_snapshot_at"); err == nil {
		out.LastSourceSnapshot, _ = time.Parse(time.RFC3339Nano, value)
	}
	out.LastSource, _ = s.q.GetSyncState(ctx, "source_path")
	out.SourceRoot, _ = s.q.GetSyncState(ctx, "merge_source_path")
	if out.SourceRoot == "" {
		out.SourceRoot = out.LastSource
	}
	if value, err := s.q.GetSyncState(ctx, "source_messages"); err == nil {
		if out.LastSourceMessages, err = strconv.Atoi(value); err == nil {
			out.SourceMessagesKnown = true
		}
	}
	if value, err := s.q.GetSyncState(ctx, "source_contacts"); err == nil {
		if out.LastSourceContacts, err = strconv.Atoi(value); err == nil {
			out.SourceContactsKnown = true
		}
	}
	if value, err := s.q.GetSyncState(ctx, "source_newest_message"); err == nil {
		out.LastSourceNewest, _ = time.Parse(time.RFC3339Nano, value)
	}
	return out, nil
}

func (s *Store) ListChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit})
}

func (s *Store) ListUnreadChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit, OnlyUnread: true})
}

func (s *Store) listChats(ctx context.Context, filter ChatFilter) ([]Chat, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.OnlyUnread {
		rows, err := s.q.ListUnreadChats(ctx, int64(filter.Limit))
		if err != nil {
			return nil, err
		}
		out := make([]Chat, 0, len(rows))
		for _, row := range rows {
			out = append(out, chatFromRow(storedb.ListChatsRow(row)))
		}
		return out, nil
	}
	rows, err := s.q.ListChats(ctx, int64(filter.Limit))
	if err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(rows))
	for _, row := range rows {
		out = append(out, chatFromRow(row))
	}
	return out, nil
}

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query, args := messageListQuery(filter)
	return scanMessages(ctx, s.db, query, args...)
}

// MessageBySourcePK returns the single message stored under the given source
// primary key, or sql.ErrNoRows when it does not exist.
func (s *Store) MessageBySourcePK(ctx context.Context, sourcePK int64) (Message, error) {
	messages, err := scanMessages(ctx, s.db, "select "+messageSelectColumns+" from messages where source_pk = ? and deleted_at is null", sourcePK)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, sql.ErrNoRows
	}
	return messages[0], nil
}

func messageListQuery(filter MessageFilter) (string, []any) {
	validQuery, validArgs := filteredMessagesQuery(filter, "")
	validQuery += " and " + validUnixPredicate("ts")
	if filter.After != nil || filter.Before != nil {
		if filter.Asc {
			validQuery += " order by ts asc, source_pk asc limit ?"
		} else {
			validQuery += " order by ts desc, source_pk desc limit ?"
		}
		return validQuery, append(validArgs, filter.Limit)
	}

	if filter.Asc {
		validQuery, validArgs = filteredMessagesQuery(filter, ", 1 as sort_bucket, ts as sort_ts")
		validQuery += " and " + validUnixPredicate("ts")
		invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 0 as sort_bucket, 0 as sort_ts")
		invalidQuery += " and " + invalidUnixPredicate("ts")
		query := "select " + messageScanColumns + " from (select * from (" + invalidQuery + " order by source_pk asc limit ?) union all select * from (" + validQuery + " order by ts asc, source_pk asc limit ?)) order by sort_bucket asc, sort_ts asc, source_pk asc limit ?"
		args := append([]any{}, invalidArgs...)
		args = append(args, filter.Limit)
		args = append(args, validArgs...)
		args = append(args, filter.Limit, filter.Limit)
		return query, args
	}

	validQuery, validArgs = filteredMessagesQuery(filter, ", 0 as sort_bucket, ts as sort_ts")
	validQuery += " and " + validUnixPredicate("ts")
	invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 1 as sort_bucket, 0 as sort_ts")
	invalidQuery += " and " + invalidUnixPredicate("ts")
	query := "select " + messageScanColumns + " from (select * from (" + validQuery + " order by ts desc, source_pk desc limit ?) union all select * from (" + invalidQuery + " order by source_pk desc limit ?)) order by sort_bucket asc, sort_ts desc, source_pk desc limit ?"
	args := append([]any{}, validArgs...)
	args = append(args, filter.Limit)
	args = append(args, invalidArgs...)
	args = append(args, filter.Limit, filter.Limit)
	return query, args
}

func filteredMessagesQuery(filter MessageFilter, extraColumns string) (string, []any) {
	query := "select " + messageSelectColumns + extraColumns + " from messages where 1=1"
	return applyMessageFilters(query, nil, filter, false)
}

func (s *Store) Search(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, errors.New("search query required")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return nil, err
	}
	snippetStart := filter.SnippetStart
	if snippetStart == "" {
		snippetStart = "["
	}
	snippetEnd := filter.SnippetEnd
	if snippetEnd == "" {
		snippetEnd = "]"
	}
	query := `select m.source_pk, m.source_row_pk, m.event_id, m.chat_jid, coalesce(m.chat_name,''), m.msg_id, coalesce(m.sender_jid,''), coalesce(m.sender_name,''), m.ts, m.from_me, coalesce(m.text,''), m.raw_type, coalesce(m.message_type,''), coalesce(m.media_type,''), coalesce(m.media_title,''), coalesce(m.media_path,''), coalesce(m.media_url,''), coalesce(m.media_size,0), m.starred, coalesce(m.deleted_at,0), coalesce(m.deletion_source,''), coalesce(m.deletion_reason,''), m.last_seen_at, snippet(messages_fts, 0, ?, ?, '...', 12) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{snippetStart, snippetEnd, ftsQuery}
	query, args = applyMessageFilters(query, args, filter, true)
	query += " order by bm25(messages_fts) limit ?"
	args = append(args, filter.Limit)
	return scanMessages(ctx, s.db, query, args...)
}

func applyMessageFilters(query string, args []any, filter MessageFilter, joined bool) (string, []any) {
	prefix := ""
	if joined {
		prefix = "m."
	}
	if !filter.IncludeDeleted {
		query += " and " + prefix + "deleted_at is null"
	}
	if strings.TrimSpace(filter.ChatJID) != "" {
		query += " and " + prefix + "chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if strings.TrimSpace(filter.Sender) != "" {
		query += " and " + prefix + "sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.After != nil {
		query += " and " + prefix + "ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		if filter.BeforePK > 0 {
			query += " and (" + prefix + "ts < ? or (" + prefix + "ts = ? and " + prefix + "source_pk < ?))"
			args = append(args, unix(*filter.Before), unix(*filter.Before), filter.BeforePK)
		} else {
			query += " and " + prefix + "ts <= ?"
			args = append(args, unix(*filter.Before))
		}
	}
	if filter.FromMe != nil {
		query += " and " + prefix + "from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and (" + prefix + "media_type <> '' or " + prefix + "media_path <> '' or " + prefix + "media_url <> '')"
	}
	return query, args
}

func scanMessages(ctx context.Context, db storedb.DBTX, query string, args ...any) ([]Message, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type messageScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row messageScanner) (Message, error) {
	var m Message
	var ts, deletedAt, lastSeenAt int64
	var fromMe, starred int
	if err := row.Scan(&m.SourcePK, &m.SourceRowPK, &m.EventID, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName,
		&ts, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL,
		&m.MediaSize, &starred, &deletedAt, &m.DeletionSource, &m.DeletionReason, &lastSeenAt, &m.Snippet); err != nil {
		return Message{}, err
	}
	m.Timestamp = fromUnix(ts)
	m.storedUnix = ts
	m.FromMe = fromMe != 0
	m.Starred = starred != 0
	m.DeletedAt = fromUnix(deletedAt)
	m.LastSeenAt = fromUnix(lastSeenAt)
	return m, nil
}

func messageBySourcePK(ctx context.Context, tx *sql.Tx, sourcePK int64) (Message, bool, error) {
	row := tx.QueryRowContext(ctx, "select "+messageSelectColumns+" from messages where source_pk = ?", sourcePK)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	return m, err == nil, err
}

func countInt(ctx context.Context, count func(context.Context) (int64, error)) (int, error) {
	v, err := count(ctx)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func chatFromRow(row storedb.ListChatsRow) Chat {
	return Chat{
		JID:            row.Jid,
		Kind:           row.Kind,
		Name:           row.Name,
		LastMessageAt:  fromUnix(row.LastMessageAt),
		UnreadCount:    int(row.UnreadCount),
		Archived:       row.Archived != 0,
		Removed:        row.Removed != 0,
		Hidden:         row.Hidden != 0,
		RawSessionType: int(row.RawSessionType),
		MessageCount:   int(row.MessageCount),
	}
}
