package whatsappdb

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/openclaw/wacrawl/internal/store"
)

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
		name := firstNonEmpty(c.FullName, c.BusinessName, c.Username, c.FirstName, c.Phone, c.JID)
		names[c.JID] = name
		if c.LID != "" {
			names[c.LID] = name
			names[c.LID+"@lid"] = name
		}
	}
	slices.SortFunc(contacts, func(a, b store.Contact) int { return cmp.Compare(a.JID, b.JID) })
	return contacts, names, rows.Err()
}

func readChats(ctx context.Context, path, sourceRoot string, names map[string]string) (Data, error) {
	db, closeFn, err := openReadOnly(path)
	if err != nil {
		return Data{}, err
	}
	defer closeFn()
	profileNames, err := readProfilePushNameRows(ctx, db)
	if err != nil {
		return Data{}, err
	}
	mergeMissingNames(names, profileNames)
	chats, err := readChatRows(ctx, db)
	if err != nil {
		return Data{}, err
	}
	groups, err := readGroupRows(ctx, db)
	if err != nil {
		return Data{}, err
	}
	participants, err := readParticipantRows(ctx, db)
	if err != nil {
		return Data{}, err
	}
	messages, mediaCount, err := readMessageRows(ctx, db, sourceRoot, names)
	if err != nil {
		return Data{}, err
	}
	return Data{Chats: chats, Groups: groups, Participants: participants, Messages: messages, MediaCount: mediaCount}, nil
}

func readProfilePushNameRows(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `select coalesce(ZJID,''), coalesce(ZPUSHNAME,'') from ZWAPROFILEPUSHNAME`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: ZWAPROFILEPUSHNAME") {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := map[string]string{}
	for rows.Next() {
		var jid, name string
		if err := rows.Scan(&jid, &name); err != nil {
			return nil, err
		}
		if jid != "" && name != "" {
			names[jid] = name
		}
	}
	return names, rows.Err()
}

func mergeMissingNames(dst, src map[string]string) {
	for jid, name := range src {
		if strings.TrimSpace(dst[jid]) == "" {
			dst[jid] = name
		}
	}
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
	slices.SortFunc(out, func(a, b store.Chat) int { return b.LastMessageAt.Compare(a.LastMessageAt) })
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
	merged := map[string]store.Group{}
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
		if existing, ok := merged[g.JID]; ok {
			merged[g.JID] = mergeGroupRows(existing, g)
			continue
		}
		merged[g.JID] = g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Group, 0, len(merged))
	for _, group := range merged {
		out = append(out, group)
	}
	slices.SortFunc(out, func(a, b store.Group) int { return cmp.Compare(a.JID, b.JID) })
	return out, nil
}

func mergeGroupRows(existing, candidate store.Group) store.Group {
	merged := existing
	if merged.Name == "" {
		merged.Name = candidate.Name
	}
	if merged.OwnerJID == "" {
		merged.OwnerJID = candidate.OwnerJID
	}
	if merged.CreatedAt.IsZero() || (!candidate.CreatedAt.IsZero() && candidate.CreatedAt.Before(merged.CreatedAt)) {
		merged.CreatedAt = candidate.CreatedAt
	}
	return merged
}

func readParticipantRows(ctx context.Context, db *sql.DB) ([]store.GroupParticipant, error) {
	rows, err := db.QueryContext(ctx, `select coalesce(c.ZCONTACTJID,''), coalesce(gm.ZMEMBERJID,''), coalesce(gm.ZCONTACTNAME,''), coalesce(gm.ZFIRSTNAME,''), coalesce(gm.ZISADMIN,0), coalesce(gm.ZISACTIVE,0) from ZWAGROUPMEMBER gm join ZWACHATSESSION c on c.Z_PK=gm.ZCHATSESSION`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	merged := map[string]store.GroupParticipant{}
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
		key := p.GroupJID + "\x00" + p.UserJID
		if existing, ok := merged[key]; ok {
			merged[key] = mergeParticipantRows(existing, p)
			continue
		}
		merged[key] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]store.GroupParticipant, 0, len(merged))
	for _, participant := range merged {
		out = append(out, participant)
	}
	slices.SortFunc(out, func(a, b store.GroupParticipant) int {
		return cmp.Or(cmp.Compare(a.GroupJID, b.GroupJID), cmp.Compare(a.UserJID, b.UserJID))
	})
	return out, nil
}

