package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store/storedb"
)

// Only explicit PN/LID contact fields are evidence. The authoritative LID
// field accepts native bare numbers or full JIDs; names, phone fields and
// message overlap never establish links.
func verifiedContactAliases(contacts []Contact) map[string]string {
	edges := map[string]map[string]bool{}
	live := map[string]bool{}
	for _, c := range contacts {
		if !c.DeletedAt.IsZero() {
			continue
		}
		live[c.JID] = true
	}
	for _, c := range contacts {
		evidence := append([]ContactLIDEvidence(nil), c.LIDEvidence...)
		evidence = append(evidence, ContactLIDEvidence{LID: c.LID})
		for _, observation := range evidence {
			lid := normalizedContactLID(observation.LID)
			if !contactPN(c.JID) || lid == "" {
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
	}
	aliases := map[string]string{}
	for id, peers := range edges {
		if len(peers) != 1 {
			continue
		}
		for peer := range peers {
			if len(edges[peer]) == 1 && (live[id] && contactPN(id) || live[peer] && contactPN(peer)) {
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
	rows, err := db.QueryContext(ctx, `select jid,coalesce(lid,''),coalesce(deleted_at,0),lid_evidence from contacts`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		var deleted int64
		var evidence string
		if err := rows.Scan(&c.JID, &c.LID, &deleted, &evidence); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidence), &c.LIDEvidence); err != nil {
			return nil, fmt.Errorf("contact %q LID evidence: %w", c.JID, err)
		}
		if err := validateLIDEvidence(c.LIDEvidence); err != nil {
			return nil, err
		}
		c.DeletedAt = fromUnix(deleted)
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// Import candidates need a current input link and a consistent retained graph.
func prepareContactIdentities(ctx context.Context, tx *sql.Tx, input []Contact, stats ImportStats, restore, snapshot bool) ([]Contact, map[string]string, error) {
	var existing []Contact
	var err error
	if !restore {
		existing, err = exportContacts(ctx, storedb.New(tx))
		if err != nil {
			return nil, nil, err
		}
	}
	projected := map[string]Contact{}
	for _, c := range existing {
		projected[c.JID] = c
	}
	original := map[string]Contact{}
	for _, c := range existing {
		original[c.JID] = c
	}
	first := map[string]string{}
	conflicting := map[string]bool{}
	for _, c := range input {
		if lid, ok := first[c.JID]; ok && !sameContactLID(lid, c.LID) {
			conflicting[c.JID] = true
		} else if !ok {
			first[c.JID] = c.LID
		}
	}
	current := make([]Contact, 0, len(input))
	for _, c := range input {
		current = append(current, Contact{JID: c.JID, LID: c.LID, Tombstone: c.Tombstone})
		old := projected[c.JID]
		evidence := append([]ContactLIDEvidence(nil), old.LIDEvidence...)
		if old.LID != "" && !hasRawLID(evidence, old.LID) {
			evidence = mergeLIDEvidence(evidence, ContactLIDEvidence{LID: old.LID})
		}
		evidence = mergeLIDEvidence(evidence, c.LIDEvidence...)
		if c.LID != "" {
			observed := ContactLIDEvidence{LID: c.LID}
			// Exact snapshots already carry observations; legacy snapshots have unknown origin.
			if !snapshot {
				observed.SourceStoreIdentity, observed.FirstObservedAt = stats.SourceStoreIdentity, stats.FinishedAt
			}
			if !snapshot || !hasRawLID(evidence, c.LID) {
				evidence = mergeLIDEvidence(evidence, observed)
			}
		}
		c.LIDEvidence = evidence
		if c.LID == "" {
			c.LID = old.LID
		}
		if !old.DeletedAt.IsZero() {
			c.DeletedAt = old.DeletedAt
		}
		projected[c.JID] = c
	}
	for jid := range conflicting {
		evidence := projected[jid].LIDEvidence
		retained := original[jid]
		retained.JID, retained.LIDEvidence = jid, evidence
		projected[jid] = retained
	}
	resulting := make([]Contact, 0, len(projected))
	for _, c := range projected {
		resulting = append(resulting, c)
	}
	consistent := verifiedContactAliases(resulting)
	aliases := verifiedContactAliases(current)
	for id, peer := range aliases {
		if consistent[id] != peer {
			delete(aliases, id)
		}
	}
	resolved := make([]Contact, 0, len(input))
	seen := map[string]bool{}
	for _, c := range input {
		if !seen[c.JID] {
			resolved = append(resolved, projected[c.JID])
			seen[c.JID] = true
		}
	}
	return resolved, aliases, nil
}

// ContactLIDEvidence is a sourced raw field observation. A zero observation time
// or empty store means unknown legacy provenance, not an inferred source event.
type ContactLIDEvidence struct {
	LID                 string    `json:"lid"`
	SourceStoreIdentity string    `json:"source_store_identity,omitempty"`
	FirstObservedAt     time.Time `json:"first_observed_at,omitempty"`
}

func hasRawLID(evidence []ContactLIDEvidence, lid string) bool {
	for _, e := range evidence {
		if e.LID == lid {
			return true
		}
	}
	return false
}

func mergeLIDEvidence(evidence []ContactLIDEvidence, incoming ...ContactLIDEvidence) []ContactLIDEvidence {
	for _, e := range incoming {
		found := false
		for _, old := range evidence {
			if old.LID == e.LID && old.SourceStoreIdentity == e.SourceStoreIdentity {
				found = true
				break
			}
		}
		if !found {
			evidence = append(evidence, e)
		}
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].LID != evidence[j].LID {
			return evidence[i].LID < evidence[j].LID
		}
		return evidence[i].SourceStoreIdentity < evidence[j].SourceStoreIdentity
	})
	return evidence
}

func validateLIDEvidence(evidence []ContactLIDEvidence) error {
	seen := map[[2]string]bool{}
	for _, e := range evidence {
		key := [2]string{e.LID, e.SourceStoreIdentity}
		if e.LID == "" || (e.SourceStoreIdentity != "" && !strings.HasPrefix(e.SourceStoreIdentity, "wa-store:")) || (!e.FirstObservedAt.IsZero() && (!validUnixTimestamp(e.FirstObservedAt.Unix()) || e.FirstObservedAt.Unix() == 0)) {
			return fmt.Errorf("invalid contact LID evidence")
		}
		if seen[key] {
			return fmt.Errorf("duplicate contact LID evidence")
		}
		seen[key] = true
	}
	return nil
}

func migrateContactEvidence(ctx context.Context, tx *sql.Tx) error {
	if err := ensureColumn(ctx, tx, "contacts", "lid_evidence", "text not null default '[]'"); err != nil {
		return err
	}
	contacts, err := readContactIdentities(ctx, tx)
	if err != nil {
		return err
	}
	for _, c := range contacts {
		if c.LID == "" || hasRawLID(c.LIDEvidence, c.LID) {
			continue
		}
		evidence := mergeLIDEvidence(c.LIDEvidence, ContactLIDEvidence{LID: c.LID})
		payload, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update contacts set lid_evidence=? where jid=?`, string(payload), c.JID); err != nil {
			return err
		}
	}
	return nil
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
	for _, c := range contacts {
		if err := validateLIDEvidence(c.LIDEvidence); err != nil {
			return err
		}
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
