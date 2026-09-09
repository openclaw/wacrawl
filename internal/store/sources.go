package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SourceMapping binds a raw row incarnation to a stable archive event. A row
// number alone is never an identity, even within one Desktop store.
type SourceMapping struct {
	AccountIdentity     string `json:"account_identity"`
	SourceStoreIdentity string `json:"source_store_identity"`
	SourceRowPK         int64  `json:"source_row_pk"`
	Discriminator       string `json:"discriminator"`
	EventID             string `json:"event_id"`
	MatchKind           string `json:"match_kind"`
}

// SourceObservation retains each distinct source payload once, including local
// names/paths. These are evidence, not canonical message edits.
type SourceObservation struct {
	SourceMapping
	PayloadJSON string    `json:"payload_json"`
	RecordedAt  time.Time `json:"recorded_at"`
}

func sourceState(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `select value from sync_state where key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(value), err
}

func migrateSources(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, sourceSchemaSQL); err != nil {
		return err
	}
	for _, col := range []struct{ name, def string }{
		{"account_identity", "text not null default ''"},
		{"source_store_identity", "text not null default ''"},
		{"source_row_pk", "integer not null default 0"},
	} {
		if err := ensureColumn(ctx, tx, "message_revisions", col.name, col.def); err != nil {
			return err
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `pragma user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= 4 {
		return nil
	}
	account, err := sourceState(ctx, tx, "merge_account_identity")
	if err != nil {
		return err
	}
	source, err := sourceState(ctx, tx, "merge_source_store_identity")
	if err != nil {
		return err
	}
	if source == "" {
		candidate, err := sourceState(ctx, tx, "merge_source_path")
		if err != nil {
			return err
		}
		if strings.HasPrefix(candidate, "wa-store:") {
			source = candidate
		}
	}
	if err := backfillSources(ctx, tx, account, source); err != nil {
		return err
	}
	if account != "" && source != "" && !strings.HasPrefix(account, "wa-store:") {
		_, err = tx.ExecContext(ctx, `update message_revisions set account_identity=?, source_store_identity=?, source_row_pk=coalesce((select source_row_pk from messages where messages.event_id=message_revisions.event_id),0) where account_identity=''`, account, source)
	}
	return err
}

func txMessages(ctx context.Context, tx *sql.Tx) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `select `+messageSelectColumns+` from messages`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func backfillSources(ctx context.Context, tx *sql.Tx, account, source string) error {
	if account == "" || source == "" || strings.HasPrefix(account, "wa-store:") {
		return nil
	}
	messages, err := txMessages(ctx, tx)
	if err != nil {
		return err
	}
	for _, m := range messages {
		key, _ := eventDiscriminator(m)
		m.sourceMapping = &SourceMapping{account, source, m.SourceRowPK, key, m.EventID, "legacy"}
		if err := recordSource(ctx, tx, m, m.LastSeenAt); err != nil {
			return err
		}
	}
	return nil
}

// Full identity deliberately excludes text (edits) and mutable presentation.
// For reactions MediaTitle is the target stanza, not a display caption.
func eventDiscriminator(m Message) (string, bool) {
	target := ""
	if m.RawType == whatsappReactionRawType {
		target = m.MediaTitle
	}
	data, _ := json.Marshal([]any{m.ChatJID, m.MessageID, m.SenderJID, messageUnix(m), m.FromMe, m.RawType, target})
	complete := m.ChatJID != "" && m.MessageID != "" && m.SenderJID != "" && validUnixTimestamp(messageUnix(m)) && messageUnix(m) != 0 && (m.RawType != whatsappReactionRawType || target != "")
	return string(data), complete
}

func mappingKey(m SourceMapping) string {
	data, _ := json.Marshal([]any{m.AccountIdentity, m.SourceStoreIdentity, m.SourceRowPK, m.Discriminator})
	return string(data)
}