func mergeParticipantRows(existing, candidate store.GroupParticipant) store.GroupParticipant {
	merged := existing
	if merged.ContactName == "" {
		merged.ContactName = candidate.ContactName
	}
	if merged.FirstName == "" {
		merged.FirstName = candidate.FirstName
	}
	merged.IsAdmin = merged.IsAdmin || candidate.IsAdmin
	merged.IsActive = merged.IsActive || candidate.IsActive
	return merged
}

func readMessageRows(ctx context.Context, db *sql.DB, sourceRoot string, names map[string]string) ([]store.Message, int, error) {
	rows, err := db.QueryContext(ctx, `
select m.Z_PK, coalesce(c.ZCONTACTJID,''), coalesce(c.ZPARTNERNAME,''), coalesce(m.ZSTANZAID,''), coalesce(m.ZISFROMME,0), m.ZMESSAGEDATE,
       m.ZTEXT, coalesce(m.ZMESSAGETYPE,0), coalesce(m.ZSTARRED,0), coalesce(m.ZFROMJID,''), coalesce(m.ZTOJID,''), coalesce(m.ZPUSHNAME,''),
       coalesce(gm.ZMEMBERJID,''), coalesce(gm.ZCONTACTNAME,''), coalesce(gm.ZFIRSTNAME,''),
       coalesce(mi.ZMEDIALOCALPATH,''), coalesce(mi.ZMEDIAURL,''), coalesce(mi.ZTITLE,''), coalesce(mi.ZVCARDNAME,''), coalesce(mi.ZFILESIZE,0)
from ZWAMESSAGE m
left join ZWACHATSESSION c on c.Z_PK=m.ZCHATSESSION
left join ZWAGROUPMEMBER gm on gm.Z_PK=m.ZGROUPMEMBER
left join ZWAMEDIAITEM mi on mi.Z_PK=coalesce(nullif(m.ZMEDIAITEM, 0), (select mi2.Z_PK from ZWAMEDIAITEM mi2 where mi2.ZMESSAGE=m.Z_PK order by mi2.Z_PK limit 1))
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
		var text sql.NullString
		var fromMe, starred int
		var fromJID, toJID, pushName, memberJID, memberName, memberFirst, mediaPath, mediaURL, mediaTitle, vcardName string
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &fromMe, &msgDate, &text, &m.RawType, &starred, &fromJID, &toJID, &pushName, &memberJID, &memberName, &memberFirst, &mediaPath, &mediaURL, &mediaTitle, &vcardName, &m.MediaSize); err != nil {
			return nil, 0, err
		}
		m.SourceRowPK = m.SourcePK
		if m.ChatJID == "" || m.MessageID == "" {
			continue
		}
		m.Timestamp = appleNullTime(msgDate)
		m.Text = text.String
		m.SourceTextNull = !text.Valid
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		m.MessageType = messageType(m.RawType)
		m.MediaType = mediaType(m.RawType)
		m.MediaTitle = firstNonEmpty(mediaTitle, vcardName)
		if mediaPath != "" {
			m.MediaPath = resolveDesktopMediaPath(sourceRoot, mediaPath)
			m.SourceMediaPathRejected = m.MediaPath == ""
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
	name := firstNonEmpty(memberName, resolvedName(jid, names), memberFirst, pushName, jid)
	return jid, name
}

func resolvedName(jid string, names map[string]string) string {
	for _, key := range []string{jid, strings.TrimSuffix(jid, "@lid")} {
		if name := strings.TrimSpace(names[key]); name != "" && name != key && name != jid {
			return name
		}
	}
	return ""
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
	case 1, 2, 3, 7, 8, 11, 15:
		return messageType(raw)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
