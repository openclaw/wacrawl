package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ReplaceAll(ctx context.Context, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	stats.Mode = "restore"
	return s.importAll(ctx, true, stats, contacts, chats, groups, participants, messages, nil)
}

func (s *Store) MergeAll(ctx context.Context, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	stats.Mode = "merge"
	return s.importAll(ctx, false, stats, contacts, chats, groups, participants, messages, nil)
}

func (s *Store) ValidateImport(ctx context.Context, stats ImportStats, messages []Message, restore bool) error {
	if err := validateImportMessages(messages); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer rollback(tx)
	messages, err = resolveReusedReactionIdentities(ctx, tx, restore, messages)
	if err != nil {
		return err
	}
	_, err = validateImportSource(ctx, tx, restore, stats, messages)
	return err
}

func (s *Store) importAll(ctx context.Context, restore bool, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message, revisions []MessageRevision) error {
	if err := validateImportMessages(messages); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	messages, err = resolveReusedReactionIdentities(ctx, tx, restore, messages)
	if err != nil {
		return err
	}
	mergeSource, err := validateImportSource(ctx, tx, restore, stats, messages)
	if err != nil {
		return err
	}
	if restore {
		if _, err := tx.ExecContext(ctx, `
delete from messages_fts;
delete from message_revisions;
delete from messages;
delete from group_participants;
delete from groups;
delete from chats;
delete from contacts;
delete from sync_state;`); err != nil {
			return err
		}
	}
	now := stats.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	observedAt := unix(now)
	sourceSnapshotAt := stats.SourceSnapshotAt
	if sourceSnapshotAt.IsZero() {
		sourceSnapshotAt = now
	}
	prepareImportTombstones(now, chats, groups, participants, messages)
	if err := prepareStoredParentTombstones(ctx, tx, chats, groups, participants, messages); err != nil {
		return err
	}
	for _, c := range contacts {
		t := normalizedTombstone(c.Tombstone, now)
		if _, err := tx.ExecContext(ctx, `insert into contacts(
jid, phone, full_name, first_name, last_name, business_name, username, lid, about_text, updated_at,
deleted_at, deletion_source, deletion_reason, last_seen_at)
values(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(jid) do update set
phone=excluded.phone, full_name=excluded.full_name, first_name=excluded.first_name,
last_name=excluded.last_name, business_name=excluded.business_name, username=excluded.username,
lid=excluded.lid, about_text=excluded.about_text, updated_at=excluded.updated_at,
deleted_at=case when contacts.deleted_at is not null then contacts.deleted_at else excluded.deleted_at end,
deletion_source=case when contacts.deleted_at is not null then contacts.deletion_source else excluded.deletion_source end,
deletion_reason=case when contacts.deleted_at is not null then contacts.deletion_reason else excluded.deletion_reason end,
last_seen_at=excluded.last_seen_at`,
			c.JID, c.Phone, c.FullName, c.FirstName, c.LastName, c.BusinessName, c.Username, c.LID, c.AboutText, unix(c.UpdatedAt),
			nullableUnix(t.DeletedAt), nullableString(t.DeletionSource), nullableString(t.DeletionReason), unix(t.LastSeenAt)); err != nil {
			return err
		}
	}
	for _, c := range chats {
		t := normalizedTombstone(c.Tombstone, now)
		if _, err := tx.ExecContext(ctx, `insert into chats(
jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type,
deleted_at, deletion_source, deletion_reason, last_seen_at)
values(?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(jid) do update set
kind=excluded.kind, name=excluded.name, last_message_at=excluded.last_message_at,
unread_count=excluded.unread_count, archived=excluded.archived, removed=excluded.removed,
hidden=excluded.hidden, raw_session_type=excluded.raw_session_type,
deleted_at=case when chats.deleted_at is not null and excluded.deleted_at is null and excluded.last_message_at > chats.deleted_at then null when chats.deleted_at is not null then chats.deleted_at else excluded.deleted_at end,
deletion_source=case when chats.deleted_at is not null and excluded.deleted_at is null and excluded.last_message_at > chats.deleted_at then null when chats.deleted_at is not null then chats.deletion_source else excluded.deletion_source end,
deletion_reason=case when chats.deleted_at is not null and excluded.deleted_at is null and excluded.last_message_at > chats.deleted_at then null when chats.deleted_at is not null then chats.deletion_reason else excluded.deletion_reason end,
last_seen_at=excluded.last_seen_at`,
			c.JID, c.Kind, c.Name, unix(c.LastMessageAt), c.UnreadCount, boolInt(c.Archived), boolInt(c.Removed), boolInt(c.Hidden), c.RawSessionType,
			nullableUnix(t.DeletedAt), nullableString(t.DeletionSource), nullableString(t.DeletionReason), unix(t.LastSeenAt)); err != nil {
			return err
		}
	}
	for _, g := range groups {
		t := normalizedTombstone(g.Tombstone, now)
		if _, err := tx.ExecContext(ctx, `insert into groups(
jid, name, owner_jid, created_at, deleted_at, deletion_source, deletion_reason, last_seen_at)
values(?,?,?,?,?,?,?,?)
on conflict(jid) do update set name=excluded.name, owner_jid=excluded.owner_jid,
created_at=excluded.created_at,
deleted_at=case when groups.deleted_at is not null and excluded.deleted_at is null and exists(select 1 from chats where jid=excluded.jid and deleted_at is null) then null when groups.deleted_at is not null then groups.deleted_at else excluded.deleted_at end,
deletion_source=case when groups.deleted_at is not null and excluded.deleted_at is null and exists(select 1 from chats where jid=excluded.jid and deleted_at is null) then null when groups.deleted_at is not null then groups.deletion_source else excluded.deletion_source end,
deletion_reason=case when groups.deleted_at is not null and excluded.deleted_at is null and exists(select 1 from chats where jid=excluded.jid and deleted_at is null) then null when groups.deleted_at is not null then groups.deletion_reason else excluded.deletion_reason end,
last_seen_at=excluded.last_seen_at`,
			g.JID, g.Name, g.OwnerJID, unix(g.CreatedAt), nullableUnix(t.DeletedAt), nullableString(t.DeletionSource), nullableString(t.DeletionReason), unix(t.LastSeenAt)); err != nil {
			return err
		}
	}
	for _, p := range participants {
		t := normalizedTombstone(p.Tombstone, now)
		if _, err := tx.ExecContext(ctx, `insert into group_participants(
group_jid, user_jid, contact_name, first_name, is_admin, is_active,
deleted_at, deletion_source, deletion_reason, last_seen_at)
values(?,?,?,?,?,?,?,?,?,?)
on conflict(group_jid,user_jid) do update set contact_name=excluded.contact_name,
first_name=excluded.first_name, is_admin=excluded.is_admin, is_active=excluded.is_active,
deleted_at=case when group_participants.deleted_at is not null and excluded.deleted_at is null and excluded.is_active=1 and exists(select 1 from groups where jid=excluded.group_jid and deleted_at is null) then null when group_participants.deleted_at is not null then group_participants.deleted_at else excluded.deleted_at end,
deletion_source=case when group_participants.deleted_at is not null and excluded.deleted_at is null and excluded.is_active=1 and exists(select 1 from groups where jid=excluded.group_jid and deleted_at is null) then null when group_participants.deleted_at is not null then group_participants.deletion_source else excluded.deletion_source end,
deletion_reason=case when group_participants.deleted_at is not null and excluded.deleted_at is null and excluded.is_active=1 and exists(select 1 from groups where jid=excluded.group_jid and deleted_at is null) then null when group_participants.deleted_at is not null then group_participants.deletion_reason else excluded.deletion_reason end,
last_seen_at=excluded.last_seen_at`,
			p.GroupJID, p.UserJID, p.ContactName, p.FirstName, boolInt(p.IsAdmin), boolInt(p.IsActive),
			nullableUnix(t.DeletedAt), nullableString(t.DeletionSource), nullableString(t.DeletionReason), unix(t.LastSeenAt)); err != nil {
			return err
		}
	}
	var mediaRoots []string
	if !restore && stats.SourceIdentity != "" && stats.MediaRoot != "" {
		mediaRoots = []string{stats.MediaRoot}
	}
	for _, m := range messages {
		if err := upsertMessage(ctx, tx, m, now, mediaRoots...); err != nil {
			return err
		}
	}
	for _, revision := range revisions {
		if _, err := tx.ExecContext(ctx, `insert into message_revisions(event_id,payload_json,recorded_at,event_source,reason) values(?,?,?,?,?)`,
			revision.EventID, revision.PayloadJSON, unix(revision.RecordedAt), revision.EventSource, revision.Reason); err != nil {
			return err
		}
	}
	if err := tombstoneSubordinates(ctx, tx, now); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"last_import_at":        now.Format(time.RFC3339Nano),
		"source_path":           stats.SourcePath,
		"import_mode":           stats.Mode,
		"source_messages":       fmt.Sprintf("%d", stats.Messages),
		"source_contacts":       fmt.Sprintf("%d", stats.Contacts),
		"source_newest_message": formatSyncTime(stats.SourceNewestMessage),
		"source_snapshot_at":    formatSyncTime(sourceSnapshotAt),
	} {
		if _, err := tx.ExecContext(ctx, `insert into sync_state(key,value,updated_at) values(?,?,?)
on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, key, value, observedAt); err != nil {
			return err
		}
	}
	if strings.TrimSpace(mergeSource) != "" {
		if _, err := tx.ExecContext(ctx, `insert into sync_state(key,value,updated_at) values('merge_source_path',?,?)
on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, mergeSource, observedAt); err != nil {
			return err
		}
	}
	if strings.TrimSpace(stats.SourceStoreIdentity) != "" {
		if _, err := tx.ExecContext(ctx, `insert into sync_state(key,value,updated_at) values('merge_source_store_identity',?,?)
on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, strings.TrimSpace(stats.SourceStoreIdentity), observedAt); err != nil {
			return err
		}
	}
	if strings.TrimSpace(stats.AccountIdentity) != "" {
		if _, err := tx.ExecContext(ctx, `insert into sync_state(key,value,updated_at) values('merge_account_identity',?,?)
on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, strings.TrimSpace(stats.AccountIdentity), observedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
