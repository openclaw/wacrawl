package whatsappdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/steipete/wacrawl/internal/store"
	_ "modernc.org/sqlite"
)

const (
	chatDBName     = "ChatStorage.sqlite"
	contactsDBName = "ContactsV2.sqlite"
	appleEpoch     = 978307200
)

type Source struct {
	Path          string   `json:"path"`
	Available     bool     `json:"available"`
	ChatDB        string   `json:"chat_db,omitempty"`
	ContactsDB    string   `json:"contacts_db,omitempty"`
	MediaDir      string   `json:"media_dir,omitempty"`
	SupportingDBs []string `json:"supporting_dbs,omitempty"`
	MessageRows   int      `json:"message_rows,omitempty"`
	ChatRows      int      `json:"chat_rows,omitempty"`
	ContactRows   int      `json:"contact_rows,omitempty"`
	MediaRows     int      `json:"media_rows,omitempty"`
	OldestMessage string   `json:"oldest_message,omitempty"`
	NewestMessage string   `json:"newest_message,omitempty"`
	SchemaNotes   []string `json:"schema_notes,omitempty"`
}

type Snapshot struct {
	Root       string
	SourcePath string
}

type Data struct {
	Contacts     []store.Contact
	Chats        []store.Chat
	Groups       []store.Group
	Participants []store.GroupParticipant
	Messages     []store.Message
	MediaCount   int
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
		_ = chatDB.QueryRowContext(ctx, "select count(*) from ZWAMESSAGE").Scan(&source.MessageRows)
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
		source.SchemaNotes = append(source.SchemaNotes,
			"CoreData tables: ZWACHATSESSION, ZWAMESSAGE, ZWAMEDIAITEM, ZWAGROUPINFO, ZWAGROUPMEMBER",
			"timestamps are seconds since 2001-01-01 UTC",
			"ZWAMESSAGE.ZGROUPMEMBER identifies group senders",
			"ZWAMEDIAITEM joins both via ZWAMESSAGE.ZMEDIAITEM and ZWAMEDIAITEM.ZMESSAGE",
		)
	}
	contactsDB, closeContacts, err := openReadOnly(source.ContactsDB)
	if err == nil {
		defer closeContacts()
		_ = contactsDB.QueryRowContext(ctx, "select count(*) from ZWAADDRESSBOOKCONTACT").Scan(&source.ContactRows)
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
	if err := copySQLiteTriad(path, root, chatDBName); err != nil {
		_ = os.RemoveAll(root)
		return Snapshot{}, err
	}
	if err := copySQLiteTriad(path, root, contactsDBName); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(root)
		return Snapshot{}, err
	}
	return Snapshot{Root: root, SourcePath: path}, nil
}

func Extract(ctx context.Context, snap Snapshot) (Data, error) {
	contacts, names, err := readContacts(ctx, filepath.Join(snap.Root, contactsDBName))
	if err != nil {
		return Data{}, err
	}
	chats, groups, participants, messages, mediaCount, err := readChats(ctx, filepath.Join(snap.Root, chatDBName), snap.SourcePath, names)
	if err != nil {
		return Data{}, err
	}
	return Data{Contacts: contacts, Chats: chats, Groups: groups, Participants: participants, Messages: messages, MediaCount: mediaCount}, nil
}

func Import(ctx context.Context, st *store.Store, path string) (store.ImportStats, error) {
	sourcePath := defaultedPath(path)
	stats := store.ImportStats{SourcePath: sourcePath, DBPath: st.Path(), StartedAt: time.Now().UTC()}
	snap, err := SnapshotPath(sourcePath)
	if err != nil {
		return stats, err
	}
	defer func() { _ = os.RemoveAll(snap.Root) }()
	data, err := Extract(ctx, snap)
	if err != nil {
		return stats, err
	}
	stats.Chats = len(data.Chats)
	stats.Contacts = len(data.Contacts)
	stats.Groups = len(data.Groups)
	stats.Participants = len(data.Participants)
	stats.Messages = len(data.Messages)
	stats.MediaMessages = data.MediaCount
	stats.FinishedAt = time.Now().UTC()
	if err := st.ReplaceAll(ctx, stats, data.Contacts, data.Chats, data.Groups, data.Participants, data.Messages); err != nil {
		return stats, err
	}
	return stats, nil
}

