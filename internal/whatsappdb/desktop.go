package whatsappdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/mediafile"
	"github.com/openclaw/wacrawl/internal/store"
)

type Data struct {
	Contacts            []store.Contact
	Chats               []store.Chat
	Groups              []store.Group
	Participants        []store.GroupParticipant
	Messages            []store.Message
	MediaCount          int
	SourceStoreIdentity string
	AccountIdentity     string
	LegacyAccountIDs    []string
}

type ImportOptions struct {
	SourcePath  string
	CopyMedia   bool
	MediaRoot   string
	Restore     bool
	AdoptSource bool
}

func Extract(ctx context.Context, snap Snapshot) (Data, error) {
	contacts, names, err := readContacts(ctx, filepath.Join(snap.Root, contactsDBName))
	if err != nil {
		return Data{}, err
	}
	data, err := readChats(ctx, filepath.Join(snap.Root, chatDBName), snap.SourcePath, names)
	if err != nil {
		return Data{}, err
	}
	sourceStoreIdentity, err := readSourceIdentity(ctx, filepath.Join(snap.Root, chatDBName))
	if err != nil {
		return Data{}, err
	}
	chatDBPath := filepath.Join(snap.Root, chatDBName)
	accountIdentity, legacyAccountIDs, err := readAccountIdentity(ctx, filepath.Join(snap.Root, axolotlDBName), chatDBPath)
	if err != nil {
		return Data{}, err
	}
	accountIdentityBefore, legacyAccountIDsBefore, err := readAccountIdentity(ctx, filepath.Join(snap.Root, axolotlBeforeDBName), chatDBPath)
	if err != nil {
		return Data{}, err
	}
	if accountIdentity != accountIdentityBefore {
		return Data{}, errors.New("WhatsApp account changed while the Desktop snapshot was being captured; retry the import")
	}
	legacyAccountIDs = sharedIdentities(legacyAccountIDs, legacyAccountIDsBefore)
	data.Contacts = contacts
	data.SourceStoreIdentity = sourceStoreIdentity
	data.AccountIdentity = accountIdentity
	data.LegacyAccountIDs = legacyAccountIDs
	return data, nil
}

func Import(ctx context.Context, st *store.Store, path string) (store.ImportStats, error) {
	return ImportWithOptions(ctx, st, ImportOptions{SourcePath: path})
}

func ImportWithOptions(ctx context.Context, st *store.Store, opts ImportOptions) (store.ImportStats, error) {
	sourcePath := defaultedPath(opts.SourcePath)
	sourceIdentity, err := canonicalSourcePath(sourcePath)
	if err != nil {
		return store.ImportStats{}, err
	}
	stats := store.ImportStats{SourcePath: sourcePath, SourceIdentity: sourceIdentity, AdoptSource: opts.AdoptSource, DBPath: st.Path(), StartedAt: time.Now().UTC()}
	sourcePath = sourceIdentity
	stats.SourceSnapshotAt = stats.StartedAt
	snap, err := SnapshotPath(sourcePath)
	if err != nil {
		return stats, err
	}
	defer func() { _ = os.RemoveAll(snap.Root) }()
	data, err := Extract(ctx, snap)
	if err != nil {
		return stats, err
	}
	stats.SourceStoreIdentity = data.SourceStoreIdentity
	stats.AccountIdentity = data.AccountIdentity
	stats.LegacyAccountIDs = data.LegacyAccountIDs
	stats.Chats = len(data.Chats)
	stats.Contacts = len(data.Contacts)
	stats.Groups = len(data.Groups)
	stats.Participants = len(data.Participants)
	stats.Messages = len(data.Messages)
	for _, message := range data.Messages {
		if message.Timestamp.After(stats.SourceNewestMessage) {
			stats.SourceNewestMessage = message.Timestamp
		}
	}
	stats.MediaMessages = data.MediaCount
	stats.FinishedAt = time.Now().UTC()
	stats.Mode = "merge"
	if opts.Restore {
		stats.Mode = "restore"
	}
	if err := st.ValidateImport(ctx, stats, data.Messages, opts.Restore, data.Contacts...); err != nil {
		return stats, err
	}
	mediaRoot := opts.MediaRoot
	if strings.TrimSpace(mediaRoot) == "" {
		mediaRoot = filepath.Join(filepath.Dir(st.Path()), "media")
	}
	mediaRoot, err = prepareMediaRoot(sourcePath, mediaRoot)
	if err != nil {
		if opts.CopyMedia {
			return stats, err
		}
		mediaRoot = ""
	}
	archivePath, err := mediafile.Resolve(st.Path())
	if err != nil {
		return stats, err
	}
	if mediaRoot != "" && containsMediaPath(mediaRoot, archivePath) {
		if opts.CopyMedia {
			return stats, errors.New("archive media root overlaps the archive database")
		}
		mediaRoot = ""
	}
	stats.MediaRoot = mediaRoot
	if opts.CopyMedia {
		copied, missing, err := copyArchiveMedia(data.Messages, sourcePath, mediaRoot)
		if err != nil {
			return stats, err
		}
		stats.MediaCopied = copied
		stats.MediaMissing = missing
	}
	importArchive := st.MergeAll
	if opts.Restore {
		importArchive = st.ReplaceAll
	}
	if err := importArchive(ctx, stats, data.Contacts, data.Chats, data.Groups, data.Participants, data.Messages); err != nil {
		return stats, err
	}
	return stats, nil
}
