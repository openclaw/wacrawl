package backup

import (
	"time"

	ckbackup "github.com/openclaw/crawlkit/backup"
)

const formatVersion = ckbackup.FormatVersion

type Manifest struct {
	Format     int          `json:"format"`
	Encrypted  bool         `json:"encrypted"`
	Exported   time.Time    `json:"exported"`
	Recipients []string     `json:"recipients,omitempty"`
	Counts     Counts       `json:"counts"`
	Shards     []ShardEntry `json:"shards"`
	Files      []FileEntry  `json:"files,omitempty"`
}

type Counts struct {
	Contacts     int `json:"contacts"`
	Chats        int `json:"chats"`
	Groups       int `json:"groups"`
	Participants int `json:"participants"`
	Messages     int `json:"messages"`
	Revisions    int `json:"message_revisions,omitempty"`
	Identity     int `json:"archive_identity,omitempty"`
	MediaFiles   int `json:"media_files,omitempty"`
}

type (
	ShardEntry = ckbackup.ShardEntry
	FileEntry  = ckbackup.FileEntry
)

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
			"contacts":          manifest.Counts.Contacts,
			"chats":             manifest.Counts.Chats,
			"groups":            manifest.Counts.Groups,
			"participants":      manifest.Counts.Participants,
			"messages":          manifest.Counts.Messages,
			"message_revisions": manifest.Counts.Revisions,
			"archive_identity":  manifest.Counts.Identity,
		},
		Shards: manifest.Shards,
		Files:  manifest.Files,
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
			Revisions:    manifest.Counts["message_revisions"],
			Identity:     manifest.Counts["archive_identity"],
			MediaFiles:   len(manifest.Files),
		},
		Shards: manifest.Shards,
		Files:  manifest.Files,
	}
}
