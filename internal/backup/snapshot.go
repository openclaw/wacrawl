package backup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

type archiveIdentity struct {
	SourceStoreIdentity string `json:"source_store_identity"`
	AccountIdentity     string `json:"account_identity"`
}

func writeSnapshot(ctx context.Context, cfg Config, data store.SnapshotData, files []ckbackup.File, old Manifest) (Manifest, error) {
	var identities []archiveIdentity
	if data.SourceStoreIdentity != "" || data.AccountIdentity != "" {
		identities = append(identities, archiveIdentity{SourceStoreIdentity: data.SourceStoreIdentity, AccountIdentity: data.AccountIdentity})
	}
	shards := []ckbackup.Shard{
		{Table: "contacts", Path: "data/contacts.jsonl.gz.age", Rows: data.Contacts},
		{Table: "chats", Path: "data/chats.jsonl.gz.age", Rows: data.Chats},
		{Table: "groups", Path: "data/groups.jsonl.gz.age", Rows: data.Groups},
		{Table: "group_participants", CountKey: "participants", Path: "data/group_participants.jsonl.gz.age", Rows: data.Participants},
		{Table: "message_revisions", CountKey: "message_revisions", Path: "data/message_revisions.jsonl.gz.age", Rows: data.Revisions},
		{Table: "archive_identity", CountKey: "archive_identity", Path: "data/archive_identity.jsonl.gz.age", Rows: identities},
	}
	if len(data.Messages) == 0 {
		shards = append(shards, ckbackup.Shard{Table: "messages", Path: "data/messages/unknown/00.jsonl.gz.age", Rows: data.Messages})
	}
	for _, shard := range messageShards(data.Messages) {
		shards = append(shards, ckbackup.Shard{Table: "messages", Path: shard.path, Rows: shard.messages})
	}
	manifest, err := ckbackup.WriteSnapshotWithFiles(ctx, crawlkitConfig(cfg), shards, files, toCrawlkitManifest(old))
	if err != nil {
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
		case "message_revisions":
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &data.Revisions); err != nil {
				return store.SnapshotData{}, err
			}
		case "archive_identity":
			var identities []archiveIdentity
			if err := ckbackup.DecodeJSONL(shard.Plaintext, &identities); err != nil {
				return store.SnapshotData{}, err
			}
			if len(identities) > 1 {
				return store.SnapshotData{}, errors.New("backup contains multiple archive identities")
			}
			if len(identities) == 1 {
				data.SourceStoreIdentity = identities[0].SourceStoreIdentity
				data.AccountIdentity = identities[0].AccountIdentity
			}
		default:
			return store.SnapshotData{}, fmt.Errorf("unknown backup table %q", shard.Entry.Table)
		}
	}
	slices.SortFunc(data.Messages, compareMessages)
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
	paths := slices.Sorted(maps.Keys(buckets))
	out := make([]messageShard, 0, len(paths))
	for _, path := range paths {
		values := buckets[path]
		slices.SortFunc(values, compareMessages)
		out = append(out, messageShard{path: path, messages: values})
	}
	return out
}

func compareMessages(a, b store.Message) int {
	return cmp.Or(a.Timestamp.Compare(b.Timestamp), cmp.Compare(a.SourcePK, b.SourcePK))
}