func readContacts(ctx context.Context, path string) ([]store.Contact, map[string]string, error) {
	db, closeFn, err := openReadOnly(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, map[string]string{}, nil
		}
		return nil, nil, err
	}
	defer closeFn()
	rows, err := db.QueryContext(ctx, `select coalesce(ZWHATSAPPID,''), coalesce(ZPHONENUMBER,''), coalesce(ZFULLNAME,''), coalesce(ZGIVENNAME,''), coalesce(ZLASTNAME,''), coalesce(ZBUSINESSNAME,''), coalesce(ZUSERNAME,''), coalesce(ZLID,''), coalesce(ZABOUTTEXT,''), ZLASTUPDATED from ZWAADDRESSBOOKCONTACT`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var contacts []store.Contact
	names := map[string]string{}
	for rows.Next() {
		var c store.Contact
		var updated sql.NullFloat64
		if err := rows.Scan(&c.JID, &c.Phone, &c.FullName, &c.FirstName, &c.LastName, &c.BusinessName, &c.Username, &c.LID, &c.AboutText, &updated); err != nil {
			return nil, nil, err
		}
		if c.JID == "" {
			continue
		}
		c.UpdatedAt = appleNullTime(updated)
		contacts = append(contacts, c)
		name := bestName(c.FullName, c.BusinessName, c.Username, c.FirstName, c.Phone, c.JID)
		names[c.JID] = name
		if c.LID != "" {
			names[c.LID] = name
			names[c.LID+"@lid"] = name
		}
	}
	sort.Slice(contacts, func(i, j int) bool { return contacts[i].JID < contacts[j].JID })
	return contacts, names, rows.Err()
}

func readChats(ctx context.Context, path, sourceRoot string, names map[string]string) ([]store.Chat, []store.Group, []store.GroupParticipant, []store.Message, int, error) {
	db, closeFn, err := openReadOnly(path)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	defer closeFn()
	chats, err := readChatRows(ctx, db)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	groups, err := readGroupRows(ctx, db)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	participants, err := readParticipantRows(ctx, db)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	messages, mediaCount, err := readMessageRows(ctx, db, sourceRoot, names)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	return chats, groups, participants, messages, mediaCount, nil
}

