package store

import (
	"context"
	"fmt"
	"time"

	"github.com/steipete/wacrawl/internal/store/storedb"
)

type SnapshotData struct {
	Contacts     []Contact
	Chats        []Chat
	Groups       []Group
	Participants []GroupParticipant
	Messages     []Message
}

func (d SnapshotData) ImportStats(sourcePath, dbPath string, finishedAt time.Time) ImportStats {
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	mediaMessages := 0
	for _, message := range d.Messages {
		if message.MediaType != "" || message.MediaPath != "" || message.MediaURL != "" {
			mediaMessages++
		}
	}
	return ImportStats{
		SourcePath:    sourcePath,
		DBPath:        dbPath,
		Chats:         len(d.Chats),
		Contacts:      len(d.Contacts),
		Groups:        len(d.Groups),
		Participants:  len(d.Participants),
		Messages:      len(d.Messages),
		MediaMessages: mediaMessages,
		StartedAt:     finishedAt,
		FinishedAt:    finishedAt,
	}
}

func (s *Store) ExportAll(ctx context.Context) (SnapshotData, error) {
	contacts, err := s.exportContacts(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	chats, err := s.exportChats(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	groups, err := s.exportGroups(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	participants, err := s.exportParticipants(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	messages, err := s.Messages(ctx, MessageFilter{Limit: int(^uint(0) >> 1), Asc: true})
	if err != nil {
		return SnapshotData{}, err
	}
	return SnapshotData{Contacts: contacts, Chats: chats, Groups: groups, Participants: participants, Messages: messages}, nil
}

func (s *Store) ImportSnapshot(ctx context.Context, data SnapshotData, sourcePath string, finishedAt time.Time) error {
	return s.ReplaceAll(ctx, data.ImportStats(sourcePath, s.Path(), finishedAt), data.Contacts, data.Chats, data.Groups, data.Participants, data.Messages)
}

func (s *Store) exportContacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.q.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(rows))
	for _, row := range rows {
		out = append(out, contactFromRow(row))
	}
	return out, nil
}

func (s *Store) exportChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.q.ExportChats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(rows))
	for _, row := range rows {
		out = append(out, exportChatFromRow(row))
	}
	return out, nil
}

func (s *Store) exportGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.q.ExportGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, groupFromRow(row))
	}
	return out, nil
}

func (s *Store) exportParticipants(ctx context.Context) ([]GroupParticipant, error) {
	rows, err := s.q.ExportParticipants(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GroupParticipant, 0, len(rows))
	for _, row := range rows {
		out = append(out, participantFromRow(row))
	}
	return out, nil
}

func (d SnapshotData) Validate() error {
	seen := map[int64]struct{}{}
	for _, message := range d.Messages {
		if message.SourcePK == 0 {
			return fmt.Errorf("message with empty source_pk")
		}
		if _, ok := seen[message.SourcePK]; ok {
			return fmt.Errorf("duplicate message source_pk %d", message.SourcePK)
		}
		seen[message.SourcePK] = struct{}{}
	}
	return nil
}

func contactFromRow(row storedb.ExportContactsRow) Contact {
	return Contact{
		JID:          row.Jid,
		Phone:        row.Phone,
		FullName:     row.FullName,
		FirstName:    row.FirstName,
		LastName:     row.LastName,
		BusinessName: row.BusinessName,
		Username:     row.Username,
		LID:          row.Lid,
		AboutText:    row.AboutText,
		UpdatedAt:    fromUnix(row.UpdatedAt),
	}
}

func exportChatFromRow(row storedb.ExportChatsRow) Chat {
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
	}
}

func groupFromRow(row storedb.ExportGroupsRow) Group {
	return Group{
		JID:       row.Jid,
		Name:      row.Name,
		OwnerJID:  row.OwnerJid,
		CreatedAt: fromUnix(row.CreatedAt),
	}
}

func participantFromRow(row storedb.ExportParticipantsRow) GroupParticipant {
	return GroupParticipant{
		GroupJID:    row.GroupJid,
		UserJID:     row.UserJid,
		ContactName: row.ContactName,
		FirstName:   row.FirstName,
		IsAdmin:     row.IsAdmin != 0,
		IsActive:    row.IsActive != 0,
	}
}
