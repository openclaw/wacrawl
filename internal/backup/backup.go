package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/crawlkit/mirror"
	"github.com/openclaw/wacrawl/internal/store"
)

type Result struct {
	Repo       string `json:"repo"`
	Changed    bool   `json:"changed"`
	Encrypted  bool   `json:"encrypted"`
	Shards     int    `json:"shards"`
	Messages   int    `json:"messages"`
	MediaFiles int    `json:"media_files"`
	Ref        string `json:"ref,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

func Init(ctx context.Context, opts Options) (Config, string, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return Config{}, "", err
	}
	if err := validateWriteLayout(cfg, opts); err != nil {
		return Config{}, "", err
	}
	if err := validateOwnedFiles(cfg, []string{"README.md", "manifest.json"}); err != nil {
		return Config{}, "", err
	}
	recipient, err := EnsureIdentity(cfg.Identity)
	if err != nil {
		return Config{}, "", err
	}
	// Creation exposes filesystem aliases (including case-equivalent names).
	// Recheck before saving config or initializing a publication repository.
	if err := validateWriteLayout(cfg, opts); err != nil {
		return Config{}, "", err
	}
	if len(cfg.Recipients) == 0 {
		cfg.Recipients = []string{recipient}
	}
	if err := SaveConfig(opts.ConfigPath, cfg); err != nil {
		return Config{}, "", err
	}
	if err := validateWriteLayout(cfg, opts); err != nil {
		return Config{}, "", err
	}
	if err := ensureRepoForWrite(ctx, cfg); err != nil {
		return Config{}, "", err
	}
	if err := validateOwnedFiles(cfg, []string{"README.md", "manifest.json"}); err != nil {
		return Config{}, "", err
	}
	if err := writeBackupReadme(cfg.Repo); err != nil {
		return Config{}, "", err
	}
	_, err = commitAndPush(ctx, cfg, "docs: describe encrypted wacrawl backup", opts.Push)
	return cfg, recipient, err
}

func Push(ctx context.Context, st *store.Store, opts Options) (Result, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return Result{}, err
	}
	opts.ArchivePath = st.Path()
	if err := validateWriteLayout(cfg, opts); err != nil {
		return Result{}, err
	}
	if len(cfg.Recipients) == 0 {
		recipient, err := RecipientFromIdentity(cfg.Identity)
		if err != nil {
			return Result{}, err
		}
		cfg.Recipients = []string{recipient}
	}
	if err := ensureRepoForWrite(ctx, cfg); err != nil {
		return Result{}, err
	}
	if err := validateSnapshotTag(ctx, cfg.Repo, opts.Tag); err != nil {
		return Result{}, err
	}
	if err := validateOwnedFiles(cfg, []string{"manifest.json"}); err != nil {
		return Result{}, err
	}
	oldManifest, err := readManifest(cfg.Repo)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	if err == nil && (oldManifest.Format != formatVersion || !oldManifest.Encrypted) {
		return Result{}, errors.New("previous backup is not a supported encrypted snapshot")
	}
	if err := validateSnapshotInputs(ctx, cfg, toCrawlkitManifest(oldManifest)); err != nil {
		return Result{}, err
	}
	if err := writeBackupReadme(cfg.Repo); err != nil {
		return Result{}, err
	}
	data, err := st.ExportAll(ctx)
	if err != nil {
		return Result{}, err
	}
	var files []ckbackup.File
	if !opts.NoMedia {
		files, err = collectBackupMedia(ctx, st.Path(), data.Messages)
		if err != nil {
			return Result{}, err
		}
	}
	manifest, err := writeSnapshot(ctx, cfg, data, files, oldManifest)
	if err != nil {
		return Result{}, err
	}
	pushWithTag := opts.Push && strings.TrimSpace(opts.Tag) != ""
	changed, err := commitAndPush(ctx, cfg, "sync: update encrypted wacrawl backup", opts.Push && !pushWithTag, toCrawlkitManifest(oldManifest), toCrawlkitManifest(manifest))
	if err != nil {
		return Result{}, err
	}
	if pushWithTag {
		if err := verifyPendingHistory(ctx, cfg); err != nil {
			return Result{}, err
		}
	}
	tag, err := tagSnapshot(ctx, cfg, opts.Tag)
	if err != nil {
		return Result{}, err
	}
	if pushWithTag {
		if err := mirror.PushAtomic(ctx, mirrorOptions(cfg), "HEAD", "refs/tags/"+tag); err != nil {
			return Result{}, err
		}
	}
	return Result{Repo: cfg.Repo, Changed: changed, Encrypted: true, Shards: len(manifest.Shards), Messages: manifest.Counts.Messages, MediaFiles: len(manifest.Files), Tag: tag}, nil
}

func Pull(ctx context.Context, st *store.Store, opts Options) (Result, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return Result{}, err
	}
	ensure := ensureRepo
	if strings.TrimSpace(opts.Ref) != "" {
		ensure = ensureRepoForRead
	}
	if err := ensure(ctx, cfg); err != nil {
		return Result{}, err
	}
	manifest, ref, err := readManifestAtRef(ctx, cfg.Repo, opts.Ref)
	if err != nil {
		return Result{}, err
	}
	var data store.SnapshotData
	if ref == "" {
		data, err = readSnapshot(cfg, manifest)
	} else {
		data, err = readSnapshotAtRef(ctx, cfg, manifest, ref)
	}
	if err != nil {
		return Result{}, err
	}
	if err := data.Validate(); err != nil {
		return Result{}, err
	}
	root, err := archiveRoot(st.Path())
	if err != nil {
		return Result{}, err
	}
	stageRoot := ""
	if !opts.NoMedia && len(manifest.Files) > 0 {
		stageRoot, err = os.MkdirTemp(root, ".wacrawl-media-restore-")
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = os.RemoveAll(stageRoot) }()
		if ref == "" {
			_, err = ckbackup.RestoreFilesUnder(ctx, crawlkitConfig(cfg), toCrawlkitManifest(manifest), stageRoot, "media")
		} else {
			_, _, err = ckbackup.RestoreFilesAtUnder(ctx, crawlkitConfig(cfg), mirrorOptions(cfg), toCrawlkitManifest(manifest), ref, stageRoot, "media")
		}
		if err != nil {
			return Result{}, err
		}
	}
	if stageRoot != "" {
		if err := localizeMediaPaths(data.Messages, root); err != nil {
			return Result{}, err
		}
	}
	sourcePath := "backup:" + cfg.Repo
	if ref != "" {
		sourcePath += "@" + ref
	}
	importSnapshot := func() error { return st.ImportSnapshot(ctx, data, sourcePath, manifest.Exported) }
	if stageRoot != "" {
		err = replaceMediaDuring(filepath.Join(stageRoot, "media"), filepath.Join(root, "media"), importSnapshot)
	} else {
		err = importSnapshot()
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Repo: cfg.Repo, Changed: true, Encrypted: manifest.Encrypted, Shards: len(manifest.Shards), Messages: len(data.Messages), MediaFiles: len(manifest.Files), Ref: ref}, nil
}

func Status(ctx context.Context, opts Options) (Manifest, string, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return Manifest{}, "", err
	}
	if err := ensureRepo(ctx, cfg); err != nil {
		return Manifest{}, "", err
	}
	manifest, err := readManifest(cfg.Repo)
	if err != nil {
		return Manifest{}, "", err
	}
	return manifest, cfg.Repo, nil
}