func resolveImportMessages(ctx context.Context, tx *sql.Tx, restore bool, stats ImportStats, input []Message, aliases map[string]string) ([]Message, error) {
	if restore || stats.SourceStoreIdentity == "" || stats.AccountIdentity == "" {
		resolved, err := resolveReusedReactionIdentities(ctx, tx, restore, input)
		if err != nil {
			return nil, err
		}
		if !restore {
			for _, m := range resolved {
				old, found, err := messageBySourcePK(ctx, tx, m.SourcePK)
				if err != nil {
					return nil, err
				}
				if found && messageIdentityConflict(old, m) {
					return nil, fmt.Errorf("message source_pk %d belongs to a different event", m.SourcePK)
				}
			}
		}
		return resolved, nil
	}
	archived, err := txMessages(ctx, tx)
	if err != nil {
		return nil, err
	}
	mappings, err := readSources(ctx, tx)
	if err != nil {
		return nil, err
	}
	byKey := map[string]SourceMapping{}
	byRow := map[int64][]SourceMapping{}
	byEvent := map[string]Message{}
	candidates := map[string][]Message{}
	occupied := map[int64]bool{}
	storeEvents := map[string]bool{}
	for _, m := range archived {
		byEvent[m.EventID] = m
		occupied[m.SourcePK] = true
		key, complete := eventDiscriminator(normalizedContactMessage(m, aliases))
		if complete {
			candidates[key] = append(candidates[key], m)
		}
	}
	for _, m := range mappings {
		byKey[mappingKey(m)] = m
		if m.AccountIdentity == stats.AccountIdentity && m.SourceStoreIdentity == stats.SourceStoreIdentity {
			storeEvents[m.EventID] = true
			byRow[m.SourceRowPK] = append(byRow[m.SourceRowPK], m)
		}
	}
	incomingCounts := map[string]int{}
	for _, m := range input {
		key, _ := eventDiscriminator(normalizedContactMessage(m, aliases))
		incomingCounts[key]++
	}
	resolved := append([]Message(nil), input...)
	// Legacy archives without a store fingerprint may only adopt raw bindings
	// while establishing their first fingerprint. Never infer a previous store.
	oldStore, err := sourceState(ctx, tx, "merge_source_store_identity")
	if err != nil {
		return nil, err
	}
	for i := range resolved {
		m := &resolved[i]
		if m.SourceRowPK == 0 {
			m.SourceRowPK = m.SourcePK
		}
		m.sourceChatJID, m.sourceSenderJID = m.ChatJID, m.SenderJID
		rawKey, _ := eventDiscriminator(*m)
		key, complete := eventDiscriminator(normalizedContactMessage(*m, aliases))
		mapping := SourceMapping{stats.AccountIdentity, stats.SourceStoreIdentity, m.SourceRowPK, rawKey, "", "separate"}
		if known, ok := byKey[mappingKey(mapping)]; ok {
			canonical, ok := byEvent[known.EventID]
			if !ok {
				return nil, errors.New("source mapping references missing event")
			}
			m.SourcePK = canonical.SourcePK
			m.EventID = canonical.EventID
			m.ChatJID, m.SenderJID = canonical.ChatJID, canonical.SenderJID
			mapping = known
		} else {
			var match *Message
			// Within a known source row, ordinary attachment type changes and
			// cleared payloads retain the established event. Reactions remain
			// separate incarnations; cross-store matching still uses the full tuple.
			for _, prior := range byRow[m.SourceRowPK] {
				if sameSourceRowEvent(prior.Discriminator, rawKey) {
					candidate := byEvent[prior.EventID]
					if match != nil && match.EventID != candidate.EventID {
						match = nil
						break
					}
					match = &candidate
					mapping.MatchKind = "source-row"
				}
			}
			if match == nil && complete && incomingCounts[key] == 1 && len(candidates[key]) == 1 && !storeEvents[candidates[key][0].EventID] {
				match = &candidates[key][0]
				mapping.MatchKind = "unique"
			}
			if oldStore == "" && match == nil {
				for j := range archived {
					old := &archived[j]
					oldKey, _ := eventDiscriminator(*old)
					if old.SourceRowPK == m.SourceRowPK && oldKey == rawKey && !storeEvents[old.EventID] {
						match = old
						mapping.MatchKind = "legacy"
						break
					}
				}
			}
			switch {
			case match != nil:
				m.SourcePK = match.SourcePK
				m.EventID = match.EventID
				if m.ChatJID != match.ChatJID || m.SenderJID != match.SenderJID {
					mapping.MatchKind = "unique-contact"
				}
				m.ChatJID, m.SenderJID = match.ChatJID, match.SenderJID
			case len(archived) == 0 && !occupied[m.SourcePK]:
				// Retain first-import reader keys; all later stores allocate archive keys.
				m.EventID = messageEventID(m.SourcePK)
				mapping.MatchKind = "initial"
			default:
				digest := sha256.Sum256([]byte(mappingKey(mapping)))
				m.EventID = fmt.Sprintf("wa-event:%x", digest[:])
				m.SourcePK = syntheticMessagePKBoundary | int64(binary.BigEndian.Uint64(digest[:8])&uint64(syntheticMessagePKBoundary-1))
				for occupied[m.SourcePK] {
					m.SourcePK++
					if m.SourcePK >= 2*syntheticMessagePKBoundary {
						m.SourcePK = syntheticMessagePKBoundary
					}
				}
			}
			mapping.EventID = m.EventID
		}
		occupied[m.SourcePK] = true
		storeEvents[m.EventID] = true
		m.sourceMapping = &mapping
	}
	return resolved, validateImportMessages(resolved)
}

// Only the mutable non-reaction type is ignored for an already mapped row.
func sameSourceRowEvent(a, b string) bool {
	var left, right [7]json.RawMessage
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return false
	}
	if string(left[5]) == "14" || string(right[5]) == "14" {
		return false
	}
	for i := 0; i < 5; i++ {
		if string(left[i]) != string(right[i]) {
			return false
		}
	}
	return true
}

func sourceAccount(m Message) string {
	if m.sourceMapping != nil {
		return m.sourceMapping.AccountIdentity
	}
	return ""
}

func sourceStore(m Message) string {
	if m.sourceMapping != nil {
		return m.sourceMapping.SourceStoreIdentity
	}
	return ""
}

