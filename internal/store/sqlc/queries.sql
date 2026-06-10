-- name: CountChats :one
select count(*) from chats;

-- name: CountUnreadChats :one
select count(*) from chats where unread_count > 0;

-- name: CountUnreadMessages :one
select cast(coalesce(sum(unread_count), 0) as integer) as unread_messages from chats;

-- name: CountContacts :one
select count(*) from contacts;

-- name: CountGroups :one
select count(*) from groups;

-- name: CountParticipants :one
select count(*) from group_participants;

-- name: CountMessages :one
select count(*) from messages;

-- name: CountMediaMessages :one
select count(*) from messages where media_type <> '' or media_path <> '' or media_url <> '';

-- name: GetMessageTimeBounds :one
select
	cast(coalesce(min(case when ts > 0 and ts <= 253402300799 then ts end), 0) as integer) as oldest_ts,
	cast(coalesce(max(case when ts > 0 and ts <= 253402300799 then ts end), 0) as integer) as newest_ts
from messages;

-- name: GetSyncState :one
select value from sync_state where key = sqlc.arg(key);

-- name: ListChats :many
select
	c.jid,
	c.kind,
	coalesce(c.name, '') as name,
	coalesce(c.last_message_at, 0) as last_message_at,
	c.unread_count,
	c.archived,
	c.removed,
	c.hidden,
	c.raw_session_type,
	count(m.rowid) as message_count
from chats c
left join messages m on m.chat_jid = c.jid
group by c.jid, c.kind, c.name, c.last_message_at, c.unread_count, c.archived, c.removed, c.hidden, c.raw_session_type
order by case when c.last_message_at > 0 and c.last_message_at <= 253402300799 then c.last_message_at else 0 end desc
limit sqlc.arg(limit);

-- name: ListUnreadChats :many
select
	c.jid,
	c.kind,
	coalesce(c.name, '') as name,
	coalesce(c.last_message_at, 0) as last_message_at,
	c.unread_count,
	c.archived,
	c.removed,
	c.hidden,
	c.raw_session_type,
	count(m.rowid) as message_count
from chats c
left join messages m on m.chat_jid = c.jid
where c.unread_count > 0
group by c.jid, c.kind, c.name, c.last_message_at, c.unread_count, c.archived, c.removed, c.hidden, c.raw_session_type
order by case when c.last_message_at > 0 and c.last_message_at <= 253402300799 then c.last_message_at else 0 end desc
limit sqlc.arg(limit);

-- name: ExportContacts :many
select
	jid,
	coalesce(phone, '') as phone,
	coalesce(full_name, '') as full_name,
	coalesce(first_name, '') as first_name,
	coalesce(last_name, '') as last_name,
	coalesce(business_name, '') as business_name,
	coalesce(username, '') as username,
	coalesce(lid, '') as lid,
	coalesce(about_text, '') as about_text,
	coalesce(updated_at, 0) as updated_at
from contacts
order by jid;

-- name: ExportChats :many
select
	jid,
	kind,
	coalesce(name, '') as name,
	coalesce(last_message_at, 0) as last_message_at,
	unread_count,
	archived,
	removed,
	hidden,
	raw_session_type
from chats
order by jid;

-- name: ExportGroups :many
select
	jid,
	coalesce(name, '') as name,
	coalesce(owner_jid, '') as owner_jid,
	coalesce(created_at, 0) as created_at
from groups
order by jid;

-- name: ExportParticipants :many
select
	group_jid,
	user_jid,
	coalesce(contact_name, '') as contact_name,
	coalesce(first_name, '') as first_name,
	is_admin,
	is_active
from group_participants
order by group_jid, user_jid;

-- name: DeleteMessagesFTS :exec
delete from messages_fts;

-- name: DeleteMessages :exec
delete from messages;

-- name: DeleteGroupParticipants :exec
delete from group_participants;

-- name: DeleteGroups :exec
delete from groups;

-- name: DeleteChats :exec
delete from chats;

-- name: DeleteContacts :exec
delete from contacts;

-- name: DeleteSyncState :exec
delete from sync_state;

-- name: InsertContact :exec
insert into contacts(jid, phone, full_name, first_name, last_name, business_name, username, lid, about_text, updated_at)
values(sqlc.arg(jid), sqlc.arg(phone), sqlc.arg(full_name), sqlc.arg(first_name), sqlc.arg(last_name), sqlc.arg(business_name), sqlc.arg(username), sqlc.arg(lid), sqlc.arg(about_text), sqlc.arg(updated_at));

-- name: InsertChat :exec
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values(sqlc.arg(jid), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(last_message_at), sqlc.arg(unread_count), sqlc.arg(archived), sqlc.arg(removed), sqlc.arg(hidden), sqlc.arg(raw_session_type));

-- name: InsertGroup :exec
insert into groups(jid, name, owner_jid, created_at)
values(sqlc.arg(jid), sqlc.arg(name), sqlc.arg(owner_jid), sqlc.arg(created_at));

-- name: InsertParticipant :exec
insert into group_participants(group_jid, user_jid, contact_name, first_name, is_admin, is_active)
values(sqlc.arg(group_jid), sqlc.arg(user_jid), sqlc.arg(contact_name), sqlc.arg(first_name), sqlc.arg(is_admin), sqlc.arg(is_active));

-- name: InsertMessage :exec
insert into messages(source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred)
values(sqlc.arg(source_pk), sqlc.arg(chat_jid), sqlc.arg(chat_name), sqlc.arg(msg_id), sqlc.arg(sender_jid), sqlc.arg(sender_name), sqlc.arg(ts), sqlc.arg(from_me), sqlc.arg(text), sqlc.arg(raw_type), sqlc.arg(message_type), sqlc.arg(media_type), sqlc.arg(media_title), sqlc.arg(media_path), sqlc.arg(media_url), sqlc.arg(media_size), sqlc.arg(starred));

-- name: InsertMessageFTS :exec
insert into messages_fts(rowid, text, chat, sender, media)
values((select rowid from messages where source_pk = sqlc.arg(source_pk)), sqlc.arg(text), sqlc.arg(chat), sqlc.arg(sender), sqlc.arg(media));

-- name: InsertSyncState :exec
insert into sync_state(key, value, updated_at)
values(sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at));
