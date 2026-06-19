package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/crawlkit/mirror"
	"github.com/openclaw/wacrawl/internal/store"
)

const formatVersion = ckbackup.FormatVersion

type Manifest struct {
	Format     int          `json:"format"`
	Encrypted  bool         `json:"encrypted"`
	Exported   time.Time    `json:"exported"`
	Recipients []string     `json:"recipients,omitempty"`
	Counts     Counts       `json:"counts"`
	Shards     []ShardEntry `json:"shards"`
}

type Counts struct {
	Contacts     int `json:"contacts"`
	Chats        int `json:"chats"`
	Groups       int `json:"groups"`
	Participants int `json:"participants"`
	Messages     int `json:"messages"`
}

type ShardEntry = ckbackup.ShardEntry

type Result struct {
	Repo      string `json:"repo"`
	Changed   bool   `json:"changed"`
	Encrypted bool   `json:"encrypted"`
	Shards    int    `json:"shards"`
	Messages  int    `json:"messages"`
	Ref       string `json:"ref,omitempty"`
	Tag       string `json:"tag,omitempty"`
}

func Init(ctx context.Context, opts Options) (Config, string, error) {
	cfg, err := ResolveOptions(opts)
	if err != nil {
		return Config{}, "", err
	}
	recipient, err := EnsureIdentity(cfg.Identity)
	if err != nil {
		return Config{}, "", err
	}
	if len(cfg.Recipients) == 0 {
		cfg.Recipients = []string{recipient}
	}
	if err := SaveConfig(opts.ConfigPath, cfg); err != nil {
		return Config{}, "", err
	}
	if err := ensureRepo(ctx, cfg); err != nil {
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
	if len(cfg.Recipients) == 0 {
		recipient, err := RecipientFromIdentity(cfg.Identity)
		if err != nil {
			return Result{}, err
		}
		cfg.Recipients = []string{recipient}
	}
	if err := ensureRepo(ctx, cfg); err != nil {
		return Result{}, err
	}
	if err := validateSnapshotTag(ctx, cfg.Repo, opts.Tag); err != nil {
		return Result{}, err
	}
	if err := writeBackupReadme(cfg.Repo); err != nil {
		return Result{}, err
	}
	oldManifest, _ := readManifest(cfg.Repo)
	data, err := st.ExportAll(ctx)
	if err != nil {
		return Result{}, err
	}
	manifest, err := writeSnapshot(ctx, cfg, data, oldManifest)
	if err != nil {
		return Result{}, err
	}
	pushWithTag := opts.Push && strings.TrimSpace(opts.Tag) != ""
	changed, err := commitAndPush(ctx, cfg, "sync: update encrypted wacrawl backup", opts.Push && !pushWithTag)
	if err != nil {
		return Result{}, err
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
	return Result{Repo: cfg.Repo, Changed: changed, Encrypted: true, Shards: len(manifest.Shards), Messages: manifest.Counts.Messages, Tag: tag}, nil
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
	sourcePath := "backup:" + cfg.Repo
	if ref != "" {
		sourcePath += "@" + ref
	}
	if err := st.ImportSnapshot(ctx, data, sourcePath, manifest.Exported); err != nil {
		return Result{}, err
	}
	return Result{Repo: cfg.Repo, Changed: true, Encrypted: manifest.Encrypted, Shards: len(manifest.Shards), Messages: len(data.Messages), Ref: ref}, nil
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

func writeSnapshot(ctx context.Context, cfg Config, data store.SnapshotData, old Manifest) (Manifest, error) {
	shards := []ckbackup.Shard{
		{Table: "contacts", Path: "data/contacts.jsonl.gz.age", Rows: data.Contacts},
		{Table: "chats", Path: "data/chats.jsonl.gz.age", Rows: data.Chats},
		{Table: "groups", Path: "data/groups.jsonl.gz.age", Rows: data.Groups},
		{Table: "group_participants", CountKey: "participants", Path: "data/group_participants.jsonl.gz.age", Rows: data.Participants},
	}
	for _, shard := range messageShards(data.Messages) {
		shards = append(shards, ckbackup.Shard{Table: "messages", Path: shard.path, Rows: shard.messages})
	}
	sharedOld := toCrawlkitManifest(old)
	if len(data.Messages) == 0 {
		delete(sharedOld.Counts, "messages")
	}
	manifest, err := ckbackup.WriteSnapshot(ctx, crawlkitConfig(cfg), shards, sharedOld)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Counts["contacts"] = len(data.Contacts)
	manifest.Counts["chats"] = len(data.Chats)
	manifest.Counts["groups"] = len(data.Groups)
	manifest.Counts["participants"] = len(data.Participants)
	manifest.Counts["messages"] = len(data.Messages)
	if ckbackup.EquivalentManifest(toCrawlkitManifest(old), manifest) {
		return old, nil
	}
	if err := ckbackup.WriteManifest(cfg.Repo, manifest); err != nil {
		return Manifest{}, err
	}
	return fromCrawlkitManifest(manifest), nil
}

func readSnapshot(cfg Config, manifest Manifest) (store.SnapshotData, error) {
	shards, err := ckbackup.ReadSnapshot(crawlkitConfig(cfg), toCrawlkitManifest(manifest))
	if err != nil {
		return store.SnapshotData{}, err
	}
	return decodeSnapshot(shards)
}

func decodeSnapshot(shards []ckbackup.DecodedShard) (store.SnapshotData, error) {
	var data store.SnapshotData
	for _, shard := range shards {
		switch shard.Entry.Table {
		case "contacts":
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &data.Contacts); err != nil {
				return store.SnapshotData{}, err
			}
		case "chats":
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &data.Chats); err != nil {
				return store.SnapshotData{}, err
			}
		case "groups":
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &data.Groups); err != nil {
				return store.SnapshotData{}, err
			}
		case "group_participants":
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &data.Participants); err != nil {
				return store.SnapshotData{}, err
			}
		case "messages":
			var messages []store.Message
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &messages); err != nil {
				return store.SnapshotData{}, err
			}
			data.Messages = append(data.Messages, messages...)
		default:
			return store.SnapshotData{}, fmt.Errorf("unknown backup table %q", shard.Entry.Table)
		}
	}
	sort.Slice(data.Messages, func(i, j int) bool {
		if data.Messages[i].Timestamp.Equal(data.Messages[j].Timestamp) {
			return data.Messages[i].SourcePK < data.Messages[j].SourcePK
		}
		return data.Messages[i].Timestamp.Before(data.Messages[j].Timestamp)
	})
	return data, nil
}

