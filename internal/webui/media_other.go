//go:build !unix

package webui

import "os"

func fileMaterialized(os.FileInfo) bool { return true }

func openMediaFile(root *os.Root, path string) (*os.File, error) {
	return root.Open(path) // #nosec G304 G703 -- containedMediaPath resolves and confines the path before this rooted call.
}
