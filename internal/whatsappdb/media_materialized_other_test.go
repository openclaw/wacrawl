//go:build !darwin

package whatsappdb

import "testing"

func TestFileMaterializedOther(t *testing.T) {
	if !fileMaterialized(nil) {
		t.Fatal("non-darwin fileMaterialized should treat every file as materialized")
	}
}
