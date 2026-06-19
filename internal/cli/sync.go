package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/cache"
	"github.com/openclaw/wacrawl/internal/store"
	"github.com/openclaw/wacrawl/internal/whatsappdb"
)

type archiveSyncMode string

const (
	archiveSyncAuto   archiveSyncMode = "auto"
	archiveSyncAlways archiveSyncMode = "always"
	archiveSyncNever  archiveSyncMode = "never"
)

func parseArchiveSyncMode(value string) (archiveSyncMode, error) {
	switch archiveSyncMode(strings.TrimSpace(value)) {
	case archiveSyncAuto:
		return archiveSyncAuto, nil
	case archiveSyncAlways:
		return archiveSyncAlways, nil
	case archiveSyncNever:
		return archiveSyncNever, nil
	default:
		return "", fmt.Errorf("--sync must be one of auto, always, never")
	}
}

func (a *app) syncArchive(ctx context.Context, st *store.Store) error {
	if a.syncMode == archiveSyncNever {
		return nil
	}

	status, err := st.Status(ctx)
	if err != nil {
		return err
	}
	if a.syncMode == archiveSyncAuto && !archiveNeedsSyncCheck(status, a.syncMaxAge) {
		return nil
	}

	source, err := whatsappdb.Discover(ctx, a.source)
	if err != nil {
		if a.syncMode == archiveSyncAuto && status.Messages > 0 {
			a.warnSync("source check failed; using existing archive: %v", err)
			return nil
		}
		return err
	}
	if !source.Available {
		if a.syncMode == archiveSyncAlways {
			return fmt.Errorf("WhatsApp Desktop source unavailable: %s", source.Path)
		}
		if status.Messages > 0 {
			a.warnSync("WhatsApp Desktop source unavailable; using existing archive")
		}
		return nil
	}
	if a.syncMode == archiveSyncAuto && !sourceAheadOfArchive(source, status) {
		return nil
	}

	a.warnSync("syncing WhatsApp Desktop snapshot")
	_, err = whatsappdb.Import(ctx, st, a.source)
	return err
}

func archiveNeedsSyncCheck(status store.Status, maxAge time.Duration) bool {
	if status.LastImportAt.IsZero() {
		return true
	}
	if maxAge < 0 {
		return false
	}
	return time.Since(status.LastImportAt) >= maxAge
}

func sourceAheadOfArchive(source whatsappdb.Source, status store.Status) bool {
	if status.LastImportAt.IsZero() || status.Messages == 0 {
		return true
	}
	if source.MessageRows != 0 && source.MessageRows != status.Messages {
		return true
	}
	if source.ContactRows != 0 && source.ContactRows != status.Contacts {
		return true
	}
	if cache.SQLiteModifiedAfter(source.ContactsDB, status.LastImportAt) {
		return true
	}
	if strings.TrimSpace(source.NewestMessage) == "" {
		return false
	}
	sourceNewest, err := time.Parse(time.RFC3339, source.NewestMessage)
	if err != nil {
		return false
	}
	return sourceNewest.After(status.NewestMessage)
}

func (a *app) warnSync(format string, args ...any) {
	if a.stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(a.stderr, "sync: "+format+"\n", args...)
}
