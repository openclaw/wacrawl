//go:build !darwin

package whatsappdb

import "os"

func fileMaterialized(os.FileInfo) bool { return true }

func openMediaFile(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- explicit copy-media reads a path from the local WhatsApp database.
}

func mediaReadWouldBlock(error) bool { return false }
