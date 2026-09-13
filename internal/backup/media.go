package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	ckbackup "github.com/openclaw/crawlkit/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

func collectBackupMedia(ctx context.Context, dbPath string, messages []store.Message) ([]ckbackup.File, error) {
	root, err := archiveRoot(dbPath)
	if err != nil {
		return nil, err
	}
	files, err := ckbackup.CollectFiles(ctx, filepath.Join(root, "media"), "media")
	if err != nil {
		return nil, err
	}
	logicalBySource := make(map[string]string, len(files))
	for _, file := range files {
		absolute, err := filepath.Abs(file.Source)
		if err != nil {
			return nil, err
		}
		logicalBySource[filepath.Clean(absolute)] = file.Path
	}
	for index := range messages {
		mediaPath := messages[index].MediaPath
		if mediaPath == "" {
			continue
		}
		absolute, err := filepath.Abs(mediaPath)
		if err != nil {
			return nil, err
		}
		if logical, ok := logicalBySource[filepath.Clean(absolute)]; ok {
			messages[index].MediaPath = logical
		}
	}
	return files, nil
}

func localizeMediaPaths(messages []store.Message, root string) error {
	for index := range messages {
		value := filepath.ToSlash(messages[index].MediaPath)
		if value == "media" || !strings.HasPrefix(value, "media/") {
			continue
		}
		clean := path.Clean(value)
		if clean == "media" || !strings.HasPrefix(clean, "media/") {
			return fmt.Errorf("backup media path escapes archive root: %s", value)
		}
		target := filepath.Clean(filepath.Join(root, filepath.FromSlash(clean)))
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("backup media path escapes archive root: %s", value)
		}
		messages[index].MediaPath = target
	}
	return nil
}

func archiveRoot(dbPath string) (string, error) {
	absolute, err := filepath.Abs(filepath.Dir(dbPath))
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func replaceMediaDuring(staged, target string, commit func() error) error {
	stagedInfo, err := os.Lstat(staged)
	if err != nil {
		return err
	}
	if stagedInfo.Mode()&os.ModeSymlink != 0 || !stagedInfo.IsDir() {
		return fmt.Errorf("staged media is not a directory: %s", staged)
	}
	parent := filepath.Dir(target)
	previous, err := os.MkdirTemp(parent, ".wacrawl-media-previous-")
	if err != nil {
		return err
	}
	if err := os.Remove(previous); err != nil {
		return err
	}
	hadPrevious := false
	if targetInfo, statErr := os.Lstat(target); statErr == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
			return fmt.Errorf("archive media is not a directory: %s", target)
		}
		if err := os.Rename(target, previous); err != nil {
			return err
		}
		hadPrevious = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(staged, target); err != nil {
		var restoreErr error
		if hadPrevious {
			restoreErr = os.Rename(previous, target)
		}
		return errors.Join(err, restoreErr)
	}
	if err := commit(); err != nil {
		removeErr := os.RemoveAll(target)
		var restoreErr error
		if hadPrevious {
			restoreErr = os.Rename(previous, target)
		}
		return errors.Join(err, removeErr, restoreErr)
	}
	if hadPrevious {
		_ = os.RemoveAll(previous)
	}
	return nil
}