func observableMessageJSON(m Message) (string, error) {
	m.SourcePK = 0
	m.SourceRowPK = 0
	m.ChatName = ""
	m.SenderName = ""
	m.MediaPath = ""
	return canonicalMessageJSON(m)
}

func recordSource(ctx context.Context, tx *sql.Tx, m Message, now time.Time) error {
	if m.sourceMapping == nil {
		return nil
	}
	mapping := *m.sourceMapping
	// The canonical keys in this payload identify the event; raw provenance is
	// explicit on the observation, including whether Desktop cleared the text.
	if m.sourceChatJID != "" {
		m.ChatJID = m.sourceChatJID
	}
	if m.sourceSenderJID != "" {
		m.SenderJID = m.sourceSenderJID
	}
	payload, err := canonicalMessageJSON(m)
	if err != nil {
		return err
	}
	payload = fmt.Sprintf(`{"message":%s,"source_text_null":%t}`, payload, m.SourceTextNull)
	return insertObservation(ctx, tx, SourceObservation{mapping, payload, now})
}

func insertMapping(ctx context.Context, tx *sql.Tx, m SourceMapping) error {
	_, err := tx.ExecContext(ctx, `insert into message_sources(account_identity,source_store_identity,source_row_pk,discriminator,event_id,match_kind) values(?,?,?,?,?,?) on conflict do nothing`, m.AccountIdentity, m.SourceStoreIdentity, m.SourceRowPK, m.Discriminator, m.EventID, m.MatchKind)
	return err
}

func insertObservation(ctx context.Context, tx *sql.Tx, o SourceObservation) error {
	if err := insertMapping(ctx, tx, o.SourceMapping); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(o.PayloadJSON))
	_, err := tx.ExecContext(ctx, `insert into source_observations(account_identity,source_store_identity,source_row_pk,discriminator,payload_hash,payload_json,recorded_at) values(?,?,?,?,?,?,?) on conflict do nothing`, o.AccountIdentity, o.SourceStoreIdentity, o.SourceRowPK, o.Discriminator, fmt.Sprintf("%x", digest), o.PayloadJSON, unix(o.RecordedAt))
	return err
}

type sourceReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readSources(ctx context.Context, db sourceReader) ([]SourceMapping, error) {
	rows, err := db.QueryContext(ctx, `select account_identity,source_store_identity,source_row_pk,discriminator,event_id,match_kind from message_sources order by account_identity,source_store_identity,source_row_pk,discriminator`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SourceMapping
	for rows.Next() {
		var m SourceMapping
		if err := rows.Scan(&m.AccountIdentity, &m.SourceStoreIdentity, &m.SourceRowPK, &m.Discriminator, &m.EventID, &m.MatchKind); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func readObservations(ctx context.Context, db sourceReader) ([]SourceObservation, error) {
	rows, err := db.QueryContext(ctx, `select s.account_identity,s.source_store_identity,s.source_row_pk,s.discriminator,s.event_id,s.match_kind,o.payload_json,o.recorded_at from source_observations o join message_sources s using(account_identity,source_store_identity,source_row_pk,discriminator) order by s.account_identity,s.source_store_identity,s.source_row_pk,s.discriminator,o.payload_hash`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SourceObservation
	for rows.Next() {
		var o SourceObservation
		var at int64
		if err := rows.Scan(&o.AccountIdentity, &o.SourceStoreIdentity, &o.SourceRowPK, &o.Discriminator, &o.EventID, &o.MatchKind, &o.PayloadJSON, &at); err != nil {
			return nil, err
		}
		o.RecordedAt = fromUnix(at)
		out = append(out, o)
	}
	return out, rows.Err()
}

func restoreSources(ctx context.Context, tx *sql.Tx, data SnapshotData) error {
	if len(data.Sources) == 0 {
		return backfillSources(ctx, tx, data.AccountIdentity, data.SourceStoreIdentity)
	}
	for _, m := range data.Sources {
		if err := insertMapping(ctx, tx, m); err != nil {
			return err
		}
	}
	for _, o := range data.Observations {
		if err := insertObservation(ctx, tx, o); err != nil {
			return err
		}
	}
	return nil
}

func revisionSourceRow(m Message) int64 {
	if m.sourceMapping != nil {
		return m.sourceMapping.SourceRowPK
	}
	return m.SourceRowPK
}

// Called only after source validation authorizes the existing same-store legacy
// fingerprint normalization. Deferred FKs keep observations and mappings atomic.
func normalizeSourceAccount(ctx context.Context, tx *sql.Tx, account string) error {
	old, err := sourceState(ctx, tx, "merge_account_identity")
	if err != nil {
		return err
	}
	if old == "" || old == account || strings.HasPrefix(old, "wa-store:") {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `pragma defer_foreign_keys=on`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update source_observations set account_identity=? where account_identity=?`, account, old); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `update message_sources set account_identity=? where account_identity=?`, account, old); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update message_revisions set account_identity=? where account_identity=?`, account, old)
	return err
}
