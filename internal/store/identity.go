package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	whatsappReactionRawType = 14
	// Synthetic keys remain distinct from Core Data row IDs and exact in web JSON.
	syntheticMessagePKBoundary = int64(1 << 52)
)

func validateImportSource(ctx context.Context, tx *sql.Tx, restore bool, stats ImportStats, messages []Message) (string, error) {
	strongSource := strings.TrimSpace(stats.SourceIdentity)
	weakSource := legacySourceIdentity(stats.SourcePath)
	if restore {
		return strongSource, nil
	}
	var existingStrong string
	err := tx.QueryRowContext(ctx, `select value from sync_state where key='merge_source_path'`).Scan(&existingStrong)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var existingStore string
	err = tx.QueryRowContext(ctx, `select value from sync_state where key='merge_source_store_identity'`).Scan(&existingStore)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if strings.HasPrefix(existingStrong, "wa-store:") {
		existingStore = existingStrong
		existingStrong = ""
	}
	var existingWeak string
	if existingStrong == "" {
		err = tx.QueryRowContext(ctx, `select value from sync_state where key='source_path'`).Scan(&existingWeak)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		existingWeak = legacySourceIdentity(existingWeak)
	}
	accountIdentity := strings.TrimSpace(stats.AccountIdentity)
	existingAccount, err := sourceState(ctx, tx, "merge_account_identity")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(existingAccount, "wa-store:") {
		existingAccount = ""
	}
	if existingAccount != "" && existingAccount != accountIdentity {
		legacyMatch := false
		if accountIdentity != "" && existingStore != "" && existingStore == strings.TrimSpace(stats.SourceStoreIdentity) {
			for _, candidate := range stats.LegacyAccountIDs {
				if strings.TrimSpace(candidate) == existingAccount {
					legacyMatch = true
					break
				}
			}
		}
		continuity := false
		if legacyMatch {
			resolved, err := resolveReusedReactionIdentities(ctx, tx, false, messages)
			if err != nil {
				return "", err
			}
			for _, m := range resolved {
				existing, found, err := messageBySourcePK(ctx, tx, m.SourcePK)
				if err != nil {
					return "", err
				}
				if found && !messageIdentityConflict(existing, m) {
					continuity = true
					break
				}
			}
		}
		if !continuity {
			return "", errors.New("archive is bound to a different WhatsApp account; use a separate --db or import --restore")
		}
	}
	entityRows, err := archiveEntityRows(ctx, tx)
	if err != nil {
		return "", err
	}
	if existingAccount == "" && entityRows > 0 && (!stats.AdoptSource || accountIdentity == "") {
		if accountIdentity == "" {
			return "", errors.New("this WhatsApp source exposes no account identity, so --adopt-source cannot bind the archive; read retained history with --sync never, or import into a separate --db")
		}
		return "", errors.New("archive has no verified WhatsApp account binding; rerun an explicit import with --adopt-source, use a separate --db, or import --restore")
	}
	incomingStore := strings.TrimSpace(stats.SourceStoreIdentity)
	sameAccount := existingAccount != "" && existingAccount == accountIdentity
	if existingStore != "" && incomingStore != existingStore && (!sameAccount || incomingStore == "") {
		return "", errors.New("archive is bound to a different WhatsApp Desktop store; use a separate --db or import --restore")
	}
	if strongSource != "" && existingStrong != "" && strongSource != existingStrong {
		return "", fmt.Errorf("archive is bound to WhatsApp source %q, not %q; use a separate --db or import --restore", existingStrong, strongSource)
	}
	if existingStrong == "" && existingWeak != "" && weakSource != existingWeak {
		return "", fmt.Errorf("archive is bound to WhatsApp source path %q, not %q; use a separate --db or import --restore", existingWeak, weakSource)
	}
	if strongSource != "" {
		return strongSource, nil
	}
	return existingStrong, nil
}

func archiveEntityRows(ctx context.Context, tx *sql.Tx) (int, error) {
	var entityRows int
	if err := tx.QueryRowContext(ctx, `select
(select count(*) from contacts)+(select count(*) from chats)+(select count(*) from groups)+
(select count(*) from group_participants)+(select count(*) from messages)`).Scan(&entityRows); err != nil {
		return 0, err
	}
	return entityRows, nil
}

