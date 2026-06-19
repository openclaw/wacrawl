package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
)

const defaultSnapshotLimit = 20

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
	if err := ensureRepoForRead(ctx, cfg); err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit == 0 {
		limit = defaultSnapshotLimit
	}
	if limit < 1 {
		return nil, "", fmt.Errorf("snapshot limit must be greater than zero")
	}
	raw, err := gitOutput(ctx, cfg.Repo, "log", "--all", "--format=%H", "-n", strconv.Itoa(limit), "--", "manifest.json")
	if err != nil {
		return nil, "", err
	}
	refs := strings.Fields(string(raw))
	out := make([]Snapshot, 0, len(refs))
	for _, ref := range refs {
		manifest, err := manifestAtCommit(ctx, cfg.Repo, ref)
		if err != nil {
			return nil, "", err
		}
		tagsRaw, err := gitOutput(ctx, cfg.Repo, "tag", "--points-at", ref)
		if err != nil {
			return nil, "", err
		}
		tags := strings.Fields(string(tagsRaw))
		sort.Strings(tags)
		out = append(out, Snapshot{Ref: ref, Tags: tags, Exported: manifest.Exported, Counts: manifest.Counts, Shards: len(manifest.Shards)})
	}
	return out, cfg.Repo, nil
}

func readManifestAtRef(ctx context.Context, repo, requested string) (Manifest, string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		manifest, err := readManifest(repo)
		return manifest, "", err
	}
	commit, err := resolveCommit(ctx, repo, requested)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("resolve backup ref %q: %w", requested, err)
	}
	manifest, err := manifestAtCommit(ctx, repo, commit)
	if err != nil {
		return Manifest{}, "", err
	}
	return manifest, commit, nil
}

func manifestAtCommit(ctx context.Context, repo, commit string) (Manifest, error) {
	raw, err := gitOutput(ctx, repo, "show", commit+":manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest at %s: %w", shortRef(commit), err)
	}
	return decodeManifest(raw)
}

func readSnapshotAtRef(ctx context.Context, cfg Config, manifest Manifest, commit string) (store.SnapshotData, error) {
	return readSnapshotWith(manifest, func(shard ShardEntry) ([]byte, error) {
		if _, err := resolveShardPath(cfg.Repo, shard.Path); err != nil {
			return nil, err
		}
		clean := path.Clean(strings.TrimSpace(shard.Path))
		ciphertext, err := gitOutput(ctx, cfg.Repo, "show", commit+":"+clean)
		if err != nil {
			return nil, fmt.Errorf("read backup shard %s at %s: %w", clean, shortRef(commit), err)
		}
		return decryptShard(ciphertext, cfg.Identity)
	})
}

func validateSnapshotTag(ctx context.Context, repo, requested string) error {
	tag := strings.TrimSpace(requested)
	if tag == "" {
		return nil
	}
	ref := "refs/tags/" + tag
	if err := git(ctx, repo, "check-ref-format", ref); err != nil {
		return fmt.Errorf("invalid snapshot tag %q: %w", tag, err)
	}
	return nil
}

func tagSnapshot(ctx context.Context, cfg Config, requested string) (string, error) {
	tag := strings.TrimSpace(requested)
	if tag == "" {
		return "", nil
	}
	if err := validateSnapshotTag(ctx, cfg.Repo, tag); err != nil {
		return "", err
	}
	ref := "refs/tags/" + tag
	head, err := resolveCommit(ctx, cfg.Repo, "HEAD")
	if err != nil {
		return "", err
	}
	existingRaw, existingErr := gitOutput(ctx, cfg.Repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if existingErr == nil {
		existing := strings.TrimSpace(string(existingRaw))
		if existing != head {
			return "", fmt.Errorf("snapshot tag %q already points to %s", tag, shortRef(existing))
		}
	} else if err := git(ctx, cfg.Repo, "update-ref", ref, head); err != nil {
		return "", err
	}
	return tag, nil
}

func resolveCommit(ctx context.Context, repo, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("backup ref is required")
	}
	raw, err := gitOutput(ctx, repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(raw))
	if commit == "" {
		return "", fmt.Errorf("backup ref %q resolved to an empty commit", ref)
	}
	return commit, nil
}

func decodeManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func shortRef(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}
