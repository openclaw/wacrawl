package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupInitReportsLocalOnly(t *testing.T) {
	root := t.TempDir()
	var out, errOut bytes.Buffer
	args := []string{"--json", "--db", filepath.Join(root, "archive.db"), "--source", filepath.Join(root, "source"), "backup", "init", "--config", filepath.Join(root, "backup.json"), "--repo", filepath.Join(root, "repo"), "--identity", filepath.Join(root, "age.key")}
	if err := Run(context.Background(), args, &out, &errOut); err != nil {
		t.Fatalf("init: %v, %s", err, &errOut)
	}
	var result struct {
		Remote string `json:"remote"`
		Pushed *bool  `json:"pushed"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Remote != "" || result.Pushed == nil || *result.Pushed {
		t.Fatalf("expected local-only output, got %s", &out)
	}
}

func TestBackupHelpExplainsLocalOnly(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"backup", "--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Backups stay local") {
		t.Fatalf("unexpected help: %s", &out)
	}
}