func readChatRows(ctx context.Context, db *sql.DB) ([]store.Chat, error) {
	rows, err := db.QueryContext(ctx, `select coalesce(c.ZCONTACTJID,''), coalesce(c.ZPARTNERNAME,''), c.ZLASTMESSAGEDATE, coalesce(c.ZUNREADCOUNT,0), coalesce(c.ZARCHIVED,0), coalesce(c.ZREMOVED,0), coalesce(c.ZHIDDEN,0), coalesce(c.ZSESSIONTYPE,0), count(m.Z_PK) from ZWACHATSESSION c left join ZWAMESSAGE m on m.ZCHATSESSION=c.Z_PK group by c.Z_PK`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	merged := map[string]store.Chat{}
	for rows.Next() {
		var c store.Chat
		var last sql.NullFloat64
		var archived, removed, hidden int
		if err := rows.Scan(&c.JID, &c.Name, &last, &c.UnreadCount, &archived, &removed, &hidden, &c.RawSessionType, &c.MessageCount); err != nil {
			return nil, err
		}
		if c.JID == "" {
			continue
		}
		c.Kind = chatKind(c.JID, c.RawSessionType)
		c.LastMessageAt = appleNullTime(last)
		c.Archived = archived != 0
		c.Removed = removed != 0
		c.Hidden = hidden != 0
		if existing, ok := merged[c.JID]; ok {
			merged[c.JID] = mergeChatRows(existing, c)
			continue
		}
		merged[c.JID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Chat, 0, len(merged))
	for _, chat := range merged {
		out = append(out, chat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastMessageAt.After(out[j].LastMessageAt) })
	return out, nil
}

func mergeChatRows(existing, candidate store.Chat) store.Chat {
	merged := existing
	older := candidate
	if merged.LastMessageAt.Before(candidate.LastMessageAt) {
		merged = candidate
		older = existing
	}
	merged.MessageCount += older.MessageCount
	if merged.Name == "" {
		merged.Name = older.Name
	}
	if merged.RawSessionType == 0 && older.RawSessionType != 0 {
		merged.RawSessionType = older.RawSessionType
		merged.Kind = chatKind(merged.JID, merged.RawSessionType)
	}
	return merged
}

func readGroupRows(ctx context.Context, db *sql.DB) ([]store.Group, error) {
	rows, err := db.QueryContext(ctx, `select coalesce(c.ZCONTACTJID,''), coalesce(c.ZPARTNERNAME,''), coalesce(g.ZOWNERJID,''), g.ZCREATIONDATE from ZWAGROUPINFO g join ZWACHATSESSION c on c.Z_PK=g.ZCHATSESSION`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.Group
	for rows.Next() {
		var g store.Group
		var created sql.NullFloat64
		if err := rows.Scan(&g.JID, &g.Name, &g.OwnerJID, &created); err != nil {
			return nil, err
		}
		if g.JID == "" {
			continue
		}
		g.CreatedAt = appleNullTime(created)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	return out, rows.Err()
}

func readParticipantRows(ctx context.Context, db *sql.DB) ([]store.GroupParticipant, error) {
	rows, err := db.QueryContext(ctx, `select coalesce(c.ZCONTACTJID,''), coalesce(gm.ZMEMBERJID,''), coalesce(gm.ZCONTACTNAME,''), coalesce(gm.ZFIRSTNAME,''), coalesce(gm.ZISADMIN,0), coalesce(gm.ZISACTIVE,0) from ZWAGROUPMEMBER gm join ZWACHATSESSION c on c.Z_PK=gm.ZCHATSESSION`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.GroupParticipant
	for rows.Next() {
		var p store.GroupParticipant
		var admin, active int
		if err := rows.Scan(&p.GroupJID, &p.UserJID, &p.ContactName, &p.FirstName, &admin, &active); err != nil {
			return nil, err
		}
		if p.GroupJID == "" || p.UserJID == "" {
			continue
		}
		p.IsAdmin = admin != 0
		p.IsActive = active != 0
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupJID == out[j].GroupJID {
			return out[i].UserJID < out[j].UserJID
		}
		return out[i].GroupJID < out[j].GroupJID
	})
	return out, rows.Err()
}

func readMessageRows(ctx context.Context, db *sql.DB, sourceRoot string, names map[string]string) ([]store.Message, int, error) {
	rows, err := db.QueryContext(ctx, `
select m.Z_PK, coalesce(c.ZCONTACTJID,''), coalesce(c.ZPARTNERNAME,''), coalesce(m.ZSTANZAID,''), coalesce(m.ZISFROMME,0), m.ZMESSAGEDATE,
       coalesce(m.ZTEXT,''), coalesce(m.ZMESSAGETYPE,0), coalesce(m.ZSTARRED,0), coalesce(m.ZFROMJID,''), coalesce(m.ZTOJID,''), coalesce(m.ZPUSHNAME,''),
       coalesce(gm.ZMEMBERJID,''), coalesce(gm.ZCONTACTNAME,''), coalesce(gm.ZFIRSTNAME,''),
       coalesce(mi.ZMEDIALOCALPATH,''), coalesce(mi.ZMEDIAURL,''), coalesce(mi.ZTITLE,''), coalesce(mi.ZVCARDNAME,''), coalesce(mi.ZFILESIZE,0)
from ZWAMESSAGE m
left join ZWACHATSESSION c on c.Z_PK=m.ZCHATSESSION
left join ZWAGROUPMEMBER gm on gm.Z_PK=m.ZGROUPMEMBER
left join ZWAMEDIAITEM mi on mi.Z_PK=m.ZMEDIAITEM
order by m.ZMESSAGEDATE asc, m.Z_PK asc`)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.Message
	mediaCount := 0
	for rows.Next() {
		var m store.Message
		var msgDate sql.NullFloat64
		var fromMe, starred int
		var fromJID, toJID, pushName, memberJID, memberName, memberFirst, mediaPath, mediaURL, mediaTitle, vcardName string
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &fromMe, &msgDate, &m.Text, &m.RawType, &starred, &fromJID, &toJID, &pushName, &memberJID, &memberName, &memberFirst, &mediaPath, &mediaURL, &mediaTitle, &vcardName, &m.MediaSize); err != nil {
			return nil, 0, err
		}
		if m.ChatJID == "" || m.MessageID == "" {
			continue
		}
		m.Timestamp = appleNullTime(msgDate)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		m.MessageType = messageType(m.RawType)
		m.MediaType = mediaType(m.RawType)
		m.MediaTitle = firstNonEmpty(mediaTitle, vcardName)
		if mediaPath != "" {
			m.MediaPath = filepath.Join(sourceRoot, filepath.FromSlash(mediaPath))
		}
		m.MediaURL = mediaURL
		m.SenderJID, m.SenderName = sender(m.FromMe, m.ChatJID, fromJID, toJID, pushName, memberJID, memberName, memberFirst, names)
		if m.Text == "" && m.MediaTitle != "" {
			m.Text = m.MediaTitle
		}
		if m.MediaType != "" || m.MediaPath != "" || m.MediaURL != "" {
			mediaCount++
		}
		out = append(out, m)
	}
	return out, mediaCount, rows.Err()
}

func sender(fromMe bool, chatJID, fromJID, toJID, pushName, memberJID, memberName, memberFirst string, names map[string]string) (string, string) {
	if fromMe {
		return firstNonEmpty(toJID), "me"
	}
	jid := firstNonEmpty(memberJID, fromJID, chatJID)
	name := firstNonEmpty(memberName, memberFirst, pushName, names[jid], names[strings.TrimSuffix(jid, "@lid")], jid)
	return jid, name
}

func chatKind(jid string, raw int) string {
	switch {
	case strings.HasSuffix(jid, "@g.us"):
		return "group"
	case strings.Contains(jid, "@newsletter"):
		return "newsletter"
	case strings.Contains(jid, "@status") || jid == "status@broadcast":
		return "status"
	case raw == 3:
		return "status"
	default:
		return "dm"
	}
}

func messageType(raw int) string {
	switch raw {
	case 0:
		return "text"
	case 1:
		return "image"
	case 2:
		return "video"
	case 3:
		return "audio"
	case 4:
		return "location"
	case 5:
		return "contact"
	case 6:
		return "system"
	case 7:
		return "link"
	case 8:
		return "document"
	case 10:
		return "group_event"
	case 11:
		return "gif"
	case 14:
		return "reaction"
	case 15:
		return "sticker"
	default:
		return fmt.Sprintf("type_%d", raw)
	}
}

func mediaType(raw int) string {
	switch raw {
	case 1:
		return "image"
	case 2:
		return "video"
	case 3:
		return "audio"
	case 7:
		return "link"
	case 8:
		return "document"
	case 11:
		return "gif"
	case 15:
		return "sticker"
	default:
		return ""
	}
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
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)", path)
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

func copySQLiteTriad(srcDir, dstDir, name string) error {
	src := filepath.Join(srcDir, name)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFileIfExists(src+suffix, filepath.Join(dstDir, name+suffix)); err != nil {
			return err
		}
	}
	return nil
}

func copyFileIfExists(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- source path is the local WhatsApp container selected for readonly snapshotting.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- destination is inside a mktemp snapshot dir.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func appleNullTime(v sql.NullFloat64) time.Time {
	if !v.Valid || v.Float64 <= 0 {
		return time.Time{}
	}
	return appleTime(v.Float64)
}

func appleTime(seconds float64) time.Time {
	whole := int64(seconds)
	nano := int64((seconds - float64(whole)) * 1e9)
	return time.Unix(whole+appleEpoch, nano).UTC()
}

func bestName(values ...string) string {
	return firstNonEmpty(values...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
