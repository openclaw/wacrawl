package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store/storedb"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Store struct {
	db   *sql.DB
	q    *storedb.Queries
	path string
}

type ImportStats struct {
	SourcePath    string    `json:"source_path"`
	DBPath        string    `json:"db_path"`
	Chats         int       `json:"chats"`
	Contacts      int       `json:"contacts"`
	Groups        int       `json:"groups"`
	Participants  int       `json:"participants"`
	Messages      int       `json:"messages"`
	MediaMessages int       `json:"media_messages"`
	MediaCopied   int       `json:"media_copied,omitempty"`
	MediaMissing  int       `json:"media_missing,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
}

type Status struct {
	DBPath         string    `json:"db_path"`
	Chats          int       `json:"chats"`
	UnreadChats    int       `json:"unread_chats"`
	UnreadMessages int       `json:"unread_messages"`
	Contacts       int       `json:"contacts"`
	Groups         int       `json:"groups"`
	Participants   int       `json:"participants"`
	Messages       int       `json:"messages"`
	MediaMessages  int       `json:"media_messages"`
	OldestMessage  time.Time `json:"oldest_message,omitzero"`
	NewestMessage  time.Time `json:"newest_message,omitzero"`
	LastImportAt   time.Time `json:"last_import_at,omitzero"`
	LastSource     string    `json:"last_source,omitempty"`
}

type Chat struct {
	JID            string
	Kind           string
	Name           string
	LastMessageAt  time.Time
	UnreadCount    int
	Archived       bool
	Removed        bool
	Hidden         bool
	RawSessionType int
	MessageCount   int
}

type ChatFilter struct {
	Limit      int
	OnlyUnread bool
}

type Contact struct {
	JID          string
	Phone        string
	FullName     string
	FirstName    string
	LastName     string
	BusinessName string
	Username     string
	LID          string
	AboutText    string
	UpdatedAt    time.Time
}

type Group struct {
	JID       string
	Name      string
	OwnerJID  string
	CreatedAt time.Time
}

type GroupParticipant struct {
	GroupJID    string
	UserJID     string
	ContactName string
	FirstName   string
	IsAdmin     bool
	IsActive    bool
}

type Message struct {
	SourcePK    int64     `json:"source_pk"`
	ChatJID     string    `json:"chat_jid"`
	ChatName    string    `json:"chat_name,omitempty"`
	MessageID   string    `json:"message_id"`
	SenderJID   string    `json:"sender_jid,omitempty"`
	SenderName  string    `json:"sender_name,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	FromMe      bool      `json:"from_me"`
	Text        string    `json:"text,omitempty"`
	RawType     int       `json:"raw_type"`
	MessageType string    `json:"message_type,omitempty"`
	MediaType   string    `json:"media_type,omitempty"`
	MediaTitle  string    `json:"media_title,omitempty"`
	MediaPath   string    `json:"media_path,omitempty"`
	MediaURL    string    `json:"media_url,omitempty"`
	MediaSize   int64     `json:"media_size,omitempty"`
	Starred     bool      `json:"starred,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
}

type MessageFilter struct {
	Query    string
	ChatJID  string
	Sender   string
	Limit    int
	After    *time.Time
	Before   *time.Time
	FromMe   *bool
	HasMedia bool
	Asc      bool
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
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

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (s *Store) ReplaceAll(ctx context.Context, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)

	for _, deleteQuery := range []func(context.Context) error{
		q.DeleteMessagesFTS,
		q.DeleteMessages,
		q.DeleteGroupParticipants,
		q.DeleteGroups,
		q.DeleteChats,
		q.DeleteContacts,
		q.DeleteSyncState,
	} {
		if err := deleteQuery(ctx); err != nil {
			return err
		}
	}
	for _, c := range contacts {
		err := q.InsertContact(ctx, storedb.InsertContactParams{
			Jid:          c.JID,
			Phone:        nullString(c.Phone),
			FullName:     nullString(c.FullName),
			FirstName:    nullString(c.FirstName),
			LastName:     nullString(c.LastName),
			BusinessName: nullString(c.BusinessName),
			Username:     nullString(c.Username),
			Lid:          nullString(c.LID),
			AboutText:    nullString(c.AboutText),
			UpdatedAt:    nullInt64(unix(c.UpdatedAt)),
		})
		if err != nil {
			return err
		}
	}
	for _, c := range chats {
		err := q.InsertChat(ctx, storedb.InsertChatParams{
			Jid:            c.JID,
			Kind:           c.Kind,
			Name:           nullString(c.Name),
			LastMessageAt:  nullInt64(unix(c.LastMessageAt)),
			UnreadCount:    int64(c.UnreadCount),
			Archived:       int64(boolInt(c.Archived)),
			Removed:        int64(boolInt(c.Removed)),
			Hidden:         int64(boolInt(c.Hidden)),
			RawSessionType: int64(c.RawSessionType),
		})
		if err != nil {
			return err
		}
	}
	for _, g := range groups {
		err := q.InsertGroup(ctx, storedb.InsertGroupParams{
			Jid:       g.JID,
			Name:      nullString(g.Name),
			OwnerJid:  nullString(g.OwnerJID),
			CreatedAt: nullInt64(unix(g.CreatedAt)),
		})
		if err != nil {
			return err
		}
	}
	for _, p := range participants {
		err := q.InsertParticipant(ctx, storedb.InsertParticipantParams{
			GroupJid:    p.GroupJID,
			UserJid:     p.UserJID,
			ContactName: nullString(p.ContactName),
			FirstName:   nullString(p.FirstName),
			IsAdmin:     int64(boolInt(p.IsAdmin)),
			IsActive:    int64(boolInt(p.IsActive)),
		})
		if err != nil {
			return err
		}
	}
	for _, m := range messages {
		err := q.InsertMessage(ctx, storedb.InsertMessageParams{
			SourcePk:    m.SourcePK,
			ChatJid:     m.ChatJID,
			ChatName:    nullString(m.ChatName),
			MsgID:       m.MessageID,
			SenderJid:   nullString(m.SenderJID),
			SenderName:  nullString(m.SenderName),
			Ts:          unix(m.Timestamp),
			FromMe:      int64(boolInt(m.FromMe)),
			Text:        nullString(m.Text),
			RawType:     int64(m.RawType),
			MessageType: nullString(m.MessageType),
			MediaType:   nullString(m.MediaType),
			MediaTitle:  nullString(m.MediaTitle),
			MediaPath:   nullString(m.MediaPath),
			MediaUrl:    nullString(m.MediaURL),
			MediaSize:   nullInt64(m.MediaSize),
			Starred:     int64(boolInt(m.Starred)),
		})
		if err != nil {
			return err
		}
		err = q.InsertMessageFTS(ctx, storedb.InsertMessageFTSParams{
			SourcePk: m.SourcePK,
			Text:     nullString(strings.TrimSpace(m.Text + " " + m.MediaTitle)),
			Chat:     nullString(m.ChatName),
			Sender:   nullString(m.SenderName),
			Media:    nullString(m.MediaType),
		})
		if err != nil {
			return err
		}
	}
	now := stats.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for key, value := range map[string]string{
		"last_import_at": now.Format(time.RFC3339Nano),
		"source_path":    stats.SourcePath,
	} {
		err := q.InsertSyncState(ctx, storedb.InsertSyncStateParams{
			Key:       key,
			Value:     value,
			UpdatedAt: unix(now),
		})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

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
	bounds, err := s.q.GetMessageTimeBounds(ctx)
	if err != nil {
		return out, err
	}
	if bounds.OldestTs > 0 {
		out.OldestMessage = time.Unix(bounds.OldestTs, 0).UTC()
	}
	if bounds.NewestTs > 0 {
		out.NewestMessage = time.Unix(bounds.NewestTs, 0).UTC()
	}
	lastImport, _ := s.q.GetSyncState(ctx, "last_import_at")
	if t, err := time.Parse(time.RFC3339Nano, lastImport); err == nil {
		out.LastImportAt = t
	}
	out.LastSource, _ = s.q.GetSyncState(ctx, "source_path")
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
			out = append(out, unreadChatFromRow(row))
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
	query := `select source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, '' from messages where 1=1`
	var args []any
	query, args = applyMessageFilters(query, args, filter, false)
	if filter.Asc {
		query += " order by ts asc, source_pk asc limit ?"
	} else {
		query += " order by ts desc, source_pk desc limit ?"
	}
	args = append(args, filter.Limit)
	return scanMessages(ctx, s.db, query, args...)
}

func (s *Store) Search(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, errors.New("search query required")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query := `select m.source_pk, m.chat_jid, m.chat_name, m.msg_id, m.sender_jid, m.sender_name, m.ts, m.from_me, m.text, m.raw_type, m.message_type, m.media_type, m.media_title, m.media_path, m.media_url, m.media_size, m.starred, snippet(messages_fts, 0, '[', ']', '...', 12) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{filter.Query}
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
		query += " and " + prefix + "ts <= ?"
		args = append(args, unix(*filter.Before))
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

func scanMessages(ctx context.Context, db *sql.DB, query string, args ...any) ([]Message, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		var fromMe, starred int
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &starred, &m.Snippet); err != nil {
			return nil, err
		}
		m.Timestamp = fromUnix(ts)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func fromUnix(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	t := time.Unix(v, 0).UTC()
	// A persisted timestamp can fall outside JSON's marshalable year range if it
	// was imported before appleNullTime clamped impossible Apple-epoch sentinels
	// (e.g. the 0@status pseudo-chat, stored as ~year 11001). Treat such values as
	// unknown so reads of an already-populated archive don't break --json output.
	if y := t.Year(); y < 1 || y > 9999 {
		return time.Time{}
	}
	return t
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: true}
}

func nullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
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

func unreadChatFromRow(row storedb.ListUnreadChatsRow) Chat {
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

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
