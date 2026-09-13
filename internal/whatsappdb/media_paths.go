package whatsappdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/wacrawl/internal/mediafile"
	"github.com/openclaw/wacrawl/internal/store"
)

func copyArchiveMedia(messages []store.Message, sourceRoot, mediaRoot string) (int, int, error) {
	// Validate the whole batch before publishing even the first valid object.
	for _, message := range messages {
		if message.SourceMediaPathRejected {
			return 0, 0, errors.New("source media path was rejected; import without copying media to retain metadata")
		}
		src := strings.TrimSpace(message.MediaPath)
		if src == "" {
			continue
		}
		root, err := sourceMediaRoot(sourceRoot, src)
		if err != nil {
			return 0, 0, err
		}
		if _, err := mediafile.Stat(root, src); err != nil && !os.IsNotExist(err) {
			return 0, 0, err
		}
	}
	var err error
	mediaRoot, err = prepareMediaRoot(sourceRoot, mediaRoot)
	if err != nil {
		return 0, 0, err
	}
	type result struct {
		path    string
		missing bool
	}
	seen := map[string]result{}
	copied := 0
	missing := 0
	for i := range messages {
		src := strings.TrimSpace(messages[i].MediaPath)
		if src == "" {
			continue
		}
		if r, ok := seen[src]; ok {
			if !r.missing {
				messages[i].MediaPath = r.path
			}
			continue
		}
		root, err := sourceMediaRoot(sourceRoot, src)
		if err != nil {
			return copied, missing, err
		}
		dest, err := copyMediaFile(root, src, mediaRoot)
		if err != nil {
			if errors.Is(err, errMediaMissing) || errors.Is(err, errMediaNotDownloaded) {
				missing++
				seen[src] = result{missing: true}
				continue
			}
			return copied, missing, err
		}
		copied++
		seen[src] = result{path: dest}
		messages[i].MediaPath = dest
	}
	return copied, missing, nil
}

// mediaMaterialized reports whether a media file's bytes are already on disk.
// Tests replace this to simulate macOS SF_DATALESS (iCloud) stubs.
var (
	mediaMaterialized    = fileMaterialized
	openMediaFileForCopy = mediafile.Open
)

var (
	errMediaNotDownloaded = errors.New("media not downloaded locally")
	errMediaMissing       = errors.New("source media is missing")
)

func normalizeMediaReadError(src string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", errMediaMissing, src)
	}
	if mediaReadWouldBlock(err) {
		return fmt.Errorf("%w: %s", errMediaNotDownloaded, src)
	}
	return err
}

func resolveDesktopMediaPath(sourceRoot, dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return ""
	}
	rel := cleanDesktopMediaRel(filepath.FromSlash(dbPath))
	if rel == "" {
		return ""
	}
	candidates := []string{filepath.Join(sourceRoot, rel)}
	if firstPathElement(rel) == "Media" {
		candidates = append([]string{filepath.Join(sourceRoot, "Message", rel)}, candidates...)
	}
	for _, candidate := range candidates {
		root, err := sourceMediaRoot(sourceRoot, candidate)
		if err != nil {
			return ""
		}
		if _, err := mediafile.Stat(root, candidate); err == nil {
			return candidate
		} else if !os.IsNotExist(err) {
			return ""
		}
	}
	return candidates[0]
}

func cleanDesktopMediaRel(path string) string {
	rel := filepath.Clean(path)
	if filepath.IsAbs(path) {
		return ""
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".." {
			return ""
		}
	}
	for _, root := range []string{"Media", filepath.Join("Message", "Media")} {
		if strings.HasPrefix(rel, root+string(filepath.Separator)) {
			return rel
		}
	}
	return ""
}

func firstPathElement(path string) string {
	if path == "." || path == "" {
		return ""
	}
	if i := strings.IndexRune(path, os.PathSeparator); i >= 0 {
		return path[:i]
	}
	return path
}
