-- Compile-time schema for sqlc. Runtime migrations remain authoritative in
-- internal/store/schema.go.

create table chats (
	jid text primary key,
	kind text not null,
	name text,
	last_message_at integer,
	unread_count integer not null default 0,
	archived integer not null default 0,
	removed integer not null default 0,
	hidden integer not null default 0,
	raw_session_type integer not null default 0,
	deleted_at integer,
	deletion_source text,
	deletion_reason text,
	last_seen_at integer not null default 0
);

create table contacts (
	jid text primary key,
	phone text,
	full_name text,
	first_name text,
	last_name text,
	business_name text,
	username text,
	lid text,
	about_text text,
	updated_at integer,
	deleted_at integer,
	deletion_source text,
	deletion_reason text,
	last_seen_at integer not null default 0
);

create table groups (
	jid text primary key,
	name text,
	owner_jid text,
	created_at integer,
	deleted_at integer,
	deletion_source text,
	deletion_reason text,
	last_seen_at integer not null default 0
);

create table group_participants (
	group_jid text not null,
	user_jid text not null,
	contact_name text,
	first_name text,
	is_admin integer not null default 0,
	is_active integer not null default 0,
	deleted_at integer,
	deletion_source text,
	deletion_reason text,
	last_seen_at integer not null default 0,
	primary key (group_jid, user_jid)
);

create table messages (
	rowid integer primary key autoincrement,
	source_pk integer not null unique,
	event_id text not null,
	chat_jid text not null,
	chat_name text,
	msg_id text not null,
	sender_jid text,
	sender_name text,
	ts integer not null,
	from_me integer not null,
	text text,
	raw_type integer not null,
	message_type text,
	media_type text,
	media_title text,
	media_path text,
	media_url text,
	media_size integer,
	starred integer not null default 0,
	deleted_at integer,
	deletion_source text,
	deletion_reason text,
	last_seen_at integer not null default 0
);

create index idx_messages_chat_ts on messages(chat_jid, ts);
create index idx_messages_chat_msg on messages(chat_jid, msg_id);
create index idx_messages_ts on messages(ts);
create index idx_messages_sender on messages(sender_jid);

-- sqlc does not need FTS behavior here; it only validates archive-store writes.
-- Runtime schema.go creates the real fts5 virtual table.
create table messages_fts (
	rowid integer primary key,
	text text,
	chat text,
	sender text,
	media text
);

create table message_revisions (
	id integer primary key autoincrement,
	event_id text not null,
	payload_json text not null,
	recorded_at integer not null,
	event_source text not null,
	reason text not null
);

create index idx_message_revisions_event on message_revisions(event_id, recorded_at desc, id desc);

create table sync_state (
	key text primary key,
	value text not null,
	updated_at integer not null
);
