package store

import (
	"time"
)

type ImportStats struct {
	Mode                string    `json:"mode"`
	SourceIdentity      string    `json:"-"`
	SourceStoreIdentity string    `json:"-"`
	AccountIdentity     string    `json:"-"`
	LegacyAccountIDs    []string  `json:"-"`
	AdoptSource         bool      `json:"-"`
	SourceSnapshotAt    time.Time `json:"-"`
	SourceNewestMessage time.Time `json:"-"`
	SourcePath          string    `json:"source_path"`
	DBPath              string    `json:"db_path"`
	Chats               int       `json:"chats"`
	Contacts            int       `json:"contacts"`
	Groups              int       `json:"groups"`
	Participants        int       `json:"participants"`
	Messages            int       `json:"messages"`
	MediaMessages       int       `json:"media_messages"`
	MediaCopied         int       `json:"media_copied,omitempty"`
	MediaMissing        int       `json:"media_missing,omitempty"`
	MediaRoot           string    `json:"-"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
}

type Status struct {
	DBPath              string    `json:"db_path"`
	Chats               int       `json:"chats"`
	UnreadChats         int       `json:"unread_chats"`
	UnreadMessages      int       `json:"unread_messages"`
	Contacts            int       `json:"contacts"`
	Groups              int       `json:"groups"`
	Participants        int       `json:"participants"`
	Messages            int       `json:"messages"`
	MediaMessages       int       `json:"media_messages"`
	DeletedChats        int       `json:"deleted_chats"`
	DeletedContacts     int       `json:"deleted_contacts"`
	DeletedGroups       int       `json:"deleted_groups"`
	DeletedParticipants int       `json:"deleted_participants"`
	DeletedMessages     int       `json:"deleted_messages"`
	MessageRevisions    int       `json:"message_revisions"`
	OldestMessage       time.Time `json:"oldest_message,omitzero"`
	NewestMessage       time.Time `json:"newest_message,omitzero"`
	LastImportAt        time.Time `json:"last_import_at,omitzero"`
	LastSourceSnapshot  time.Time `json:"-"`
	LastSource          string    `json:"last_source,omitempty"`
	SourceRoot          string    `json:"-"`
	LastSourceMessages  int       `json:"last_source_messages,omitempty"`
	LastSourceContacts  int       `json:"last_source_contacts,omitempty"`
	SourceMessagesKnown bool      `json:"-"`
	SourceContactsKnown bool      `json:"-"`
	LastSourceNewest    time.Time `json:"-"`
	NewestObserved      time.Time `json:"-"`
}

type Tombstone struct {
	DeletedAt      time.Time `json:"deleted_at,omitzero"`
	DeletionSource string    `json:"deletion_source,omitempty"`
	DeletionReason string    `json:"deletion_reason,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitzero"`
}

type Chat struct {
	Tombstone
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
	Tombstone
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
	Tombstone
	JID       string
	Name      string
	OwnerJID  string
	CreatedAt time.Time
}

type GroupParticipant struct {
	Tombstone
	GroupJID    string
	UserJID     string
	ContactName string
	FirstName   string
	IsAdmin     bool
	IsActive    bool
}

type Message struct {
	Tombstone
	SourcePK       int64     `json:"source_pk"`
	SourceRowPK    int64     `json:"source_row_pk"`
	EventID        string    `json:"event_id"`
	ChatJID        string    `json:"chat_jid"`
	ChatName       string    `json:"chat_name,omitempty"`
	MessageID      string    `json:"message_id"`
	SenderJID      string    `json:"sender_jid,omitempty"`
	SenderName     string    `json:"sender_name,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	FromMe         bool      `json:"from_me"`
	Text           string    `json:"text,omitempty"`
	RawType        int       `json:"raw_type"`
	MessageType    string    `json:"message_type,omitempty"`
	MediaType      string    `json:"media_type,omitempty"`
	MediaTitle     string    `json:"media_title,omitempty"`
	MediaPath      string    `json:"media_path,omitempty"`
	MediaURL       string    `json:"media_url,omitempty"`
	MediaSize      int64     `json:"media_size,omitempty"`
	Starred        bool      `json:"starred,omitempty"`
	Snippet        string    `json:"snippet,omitempty"`
	SourceTextNull bool      `json:"-"`
	// Rejected optional cache paths are not evidence of a cleared source payload.
	SourceMediaPathRejected bool `json:"-"`
	mediaContentChanged     bool
	sourceMapping           *SourceMapping
	sourceChatJID           string
	sourceSenderJID         string
	storedUnix              int64
}

type MessageRevision struct {
	EventID             string    `json:"event_id"`
	PayloadJSON         string    `json:"payload_json"`
	RecordedAt          time.Time `json:"recorded_at"`
	EventSource         string    `json:"event_source"`
	Reason              string    `json:"reason"`
	AccountIdentity     string    `json:"account_identity,omitempty"`
	SourceStoreIdentity string    `json:"source_store_identity,omitempty"`
	SourceRowPK         int64     `json:"source_row_pk,omitempty"`
}

type MessageFilter struct {
	chatAlias   string
	senderAlias string
	Query       string
	ChatJID     string
	Sender      string
	Limit       int
	After       *time.Time
	Before      *time.Time
	// BeforePK tightens Before into a composite cursor: rows must have
	// ts < Before, or ts == Before with source_pk < BeforePK. Without it,
	// paging by timestamp alone can stall when a page boundary lands inside
	// a run of messages that share the same second.
	BeforePK       int64
	FromMe         *bool
	HasMedia       bool
	Asc            bool
	IncludeDeleted bool
	// SnippetStart and SnippetEnd wrap search matches inside snippets.
	// Both default to the CLI-friendly "[" and "]" markers.
	SnippetStart string
	SnippetEnd   string
}
