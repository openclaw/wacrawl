package backup

import (
	"context"
	"encoding/json"
	"strings"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/crawlkit/mirror"
)

func encryptShard(plaintext []byte, recipientStrings []string) ([]byte, string, error) {
	return ckbackup.EncryptShard(plaintext, recipientStrings)
}

func decryptShard(ciphertext []byte, identityPath string) ([]byte, error) {
	return ckbackup.DecryptShard(ciphertext, identityPath)
}

func sha256Hex(data []byte) string {
	return ckbackup.SHA256Hex(data)
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