func legacySourceIdentity(source string) string {
	source = strings.TrimSpace(source)
	if source == "" || strings.HasPrefix(source, "backup:") {
		return ""
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return filepath.Clean(source)
	}
	if evaluated, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = evaluated
	}
	return filepath.Clean(absolute)
}

func validateImportMessages(messages []Message) error {
	seen := make(map[int64]struct{}, len(messages))
	seenEvents := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if message.SourcePK == 0 {
			return errors.New("message with empty source_pk")
		}
		if _, ok := seen[message.SourcePK]; ok {
			return fmt.Errorf("duplicate message source_pk %d", message.SourcePK)
		}
		seen[message.SourcePK] = struct{}{}
		eventID := message.EventID
		if eventID == "" {
			eventID = messageEventID(message.SourcePK)
		} else if strings.TrimSpace(eventID) == "" {
			return fmt.Errorf("message source_pk %d has empty event_id", message.SourcePK)
		}
		if _, ok := seenEvents[eventID]; ok {
			return fmt.Errorf("duplicate message event_id %q", eventID)
		}
		seenEvents[eventID] = struct{}{}
	}
	return nil
}

func messageEventID(sourcePK int64) string {
	return fmt.Sprintf("wa:%d", sourcePK)
}

func resolveReusedReactionIdentities(ctx context.Context, tx *sql.Tx, restore bool, messages []Message) ([]Message, error) {
	resolved := append([]Message(nil), messages...)
	for i := range resolved {
		message := &resolved[i]
		if message.SourceRowPK == 0 {
			message.SourceRowPK = message.SourcePK
		}
		if restore {
			continue
		}
		existing, found, err := messageBySourcePK(ctx, tx, message.SourcePK)
		if err != nil {
			return nil, err
		}
		if !found || !reactionReusesSourceRow(existing, *message) {
			continue
		}
		eventID, sourcePK := reusedReactionIdentity(*message)
		collision, collisionFound, err := messageBySourcePK(ctx, tx, sourcePK)
		if err != nil {
			return nil, err
		}
		if collisionFound && collision.EventID != eventID {
			return nil, fmt.Errorf("synthetic reaction source_pk %d belongs to a different event", sourcePK)
		}
		message.SourcePK = sourcePK
		message.EventID = eventID
	}
	if err := validateImportMessages(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

func reactionReusesSourceRow(existing, incoming Message) bool {
	return incoming.RawType == whatsappReactionRawType &&
		(incoming.SourceTextNull || looksLikeActorJID(strings.TrimSpace(incoming.Text))) &&
		incoming.MessageID != "" &&
		incoming.MessageID != existing.MessageID &&
		incoming.MediaTitle == existing.MessageID &&
		incoming.ChatJID == existing.ChatJID
}

func looksLikeActorJID(value string) bool {
	local, found := strings.CutSuffix(value, "@lid")
	if !found {
		local, found = strings.CutSuffix(value, "@s.whatsapp.net")
	}
	if !found || local == "" {
		return false
	}
	for i := range len(local) {
		if local[i] < '0' || local[i] > '9' {
			return false
		}
	}
	return true
}

func reusedReactionIdentity(message Message) (string, int64) {
	seed := fmt.Sprintf("%d\x00%s\x00%s\x00%s", message.SourceRowPK, message.ChatJID, message.MessageID, message.MediaTitle)
	digest := sha256.Sum256([]byte(seed))
	eventID := fmt.Sprintf("wa-reaction:%d:%x", message.SourceRowPK, digest[:16])
	sourcePK := syntheticMessagePKBoundary | int64(binary.BigEndian.Uint64(digest[:8])&uint64(syntheticMessagePKBoundary-1))
	return eventID, sourcePK
}

func messageIdentityConflict(existing, incoming Message) bool {
	return existing.ChatJID != incoming.ChatJID || existing.MessageID != incoming.MessageID || existing.FromMe != incoming.FromMe || messageUnix(existing) != messageUnix(incoming)
}
