//go:build unix

package webui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenMediaFileDoesNotBlockOnFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "media-pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	result := make(chan *os.File, 1)
	errs := make(chan error, 1)
	go func() {
		file, err := openMediaFile(root, "media-pipe")
		result <- file
		errs <- err
	}()
	select {
	case file := <-result:
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		if info, err := file.Stat(); err != nil || info.Mode().IsRegular() {
			t.Fatalf("FIFO stat=%v err=%v", info, err)
		}
	case <-time.After(time.Second):
		t.Fatal("opening FIFO blocked")
	}
}
