package whatsappdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/cache"
	"github.com/openclaw/wacrawl/internal/mediafile"
	"github.com/openclaw/wacrawl/internal/sqlitedsn"
	_ "modernc.org/sqlite"
)

const (
	chatDBName          = "ChatStorage.sqlite"
	contactsDBName      = "ContactsV2.sqlite"
	axolotlDBName       = "Axolotl.sqlite"
	axolotlBeforeDBName = "Axolotl.before.sqlite"
)

type Source struct {
	Path             string   `json:"path"`
	Available        bool     `json:"available"`
	ChatDB           string   `json:"chat_db,omitempty"`
	ContactsDB       string   `json:"contacts_db,omitempty"`
	MediaDir         string   `json:"media_dir,omitempty"`
	SupportingDBs    []string `json:"supporting_dbs,omitempty"`
	MessageRows      int      `json:"message_rows,omitempty"`
	MessageRowsKnown bool     `json:"-"`
	ChatRows         int      `json:"chat_rows,omitempty"`
	ContactRows      int      `json:"contact_rows,omitempty"`
	ContactRowsKnown bool     `json:"-"`
	MediaRows        int      `json:"media_rows,omitempty"`
	OldestMessage    string   `json:"oldest_message,omitempty"`
	NewestMessage    string   `json:"newest_message,omitempty"`
	SchemaNotes      []string `json:"schema_notes,omitempty"`
}

type Snapshot struct {
	Root       string
	SourcePath string
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Group Containers", "group.net.whatsapp.WhatsApp.shared")
	}
	return ""
}

func Discover(ctx context.Context, path string) (Source, error) {
	path = defaultedPath(path)
	source := Source{Path: path}
	if path == "" {
		return source, errors.New("WhatsApp desktop path is only auto-detected on macOS")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return source, nil
		}
		return source, err
	}
	if !info.IsDir() {
		return source, fmt.Errorf("desktop path is not a directory: %s", path)
	}
	source.Available = true
	source.ChatDB = filepath.Join(path, chatDBName)
	source.ContactsDB = filepath.Join(path, contactsDBName)
	source.MediaDir = filepath.Join(path, "Message", "Media")
	for _, rel := range []string{
		"Axolotl.sqlite",
		"LID.sqlite",
		"LocalKeyValue.sqlite",
		"BackedUpKeyValue.sqlite",
		filepath.Join("fts", "ChatSearchV5f.sqlite"),
	} {
		full := filepath.Join(path, rel)
		if _, err := os.Stat(full); err == nil {
			source.SupportingDBs = append(source.SupportingDBs, rel)
		}
	}
	chatDB, closeChat, err := openReadOnly(source.ChatDB)
	if err == nil {
		defer closeChat()
		if err := chatDB.QueryRowContext(ctx, "select count(*) from ZWAMESSAGE").Scan(&source.MessageRows); err == nil {
			source.MessageRowsKnown = true
		}
		_ = chatDB.QueryRowContext(ctx, "select count(*) from ZWACHATSESSION").Scan(&source.ChatRows)
		_ = chatDB.QueryRowContext(ctx, "select count(*) from ZWAMEDIAITEM").Scan(&source.MediaRows)
		var minDate, maxDate sql.NullFloat64
		_ = chatDB.QueryRowContext(ctx, "select min(ZMESSAGEDATE), max(ZMESSAGEDATE) from ZWAMESSAGE").Scan(&minDate, &maxDate)
		if minDate.Valid {
			source.OldestMessage = appleTime(minDate.Float64).Format(time.RFC3339)
		}
		if maxDate.Valid {
			source.NewestMessage = appleTime(maxDate.Float64).Format(time.RFC3339)
		}
		source.SchemaNotes = append(
			source.SchemaNotes,
			"CoreData tables: ZWACHATSESSION, ZWAMESSAGE, ZWAMEDIAITEM, ZWAGROUPINFO, ZWAGROUPMEMBER",
			"timestamps are seconds since 2001-01-01 UTC",
			"ZWAMESSAGE.ZGROUPMEMBER identifies group senders",
			"ZWAMEDIAITEM joins both via ZWAMESSAGE.ZMEDIAITEM and ZWAMEDIAITEM.ZMESSAGE",
		)
	}
	contactsDB, closeContacts, err := openReadOnly(source.ContactsDB)
	if err == nil {
		defer closeContacts()
		if err := contactsDB.QueryRowContext(ctx, "select count(*) from ZWAADDRESSBOOKCONTACT").Scan(&source.ContactRows); err == nil {
			source.ContactRowsKnown = true
		}
	}
	return source, nil
}

func SnapshotPath(path string) (Snapshot, error) {
	path = defaultedPath(path)
	if path == "" {
		return Snapshot{}, errors.New("desktop path is required")
	}
	root, err := os.MkdirTemp("", "wacrawl-desktop-*")
	if err != nil {
		return Snapshot{}, err
	}
	for _, file := range []struct {
		source   string
		name     string
		optional bool
	}{
		{axolotlDBName, axolotlBeforeDBName, true},
		{chatDBName, chatDBName, false},
		{contactsDBName, contactsDBName, true},
		{axolotlDBName, axolotlDBName, true},
	} {
		_, err := cache.SnapshotSQLite(cache.SQLiteSnapshotOptions{SourcePath: filepath.Join(path, file.source), DestinationDir: root, Name: file.name})
		if err != nil && !(file.optional && errors.Is(err, os.ErrNotExist)) {
			_ = os.RemoveAll(root)
			return Snapshot{}, err
		}
	}
	return Snapshot{Root: root, SourcePath: path}, nil
}

func canonicalSourcePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	absolute, err := mediafile.Resolve(path)
	if err != nil {
		return "", fmt.Errorf("resolve desktop path: %w", err)
	}
	return filepath.Abs(absolute)
}

func defaultedPath(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	return DefaultPath()
}

func openReadOnly(path string) (*sql.DB, func(), error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, err
	}
	dsn, err := sqlitedsn.File(
		path,
		sqlitedsn.P("mode", "ro"),
		sqlitedsn.P("_pragma", "query_only(1)"),
		sqlitedsn.P("_pragma", "busy_timeout(5000)"),
		sqlitedsn.P("_pragma", "temp_store(MEMORY)"),
	)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, func() { _ = db.Close() }, nil
}
