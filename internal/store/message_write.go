package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func upsertMessage(ctx context.Context, tx *sql.Tx, m Message, observedAt time.Time, mediaRoots ...string) error {
	sourcePayloadCleared := m.SourceTextNull && !m.SourceMediaPathRejected && m.RawType == 0 && m.MediaTitle == "" && m.MediaType == "" && m.MediaPath == "" && m.MediaURL == "" && m.DeletedAt.IsZero()
	preserveExistingFTS := false
	if m.EventID == "" {
		m.EventID = messageEventID(m.SourcePK)
	}
	if m.SourceRowPK == 0 {
		m.SourceRowPK = m.SourcePK
	}
	m.Tombstone = normalizedTombstone(m.Tombstone, observedAt)
	existing, found, err := messageBySourcePK(ctx, tx, m.SourcePK)
	if err != nil {
		return err
	}
	if found {
		if messageIdentityConflict(existing, m) {
			return fmt.Errorf("message source_pk %d belongs to a different event; use a separate archive or import --restore", m.SourcePK)
		}
		if !existing.DeletedAt.IsZero() {
			return nil
		}
		if sourcePayloadCleared && messageHasPayload(existing) {
			m.Tombstone = sourceTombstone(observedAt, "whatsapp_payload_cleared")
			preserveExistingFTS = messageHasPayload(existing)
		}
		if !sourcePayloadCleared && m.DeletedAt.IsZero() && len(mediaRoots) != 0 {
			m, err = retainArchivedMedia(mediaRoots[0], existing, m)
			if err != nil {
				return err
			}
		}
		m.EventID = existing.EventID
		previous, err := canonicalMessageJSON(existing)
		if err != nil {
			return err
		}
		incoming, err := canonicalMessageJSON(m)
		if err != nil {
			return err
		}
		if previous != incoming {
			reason := "whatsapp_edit"
			if !m.DeletedAt.IsZero() {
				reason = m.DeletionReason
			}
			if _, err := tx.ExecContext(ctx, `insert into message_revisions(event_id,payload_json,recorded_at,event_source,reason) values(?,?,?,?,?)`,
				existing.EventID, previous, unix(observedAt), "whatsapp-desktop", reason); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `insert into messages(
source_pk,source_row_pk,event_id,chat_jid,chat_name,msg_id,sender_jid,sender_name,ts,from_me,text,
raw_type,message_type,media_type,media_title,media_path,media_url,media_size,starred,
deleted_at,deletion_source,deletion_reason,last_seen_at)
values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(source_pk) do update set source_row_pk=excluded.source_row_pk, event_id=excluded.event_id, chat_jid=excluded.chat_jid,
chat_name=excluded.chat_name, msg_id=excluded.msg_id, sender_jid=excluded.sender_jid,
sender_name=excluded.sender_name, ts=excluded.ts, from_me=excluded.from_me, text=excluded.text,
raw_type=excluded.raw_type, message_type=excluded.message_type, media_type=excluded.media_type,
media_title=excluded.media_title, media_path=excluded.media_path, media_url=excluded.media_url,
media_size=excluded.media_size, starred=excluded.starred, deleted_at=excluded.deleted_at,
deletion_source=excluded.deletion_source, deletion_reason=excluded.deletion_reason,
last_seen_at=excluded.last_seen_at`,
		m.SourcePK, m.SourceRowPK, m.EventID, m.ChatJID, m.ChatName, m.MessageID, m.SenderJID, m.SenderName, messageUnix(m), boolInt(m.FromMe), m.Text,
		m.RawType, m.MessageType, m.MediaType, m.MediaTitle, m.MediaPath, m.MediaURL, m.MediaSize, boolInt(m.Starred),
		nullableUnix(m.DeletedAt), nullableString(m.DeletionSource), nullableString(m.DeletionReason), unix(m.LastSeenAt)); err != nil {
		return err
	}
	if !preserveExistingFTS {
		if _, err := tx.ExecContext(ctx, `delete from messages_fts where rowid=(select rowid from messages where source_pk=?)`, m.SourcePK); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into messages_fts(rowid,text,chat,sender,media)
values((select rowid from messages where source_pk=?),?,?,?,?)`, m.SourcePK,
			strings.TrimSpace(m.Text+" "+m.MediaTitle), m.ChatName, m.SenderName, m.MediaType); err != nil {
			return err
		}
	}
	return nil
}

func messageHasPayload(message Message) bool {
	return strings.TrimSpace(message.Text) != "" || strings.TrimSpace(message.MediaTitle) != "" || message.MediaType != "" || message.MediaPath != "" || message.MediaURL != ""
}

func canonicalMessageJSON(m Message) (string, error) {
	payload := struct {
		SourcePK       int64  `json:"source_pk"`
		SourceRowPK    int64  `json:"source_row_pk"`
		EventID        string `json:"event_id"`
		ChatJID        string `json:"chat_jid"`
		ChatName       string `json:"chat_name,omitempty"`
		MessageID      string `json:"message_id"`
		SenderJID      string `json:"sender_jid,omitempty"`
		SenderName     string `json:"sender_name,omitempty"`
		Timestamp      int64  `json:"timestamp_unix"`
		FromMe         bool   `json:"from_me"`
		Text           string `json:"text,omitempty"`
		RawType        int    `json:"raw_type"`
		MessageType    string `json:"message_type,omitempty"`
		MediaType      string `json:"media_type,omitempty"`
		MediaTitle     string `json:"media_title,omitempty"`
		MediaPath      string `json:"media_path,omitempty"`
		MediaURL       string `json:"media_url,omitempty"`
		MediaSize      int64  `json:"media_size,omitempty"`
		Starred        bool   `json:"starred,omitempty"`
		DeletedAt      int64  `json:"deleted_at,omitempty"`
		DeletionSource string `json:"deletion_source,omitempty"`
		DeletionReason string `json:"deletion_reason,omitempty"`
	}{
		SourcePK: m.SourcePK, SourceRowPK: m.SourceRowPK, EventID: m.EventID, ChatJID: m.ChatJID, ChatName: m.ChatName,
		MessageID: m.MessageID, SenderJID: m.SenderJID, SenderName: m.SenderName,
		Timestamp: messageUnix(m), FromMe: m.FromMe, Text: m.Text, RawType: m.RawType,
		MessageType: m.MessageType, MediaType: m.MediaType, MediaTitle: m.MediaTitle,
		MediaPath: m.MediaPath, MediaURL: m.MediaURL, MediaSize: m.MediaSize, Starred: m.Starred,
		DeletedAt: unix(m.DeletedAt), DeletionSource: m.DeletionSource, DeletionReason: m.DeletionReason,
	}
	data, err := json.Marshal(payload)
	return string(data), err
}
