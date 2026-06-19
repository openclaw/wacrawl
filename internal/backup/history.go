package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/crawlkit/mirror"
	"github.com/openclaw/wacrawl/internal/store"
)

const defaultSnapshotLimit = ckbackup.DefaultHistoryLimit

type Snapshot struct {
	Ref      string    `json:"ref"`
	Tags     []string  `json:"tags,omitempty"`
	Exported time.Time `json:"exported"`
	Counts   Counts    `json:"counts"`
	Shards   int       `json:"shards"`
}

func Snapshots(ctx context.Context, opts Options) ([]Snapshot, string, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit == 0 {
		limit = defaultSnapshotLimit
	}
	if limit < 1 {
		return nil, "", fmt.Errorf("snapshot limit must be greater than zero")
	}
	history, err := ckbackup.History(ctx, syncOptions(cfg), limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]Snapshot, 0, len(history))
	for _, entry := range history {
		manifest := fromCrawlkitManifest(entry.Manifest)
		out = append(out, Snapshot{Ref: entry.Ref, Tags: entry.Tags, Exported: manifest.Exported, Counts: manifest.Counts, Shards: len(manifest.Shards)})
	}
	return out, cfg.Repo, nil
}

func readManifestAtRef(ctx context.Context, repo, requested string) (Manifest, string, error) {
	manifest, commit, err := ckbackup.ReadManifestAt(ctx, mirror.Options{RepoPath: repo, Branch: "main"}, requested)
	if err != nil {
		return Manifest{}, "", err
	}
	return fromCrawlkitManifest(manifest), commit, nil
}

func readSnapshotAtRef(ctx context.Context, cfg Config, manifest Manifest, commit string) (store.SnapshotData, error) {
	shards, _, err := ckbackup.ReadSnapshotAt(ctx, crawlkitConfig(cfg), mirrorOptions(cfg), toCrawlkitManifest(manifest), commit)
	if err != nil {
		return store.SnapshotData{}, err
	}
	return decodeSnapshot(shards)
}

func validateSnapshotTag(ctx context.Context, repo, requested string) error {
	return mirror.ValidateTag(ctx, mirror.Options{RepoPath: repo, Branch: "main"}, requested)
}

func tagSnapshot(ctx context.Context, cfg Config, requested string) (string, error) {
	return mirror.CreateImmutableTag(ctx, mirrorOptions(cfg), requested)
}

func resolveCommit(ctx context.Context, repo, ref string) (string, error) {
	return mirror.ResolveCommit(ctx, mirror.Options{RepoPath: repo, Branch: "main"}, ref)
}

func decodeManifest(data []byte) (Manifest, error) {
	var manifest ckbackup.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return fromCrawlkitManifest(manifest), nil
}

func shortRef(ref string) string {
	return mirror.ShortRef(strings.TrimSpace(ref))
}
