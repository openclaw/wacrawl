//go:build !darwin

package webui

import "os"

func fileMaterialized(os.FileInfo) bool { return true }

func openMediaFile(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- media_path was written by our own importer, not request input.
}
