package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func prepareImportTombstones(now time.Time, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) {
	deletedChats := make(map[string]Tombstone)
	for i := range chats {
		if chats[i].Removed && chats[i].DeletedAt.IsZero() {
			chats[i].Tombstone = sourceTombstone(now, "whatsapp_removed")
		}
		if !chats[i].DeletedAt.IsZero() {
			deletedChats[chats[i].JID] = chats[i].Tombstone
		}
	}
	deletedGroups := make(map[string]Tombstone)
	for i := range groups {
		if parent, ok := deletedChats[groups[i].JID]; ok && groups[i].DeletedAt.IsZero() {
			groups[i].Tombstone = childTombstone(parent, "parent_chat_deleted")
		}
		if !groups[i].DeletedAt.IsZero() {
			deletedGroups[groups[i].JID] = groups[i].Tombstone
		}
	}
	for i := range participants {
		if parent, ok := deletedGroups[participants[i].GroupJID]; ok && participants[i].DeletedAt.IsZero() {
			participants[i].Tombstone = childTombstone(parent, "parent_group_deleted")
		} else if !participants[i].IsActive && participants[i].DeletedAt.IsZero() {
			participants[i].Tombstone = sourceTombstone(now, "whatsapp_inactive")
		}
	}
	for i := range messages {
		if parent, ok := deletedChats[messages[i].ChatJID]; ok && messages[i].DeletedAt.IsZero() {
			messages[i].Tombstone = childTombstone(parent, "parent_chat_deleted")
		}
	}
}

func prepareStoredParentTombstones(ctx context.Context, tx *sql.Tx, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	liveChats := make(map[string]struct{})
	for _, chat := range chats {
		if chat.DeletedAt.IsZero() {
			liveChats[chat.JID] = struct{}{}
		}
	}
	for i := range groups {
		if !groups[i].DeletedAt.IsZero() {
			continue
		}
		if _, live := liveChats[groups[i].JID]; live {
			continue
		}
		parent, found, err := storedTombstone(ctx, tx, "chats", "jid", groups[i].JID)
		if err != nil {
			return err
		}
		if found {
			groups[i].Tombstone = childTombstone(parent, "parent_chat_deleted")
		}
	}
	liveGroups := make(map[string]struct{})
	for _, group := range groups {
		if group.DeletedAt.IsZero() {
			liveGroups[group.JID] = struct{}{}
		}
	}
	for i := range participants {
		if !participants[i].DeletedAt.IsZero() {
			continue
		}
		if _, live := liveGroups[participants[i].GroupJID]; live {
			continue
		}
		parent, found, err := storedTombstone(ctx, tx, "groups", "jid", participants[i].GroupJID)
		if err != nil {
			return err
		}
		if found {
			participants[i].Tombstone = childTombstone(parent, "parent_group_deleted")
		}
	}
	for i := range messages {
		if !messages[i].DeletedAt.IsZero() {
			continue
		}
		if _, live := liveChats[messages[i].ChatJID]; live {
			continue
		}
		parent, found, err := storedTombstone(ctx, tx, "chats", "jid", messages[i].ChatJID)
		if err != nil {
			return err
		}
		if found {
			messages[i].Tombstone = childTombstone(parent, "parent_chat_deleted")
		}
	}
	return nil
}

func storedTombstone(ctx context.Context, tx *sql.Tx, table, key, value string) (Tombstone, bool, error) {
	var deletedAt, lastSeenAt int64
	var source, reason string
	query := "select deleted_at,coalesce(deletion_source,''),coalesce(deletion_reason,''),last_seen_at from " + table + " where " + key + "=? and deleted_at is not null" //nolint:gosec // fixed internal table and key names only.
	err := tx.QueryRowContext(ctx, query, value).Scan(&deletedAt, &source, &reason, &lastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Tombstone{}, false, nil
	}
	if err != nil {
		return Tombstone{}, false, err
	}
	return Tombstone{DeletedAt: fromUnix(deletedAt), DeletionSource: source, DeletionReason: reason, LastSeenAt: fromUnix(lastSeenAt)}, true, nil
}

func sourceTombstone(at time.Time, reason string) Tombstone {
	return Tombstone{DeletedAt: at, DeletionSource: "whatsapp-desktop", DeletionReason: reason, LastSeenAt: at}
}

func childTombstone(parent Tombstone, reason string) Tombstone {
	return Tombstone{DeletedAt: parent.DeletedAt, DeletionSource: parent.DeletionSource, DeletionReason: reason, LastSeenAt: parent.LastSeenAt}
}

func normalizedTombstone(t Tombstone, observedAt time.Time) Tombstone {
	if t.LastSeenAt.IsZero() {
		t.LastSeenAt = observedAt
	}
	if !t.DeletedAt.IsZero() {
		if t.DeletionSource == "" {
			t.DeletionSource = "snapshot"
		}
		if t.DeletionReason == "" {
			t.DeletionReason = "explicit_tombstone"
		}
	}
	return t
}

func tombstoneSubordinates(ctx context.Context, tx *sql.Tx, observedAt time.Time) error {
	rows, err := tx.QueryContext(ctx, `select `+messageSelectColumns+` from messages m
where m.deleted_at is null and exists (select 1 from chats c where c.jid=m.chat_jid and c.deleted_at is not null)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var messages []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return err
		}
		messages = append(messages, m)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range messages {
		var deletedAt int64
		var source string
		if err := tx.QueryRowContext(ctx, `select deleted_at,coalesce(deletion_source,'whatsapp-desktop') from chats where jid=?`, m.ChatJID).Scan(&deletedAt, &source); err != nil {
			return err
		}
		m.Tombstone = Tombstone{DeletedAt: fromUnix(deletedAt), DeletionSource: source, DeletionReason: "parent_chat_deleted", LastSeenAt: observedAt}
		if err := upsertMessage(ctx, tx, m, observedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `update groups set
deleted_at=coalesce(deleted_at,(select deleted_at from chats where chats.jid=groups.jid)),
deletion_source=coalesce(deletion_source,(select deletion_source from chats where chats.jid=groups.jid),'whatsapp-desktop'),
deletion_reason=coalesce(deletion_reason,'parent_chat_deleted')
where deleted_at is null and exists(select 1 from chats where chats.jid=groups.jid and chats.deleted_at is not null)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update group_participants set
deleted_at=coalesce(deleted_at,(select deleted_at from groups where groups.jid=group_participants.group_jid)),
deletion_source=coalesce(deletion_source,(select deletion_source from groups where groups.jid=group_participants.group_jid),'whatsapp-desktop'),
deletion_reason=coalesce(deletion_reason,'parent_group_deleted')
where deleted_at is null and exists(select 1 from groups where groups.jid=group_participants.group_jid and groups.deleted_at is not null)`); err != nil {
		return err
	}
	return nil
}