type messageShard struct {
	path     string
	messages []store.Message
}

func messageShards(messages []store.Message) []messageShard {
	buckets := map[string][]store.Message{}
	for _, message := range messages {
		t := message.Timestamp.UTC()
		year, month := "unknown", "00"
		if !t.IsZero() {
			year = fmt.Sprintf("%04d", t.Year())
			month = fmt.Sprintf("%02d", int(t.Month()))
		}
		rel := fmt.Sprintf("data/messages/%s/%s.jsonl.gz.age", year, month)
		buckets[rel] = append(buckets[rel], message)
	}
	paths := make([]string, 0, len(buckets))
	for path := range buckets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]messageShard, 0, len(paths))
	for _, path := range paths {
		values := buckets[path]
		sort.Slice(values, func(i, j int) bool {
			if values[i].Timestamp.Equal(values[j].Timestamp) {
				return values[i].SourcePK < values[j].SourcePK
			}
			return values[i].Timestamp.Before(values[j].Timestamp)
		})
		out = append(out, messageShard{path: path, messages: values})
	}
	return out
}

func readManifest(repo string) (Manifest, error) {
	manifest, err := ckbackup.ReadManifest(repo)
	if err != nil {
		return Manifest{}, err
	}
	return fromCrawlkitManifest(manifest), nil
}

func crawlkitConfig(cfg Config) ckbackup.Config {
	return ckbackup.Config{Repo: cfg.Repo, Identity: cfg.Identity, Recipients: cfg.Recipients}
}

