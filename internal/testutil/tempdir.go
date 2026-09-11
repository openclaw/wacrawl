// Package testutil provides shared filesystem fixtures for tests.
package testutil

import (
	"path/filepath"
	"testing"
)

// TempDir returns a canonical temporary directory for exact path assertions.
// macOS commonly exposes its temporary root through the /var symlink.
func TempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
