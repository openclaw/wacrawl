//go:build darwin

package webui

import (
	"os"
	"syscall"
)

// sfDataless mirrors SF_DATALESS from <sys/stat.h>: the file's bytes live with
// a dataless-file provider (for WhatsApp media, content that has not been
// downloaded yet) and reading it would block until macOS materializes it.
const sfDataless = 0x40000000

func fileMaterialized(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return stat.Flags&sfDataless == 0
}

// openMediaFile opens with O_NONBLOCK so a dataless file fails fast instead of
// stalling the request goroutine on materialization; the flag has no effect on
// reads of ordinary regular files.
func openMediaFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) // #nosec G304 -- media_path was written by our own importer, not request input.
}
