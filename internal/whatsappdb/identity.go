package whatsappdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

func readSourceIdentity(ctx context.Context, chatDBPath string) (string, error) {
	db, closeDB, err := openReadOnly(chatDBPath)
	if err != nil {
		return "", err
	}
	defer closeDB()
	var metadataTables int
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name='Z_METADATA'`).Scan(&metadataTables); err != nil {
		return "", err
	}
	if metadataTables == 0 {
		return "", nil
	}
	var storeUUID string
	if err := db.QueryRowContext(ctx, `select coalesce(Z_UUID,'') from Z_METADATA limit 1`).Scan(&storeUUID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("read WhatsApp store identity: %w", err)
	}
	storeUUID = strings.TrimSpace(storeUUID)
	if storeUUID == "" {
		return "", nil
	}
	fingerprint := sha256.Sum256([]byte("chat-store\x00" + storeUUID))
	return fmt.Sprintf("wa-store:%x", fingerprint), nil
}

func readAccountIdentity(ctx context.Context, axolotlDBPath, chatDBPath string) (string, []string, error) {
	var metadataIDs map[string]struct{}
	legacyIDs := make(map[string]struct{})
	if _, err := os.Stat(axolotlDBPath); err == nil {
		db, closeDB, err := openReadOnly(axolotlDBPath)
		if err != nil {
			return "", nil, err
		}
		defer closeDB()

		metadataIDs, err = accountIDs(ctx, db, "ZWAZMDACCOUNT", "ZUSERJIDSTRING")
		if err != nil {
			return "", nil, err
		}
		if len(metadataIDs) == 0 {
			metadataIDs, err = accountIDs(ctx, db, "ZWAZMDACCOUNT", "ZACCOUNTJIDSTRING")
			if err != nil {
				return "", nil, err
			}
		}
		for _, table := range []string{"ZWAAXOLOTLIDENTITY", "ZWAAXOLOTLSESSION", "ZWASENDERKEY"} {
			ids, err := accountIDs(ctx, db, table, "ZACCOUNTJIDSTRING")
			if err != nil {
				return "", nil, err
			}
			for id := range ids {
				legacyIDs[id] = struct{}{}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}

	messageIDs, err := incomingMessageAccountIDs(ctx, chatDBPath)
	if err != nil {
		return "", nil, err
	}
	metadataIdentity, err := accountFingerprint(metadataIDs)
	if err != nil {
		return "", nil, err
	}
	messageIdentity, err := accountFingerprint(messageIDs)
	if err != nil {
		return "", nil, err
	}
	if metadataIdentity != "" && messageIdentity != "" && metadataIdentity != messageIdentity {
		return "", nil, errors.New("WhatsApp account metadata does not match the imported message store")
	}
	if metadataIdentity != "" {
		return metadataIdentity, accountFingerprints(legacyIDs), nil
	}
	return messageIdentity, accountFingerprints(legacyIDs), nil
}

func accountFingerprints(ids map[string]struct{}) []string {
	fingerprints := make([]string, 0, len(ids))
	for id := range ids {
		fingerprint := sha256.Sum256([]byte("account-jid\x00" + id))
		fingerprints = append(fingerprints, fmt.Sprintf("wa-account:%x", fingerprint))
	}
	slices.Sort(fingerprints)
	return fingerprints
}

func sharedIdentities(current, before []string) []string {
	beforeSet := make(map[string]struct{}, len(before))
	for _, identity := range before {
		beforeSet[identity] = struct{}{}
	}
	shared := make([]string, 0, len(current))
	for _, identity := range current {
		if _, ok := beforeSet[identity]; ok {
			shared = append(shared, identity)
		}
	}
	return shared
}

func incomingMessageAccountIDs(ctx context.Context, chatDBPath string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	if _, err := os.Stat(chatDBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ids, nil
		}
		return nil, err
	}
	db, closeDB, err := openReadOnly(chatDBPath)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	for _, column := range []string{"ZISFROMME", "ZTOJID"} {
		var present int
		if err := db.QueryRowContext(ctx, `select count(*) from pragma_table_info('ZWAMESSAGE') where name=?`, column).Scan(&present); err != nil {
			return nil, err
		}
		if present == 0 {
			return ids, nil
		}
	}
	if err := collectAccountIDs(ctx, db, `
select coalesce(ZTOJID,'')
from ZWAMESSAGE
where coalesce(ZISFROMME,0)=0
  and trim(coalesce(ZTOJID,''))<>''`, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func accountIDs(ctx context.Context, db *sql.DB, table string, columns ...string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	var tables int
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name=?`, table).Scan(&tables); err != nil {
		return nil, err
	}
	if tables == 0 {
		return ids, nil
	}
	for _, column := range columns {
		var present int
		if err := db.QueryRowContext(ctx, `select count(*) from pragma_table_info(?) where name=?`, table, column).Scan(&present); err != nil {
			return nil, err
		}
		if present == 0 {
			continue
		}
		query := fmt.Sprintf("select coalesce(%s,'') from %s", column, table) //nolint:gosec // names come from fixed internal lists.
		if err := collectAccountIDs(ctx, db, query, ids); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func collectAccountIDs(ctx context.Context, db *sql.DB, query string, ids map[string]struct{}) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	return rows.Err()
}

func accountFingerprint(ids map[string]struct{}) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	if len(ids) != 1 {
		return "", errors.New("source contains multiple WhatsApp account identities and cannot be imported safely")
	}
	var id string
	for id = range ids {
	}
	fingerprint := sha256.Sum256([]byte("account-jid\x00" + id))
	return fmt.Sprintf("wa-account:%x", fingerprint), nil
}
