//go:build unix && !darwin

package webui

import (
	"os"
	"syscall"
)

func fileMaterialized(os.FileInfo) bool { return true }

func openMediaFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) // #nosec G304 G703 -- containedMediaPath resolves and confines the path before this rooted call.
}
