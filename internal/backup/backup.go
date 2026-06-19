package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
)

const formatVersion = 1

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

type ShardEntry struct {
	Table  string `json:"table"`
	Path   string `json:"path"`
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

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
		if err := git(ctx, cfg.Repo, "push", "--atomic", "-u", "origin", "HEAD", "refs/tags/"+tag); err != nil {
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
	_ = ctx
	recipients := normalizedStrings(cfg.Recipients)
	reuseEncrypted := sameStrings(old.Recipients, recipients)
	var shards []ShardEntry
	add := func(table, rel string, rows any) error {
		plaintext, count, err := encodeJSONL(rows)
		if err != nil {
			return err
		}
		entry, err := writeShard(cfg, old, table, rel, plaintext, count, reuseEncrypted)
		if err != nil {
			return err
		}
		shards = append(shards, entry)
		return nil
	}
	staticTables := []struct {
		table string
		path  string
		rows  any
	}{
		{"contacts", "data/contacts.jsonl.gz.age", data.Contacts},
		{"chats", "data/chats.jsonl.gz.age", data.Chats},
		{"groups", "data/groups.jsonl.gz.age", data.Groups},
		{"group_participants", "data/group_participants.jsonl.gz.age", data.Participants},
	}
	for _, table := range staticTables {
		if err := add(table.table, table.path, table.rows); err != nil {
			return Manifest{}, err
		}
	}
	for _, shard := range messageShards(data.Messages) {
		if err := add("messages", shard.path, shard.messages); err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].Path < shards[j].Path })
	manifest := Manifest{
		Format:     formatVersion,
		Encrypted:  true,
		Exported:   time.Now().UTC(),
		Recipients: recipients,
		Counts: Counts{
			Contacts:     len(data.Contacts),
			Chats:        len(data.Chats),
			Groups:       len(data.Groups),
			Participants: len(data.Participants),
			Messages:     len(data.Messages),
		},
		Shards: shards,
	}
	if equivalentManifest(old, manifest) {
		return old, nil
	}
	if err := removeStaleShards(cfg.Repo, shards); err != nil {
		return Manifest{}, err
	}
	if err := writeManifest(cfg.Repo, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readSnapshot(cfg Config, manifest Manifest) (store.SnapshotData, error) {
	return readSnapshotWith(manifest, func(shard ShardEntry) ([]byte, error) {
		return decryptShardFile(cfg, shard)
	})
}

func readSnapshotWith(manifest Manifest, load func(ShardEntry) ([]byte, error)) (store.SnapshotData, error) {
	if manifest.Format != formatVersion {
		return store.SnapshotData{}, fmt.Errorf("unsupported backup format %d", manifest.Format)
	}
	var data store.SnapshotData
	for _, shard := range manifest.Shards {
		plaintext, err := load(shard)
		if err != nil {
			return store.SnapshotData{}, err
		}
		if got := sha256Hex(plaintext); got != shard.SHA256 {
			return store.SnapshotData{}, fmt.Errorf("backup shard hash mismatch for %s", shard.Path)
		}
		switch shard.Table {
		case "contacts":
			if err := decodeJSONL(plaintext, &data.Contacts); err != nil {
				return store.SnapshotData{}, err
			}
		case "chats":
			if err := decodeJSONL(plaintext, &data.Chats); err != nil {
				return store.SnapshotData{}, err
			}
		case "groups":
			if err := decodeJSONL(plaintext, &data.Groups); err != nil {
				return store.SnapshotData{}, err
			}
		case "group_participants":
			if err := decodeJSONL(plaintext, &data.Participants); err != nil {
				return store.SnapshotData{}, err
			}
		case "messages":
			var messages []store.Message
			if err := decodeJSONL(plaintext, &messages); err != nil {
				return store.SnapshotData{}, err
			}
			data.Messages = append(data.Messages, messages...)
		default:
			return store.SnapshotData{}, fmt.Errorf("unknown backup table %q", shard.Table)
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

func writeShard(cfg Config, old Manifest, table, rel string, plaintext []byte, rows int, reuseEncrypted bool) (ShardEntry, error) {
	hash := sha256Hex(plaintext)
	path, err := resolveShardPath(cfg.Repo, rel)
	if err != nil {
		return ShardEntry{}, err
	}
	if oldEntry, ok := old.entry(rel); reuseEncrypted && ok && oldEntry.SHA256 == hash {
		if info, err := os.Stat(path); err == nil {
			oldEntry.Bytes = info.Size()
			return oldEntry, nil
		}
	}
	encrypted, _, err := encryptShard(plaintext, cfg.Recipients)
	if err != nil {
		return ShardEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ShardEntry{}, err
	}
	if err := os.WriteFile(path, encrypted, 0o600); err != nil {
		return ShardEntry{}, err
	}
	return ShardEntry{Table: table, Path: rel, Rows: rows, SHA256: hash, Bytes: int64(len(encrypted))}, nil
}

func decryptShardFile(cfg Config, shard ShardEntry) ([]byte, error) {
	path, err := resolveShardPath(cfg.Repo, shard.Path)
	if err != nil {
		return nil, err
	}
	ciphertext, err := os.ReadFile(path) // #nosec G304 -- resolveShardPath confines manifest-controlled shard paths to data/*.age inside the backup repo.
	if err != nil {
		return nil, err
	}
	return decryptShard(ciphertext, cfg.Identity)
}

func resolveShardPath(repo, rel string) (string, error) {
	clean := path.Clean(strings.TrimSpace(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", fmt.Errorf("backup shard path escapes backup root: %s", rel)
	}
	if !strings.HasPrefix(clean, "data/") || !strings.HasSuffix(clean, ".age") {
		return "", fmt.Errorf("invalid backup shard path: %s", rel)
	}
	full := filepath.Join(repo, filepath.FromSlash(clean))
	root := filepath.Clean(filepath.Join(repo, "data"))
	parent := filepath.Clean(filepath.Dir(full))
	if parent != root && !strings.HasPrefix(parent, root+string(filepath.Separator)) {
		return "", fmt.Errorf("backup shard path escapes backup root: %s", rel)
	}
	return full, nil
}

func encodeJSONL(rows any) ([]byte, int, error) {
	value := reflect.ValueOf(rows)
	if value.Kind() != reflect.Slice {
		return nil, 0, fmt.Errorf("unsupported JSONL rows %T", rows)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := 0; i < value.Len(); i++ {
		if err := enc.Encode(value.Index(i).Interface()); err != nil {
			return nil, 0, err
		}
	}
	return buf.Bytes(), value.Len(), nil
}

func decodeJSONL[T any](plaintext []byte, out *[]T) error {
	scanner := bufio.NewScanner(bytes.NewReader(plaintext))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var value T
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return scanner.Err()
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
	data, err := os.ReadFile(filepath.Join(repo, "manifest.json")) // #nosec G304 -- repo is the configured local backup repository.
	if err != nil {
		return Manifest{}, err
	}
	return decodeManifest(data)
}

func writeManifest(repo string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(repo, "manifest.json"), data, 0o600)
}

func (m Manifest) entry(path string) (ShardEntry, bool) {
	for _, shard := range m.Shards {
		if shard.Path == path {
			return shard, true
		}
	}
	return ShardEntry{}, false
}

func equivalentManifest(a, b Manifest) bool {
	if a.Format != b.Format || a.Encrypted != b.Encrypted || !sameStrings(a.Recipients, b.Recipients) || a.Counts != b.Counts || len(a.Shards) != len(b.Shards) {
		return false
	}
	for i := range a.Shards {
		left, right := a.Shards[i], b.Shards[i]
		left.Bytes, right.Bytes = 0, 0
		if left != right {
			return false
		}
	}
	return true
}

func normalizedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	a, b = normalizedStrings(a), normalizedStrings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func removeStaleShards(repo string, shards []ShardEntry) error {
	keep := map[string]struct{}{}
	for _, shard := range shards {
		keep[filepath.Clean(filepath.Join(repo, filepath.FromSlash(shard.Path)))] = struct{}{}
	}
	root := filepath.Join(repo, "data")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	var stale []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".age") {
			return nil
		}
		clean := filepath.Clean(path)
		if _, ok := keep[clean]; ok {
			return nil
		}
		stale = append(stale, clean)
		return nil
	}); err != nil {
		return err
	}
	for _, path := range stale {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("stale shard path escapes backup root: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
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
