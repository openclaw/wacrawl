package backup

import ckbackup "github.com/openclaw/crawlkit/backup"

func EnsureIdentity(path string) (string, error) {
	return ckbackup.EnsureIdentity(path)
}

func RecipientFromIdentity(path string) (string, error) {
	return ckbackup.RecipientFromIdentity(path)
}