func toCrawlkitManifest(manifest Manifest) ckbackup.Manifest {
	return ckbackup.Manifest{
		Format:     manifest.Format,
		Encrypted:  manifest.Encrypted,
		Exported:   manifest.Exported,
		Recipients: manifest.Recipients,
		Counts: map[string]int{
			"contacts":     manifest.Counts.Contacts,
			"chats":        manifest.Counts.Chats,
			"groups":       manifest.Counts.Groups,
			"participants": manifest.Counts.Participants,
			"messages":     manifest.Counts.Messages,
		},
		Shards: manifest.Shards,
	}
}

func fromCrawlkitManifest(manifest ckbackup.Manifest) Manifest {
	participants := manifest.Counts["participants"]
	if participants == 0 {
		participants = manifest.Counts["group_participants"]
	}
	return Manifest{
		Format:     manifest.Format,
		Encrypted:  manifest.Encrypted,
		Exported:   manifest.Exported,
		Recipients: manifest.Recipients,
		Counts: Counts{
			Contacts:     manifest.Counts["contacts"],
			Chats:        manifest.Counts["chats"],
			Groups:       manifest.Counts["groups"],
			Participants: participants,
			Messages:     manifest.Counts["messages"],
		},
		Shards: manifest.Shards,
	}
}

func writeBackupReadme(repo string) error {
	path := filepath.Join(repo, "README.md")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	const body = `# backup-wacrawl

Encrypted Git backup for a local wacrawl archive.

This repository is written by ` + "`wacrawl backup push`" + `. It is safe to keep on
GitHub because the archive payload is encrypted before Git sees it.

## Layout

` + "```text" + `
README.md
manifest.json
data/chats.jsonl.gz.age
data/contacts.jsonl.gz.age
data/groups.jsonl.gz.age
data/group_participants.jsonl.gz.age
data/messages/YYYY/MM.jsonl.gz.age
` + "```" + `

` + "`manifest.json`" + ` is cleartext and contains format version, export time,
public age recipients, table counts, shard paths, encrypted byte sizes, and
plaintext hashes used for restore verification. Message text, contacts, chat
names, participant IDs, and media metadata stay inside encrypted ` + "`*.jsonl.gz.age`" + ` shards.

## Security Model

Shard contents are JSONL, gzip-compressed with a fixed gzip timestamp, and
encrypted with age for every configured public recipient. The local
` + "`~/.wacrawl/age.key`" + ` identity is required to decrypt.

Git can still see manifest metadata: export time, public recipients, table
names, row counts, shard paths, encrypted byte sizes, plaintext shard hashes,
backup cadence, and which encrypted shards changed. Git cannot read message
text, contacts, chat names, participant IDs, or media metadata without an age
identity.

Anyone who can push to this repository can replace encrypted backup data with
different data encrypted to your public recipient. Keep repository write access
restricted and review unexpected backup commits. If an age identity is
compromised, remove its public recipient and push a new backup; old Git history
may still contain shards decryptable by the compromised key.

## Push

` + "```bash" + `
wacrawl backup push
wacrawl backup push --tag snapshot/before-phone-migration
` + "```" + `

The command pulls/rebases this checkout, refreshes the local wacrawl archive
according to the normal sync policy, writes encrypted shards, updates the
manifest, commits, and pushes this repository.

Every changed backup is a Git commit. Optional tags name important checkpoints;
tag names are visible Git metadata and should not contain sensitive text.

## Restore

` + "```bash" + `
wacrawl backup pull
wacrawl backup snapshots
wacrawl --db /tmp/wacrawl-history.db backup pull --ref snapshot/before-phone-migration
` + "```" + `

` + "`backup pull`" + ` decrypts every shard with the local age identity, verifies the
manifest hashes, validates the snapshot, and imports it into the configured
wacrawl archive database. Historical refs are read directly from Git objects
without changing this checkout's current branch.

## Recovery

Install wacrawl, clone this repo to the path in ` + "`~/.wacrawl/backup.json`" + `,
restore the local age identity file, then run:

` + "```bash" + `
wacrawl backup pull
wacrawl --sync never status
` + "```" + `

Do not commit the age identity. Only public ` + "`age1...`" + ` recipients belong in
config; ` + "`AGE-SECRET-KEY-...`" + ` values must stay local or in a password manager.
`
	return os.WriteFile(path, []byte(body), 0o600)
}
