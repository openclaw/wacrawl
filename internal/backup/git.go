package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/crawlkit/mirror"
)

func mirrorOptions(cfg Config) mirror.Options {
	return mirror.Options{RepoPath: cfg.Repo, Remote: cfg.Remote, Branch: "main"}
}

func syncOptions(cfg Config) mirror.Options {
	opts := mirrorOptions(cfg)
	if _, err := os.Stat(filepath.Join(cfg.Repo, ".git")); err == nil {
		opts.Remote = ""
	}
	return opts
}

func ensureRepo(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	opts := syncOptions(cfg)
	err := mirror.SyncCurrentForWrite(ctx, opts)
	if err == nil || opts.Remote == "" {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(cfg.Repo, ".git")); statErr == nil {
		return err
	}
	local := opts
	local.Remote = ""
	if initErr := mirror.EnsureRepo(ctx, local); initErr != nil {
		return fmt.Errorf("initialize backup repo after clone failed: %w", initErr)
	}
	if remoteErr := mirror.EnsureRemote(ctx, opts); remoteErr != nil {
		return fmt.Errorf("configure backup remote after clone failed: %w", remoteErr)
	}
	return nil
}

func ensureRepoForRead(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	return mirror.Fetch(ctx, syncOptions(cfg))
}

func commitAndPush(ctx context.Context, cfg Config, message string, push bool) (bool, error) {
	changed, err := mirror.Commit(ctx, mirrorOptions(cfg), message)
	if err != nil || !push || !changed {
		return changed, err
	}
	return true, mirror.PushAtomic(ctx, mirrorOptions(cfg), "HEAD")
}
