//go:build darwin

package whatsappdb

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type fakeFileInfo struct {
	os.FileInfo
	sys any
}

func (f fakeFileInfo) Sys() any { return f.sys }

func TestFileMaterializedDatalessFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileMaterialized(fakeFileInfo{FileInfo: info, sys: &syscall.Stat_t{Flags: sfDataless}}) {
		t.Fatal("SF_DATALESS file should not be materialized")
	}
	if !fileMaterialized(fakeFileInfo{FileInfo: info, sys: &syscall.Stat_t{Flags: 0}}) {
		t.Fatal("zero flags should be materialized")
	}
	if !fileMaterialized(fakeFileInfo{FileInfo: info, sys: "not-a-stat"}) {
		t.Fatal("unknown Sys should be treated as materialized")
	}
}

func TestCopyMediaFileTreatsNonblockingOpenAsMissing(t *testing.T) {
	old := openMediaFileForCopy
	t.Cleanup(func() { openMediaFileForCopy = old })
	openMediaFileForCopy = func(string) (*os.File, error) { return nil, syscall.EAGAIN }

	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(dir, "out", "photo.jpg")
	if err := os.WriteFile(src, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyMediaFile(src, dest); !errors.Is(err, errMediaNotDownloaded) {
		t.Fatalf("nonblocking open error = %v, want media not downloaded", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist: %v", err)
	}
}
