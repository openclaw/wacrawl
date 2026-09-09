package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Only explicit PN/LID contact fields are evidence. The authoritative LID
// field accepts native bare numbers or full JIDs; names, phone fields and
// message overlap never establish links.
func verifiedContactAliases(contacts []Contact) map[string]string {
	edges := map[string]map[string]bool{}
	for _, c := range contacts {
		lid := normalizedContactLID(c.LID)
		if !c.DeletedAt.IsZero() || !contactPN(c.JID) || lid == "" {
			continue
		}
		if edges[c.JID] == nil {
			edges[c.JID] = map[string]bool{}
		}
		if edges[lid] == nil {
			edges[lid] = map[string]bool{}
		}
		edges[c.JID][lid] = true
		edges[lid][c.JID] = true
	}
	aliases := map[string]string{}
	for id, peers := range edges {
		if len(peers) != 1 {
			continue
		}
		for peer := range peers {
			if len(edges[peer]) == 1 {
				aliases[id] = peer
			}
		}
	}
	return aliases
}

func contactPN(id string) bool {
	local, ok := strings.CutSuffix(id, "@s.whatsapp.net")
	return ok && local != "" && !strings.ContainsAny(local, "@ \t\r\n")
}

func contactLID(id string) bool {
	local, ok := strings.CutSuffix(id, "@lid")
	return ok && local != "" && !strings.ContainsAny(local, "@ \t\r\n")
}

func readContactIdentities(ctx context.Context, db sourceReader) ([]Contact, error) {
	rows, err := db.QueryContext(ctx, `select jid,coalesce(lid,''),coalesce(deleted_at,0) from contacts`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		var deleted int64
		if err := rows.Scan(&c.JID, &c.LID, &deleted); err != nil {
			return nil, err
		}
		c.DeletedAt = fromUnix(deleted)
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// Import candidates require an input contact link. Also exclude links that
// would conflict in the resulting canonical contacts, so reads and imports
// agree. Duplicate input JIDs cannot become aliases by last-writer wins.
func prepareContactIdentities(ctx context.Context, tx *sql.Tx, input []Contact) (map[string]string, error) {
	aliases := verifiedContactAliases(input)
	existing, err := readContactIdentities(ctx, tx)
	if err != nil {
		return nil, err
	}
	projected := map[string]Contact{}
	for _, c := range existing {
		projected[c.JID] = c
	}
	for _, c := range input {
		if old, ok := projected[c.JID]; ok {
			if old.LID != "" && c.LID != "" && !sameContactLID(old.LID, c.LID) {
				return nil, fmt.Errorf("contact %q has contradictory archived LID %q and incoming LID %q; import unchanged", c.JID, old.LID, c.LID)
			}
			if c.LID == "" {
				c.LID = old.LID
			}
			if !old.DeletedAt.IsZero() {
				c.DeletedAt = old.DeletedAt
			}
		}
		projected[c.JID] = c
	}
	resulting := make([]Contact, 0, len(projected))
	for _, c := range projected {
		resulting = append(resulting, c)
	}
	consistent := verifiedContactAliases(resulting)
	for id, peer := range aliases {
		if consistent[id] != peer {
			delete(aliases, id)
		}
	}
	return aliases, nil
}

func normalizedContactMessage(m Message, aliases map[string]string) Message {
	if pn := aliases[m.ChatJID]; contactLID(m.ChatJID) && pn != "" {
		m.ChatJID = pn
	}
	if pn := aliases[m.SenderJID]; contactLID(m.SenderJID) && pn != "" {
		m.SenderJID = pn
	}
	return m
}

// Filters require full JIDs: unlike the typed native Contact.LID field, a bare
// query number does not establish whether it belongs to the PN or LID namespace.
func (s *Store) withContactAliases(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if filter.ChatJID == "" && filter.Sender == "" {
		return filter, nil
	}
	contacts, err := readContactIdentities(ctx, s.db)
	if err != nil {
		return filter, err
	}
	aliases := verifiedContactAliases(contacts)
	filter.chatAlias = aliases[filter.ChatJID]
	filter.senderAlias = aliases[filter.Sender]
	return filter, nil
}

func validateContactIdentities(contacts []Contact) error {
	seen := map[string]string{}
	for _, c := range contacts {
		if previous, ok := seen[c.JID]; ok && !sameContactLID(previous, c.LID) {
			return fmt.Errorf("contact %q has contradictory LID values %q and %q in the same import", c.JID, previous, c.LID)
		}
		seen[c.JID] = c.LID
	}
	return nil
}

// Normalize only the authoritative contact LID column, never a phone/name or
// arbitrary message field. Keep the raw Contact.LID untouched for export.
func normalizedContactLID(raw string) string {
	if contactLID(raw) {
		return raw
	}
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return raw + "@lid"
}

func sameContactLID(a, b string) bool {
	if a == b {
		return true
	}
	normalized := normalizedContactLID(a)
	return normalized != "" && normalized == normalizedContactLID(b)
}

// Apply the existing later-message revival rule across a verified PN/LID pair.
// Raw chat rows remain intact; the equivalence affects parent lifecycle only.
func chatParentTombstones(chats []Chat, aliases map[string]string) map[string]Tombstone {
	states := map[string]Chat{}
	for _, chat := range chats {
		states[chat.JID] = chat
	}
	parents := map[string]Tombstone{}
	for _, chat := range chats {
		if chat.DeletedAt.IsZero() {
			continue
		}
		peer := aliases[chat.JID]
		if live, ok := states[peer]; ok && live.DeletedAt.IsZero() && live.LastMessageAt.After(chat.DeletedAt) {
			continue
		}
		for _, id := range []string{chat.JID, peer} {
			if id == "" {
				continue
			}
			if prior, ok := parents[id]; !ok || chat.DeletedAt.Before(prior.DeletedAt) {
				parents[id] = chat.Tombstone
			}
		}
	}
	return parents
}

func storedChatParentTombstones(ctx context.Context, tx *sql.Tx, aliases map[string]string) (map[string]Tombstone, error) {
	rows, err := tx.QueryContext(ctx, `select jid,coalesce(last_message_at,0),coalesce(deleted_at,0),coalesce(deletion_source,''),coalesce(deletion_reason,''),last_seen_at from chats`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var chats []Chat
	for rows.Next() {
		var c Chat
		var last, deleted, seen int64
		if err := rows.Scan(&c.JID, &last, &deleted, &c.DeletionSource, &c.DeletionReason, &seen); err != nil {
			return nil, err
		}
		c.LastMessageAt = fromUnix(last)
		c.DeletedAt = fromUnix(deleted)
		c.LastSeenAt = fromUnix(seen)
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chatParentTombstones(chats, aliases), nil
}
