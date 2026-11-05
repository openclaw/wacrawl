package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupDefaultsToLocal(t *testing.T) {
	if got := DefaultConfig().Remote; got != "" {
		t.Fatalf("default remote = %q, want empty", got)
	}
	for _, body := range []string{"{}", `{"remote":""}`} {
		path := filepath.Join(t.TempDir(), "backup.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil || cfg.Remote != "" {
			t.Fatalf("load %q: remote=%q, error=%v", body, cfg.Remote, err)
		}
	}
}

func TestBackupInitWithoutRemote(t *testing.T) {
	root := t.TempDir()
	opts := Options{ConfigPath: filepath.Join(root, "backup.json"), Repo: filepath.Join(root, "repo"), Identity: filepath.Join(root, "age.key"), Push: true}
	cfg, _, err := Init(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote != "" {
		t.Fatalf("remote = %q, want empty", cfg.Remote)
	}
	if remotes := auditGitBytes(t, cfg.Repo, "remote"); len(remotes) != 0 {
		t.Fatalf("unexpected remotes: %s", remotes)
	}
}

func TestSavedBackupRemoteIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.json")
	body := []byte(`{"remote":"https://example.invalid/personal-backup.git"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Remote != "https://example.invalid/personal-backup.git" {
		t.Fatalf("remote=%q, error=%v", cfg.Remote, err)
	}
	after, err := os.ReadFile(path) // #nosec G304 -- reads the generated config in this test's temporary directory.
	if err != nil || !bytes.Equal(body, after) {
		t.Fatal("loading rewrote saved config")
	}
}

func TestBackupPushWithoutRemoteStaysLocal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	trace := filepath.Join(root, "git.trace")
	t.Setenv("GIT_TRACE", trace)
	opts := Options{ConfigPath: filepath.Join(root, "backup.json"), Repo: filepath.Join(root, "repo"), Identity: filepath.Join(root, "age.key"), Push: true}
	if _, _, err := Init(ctx, opts); err != nil {
		t.Fatal(err)
	}
	st := openFixtureStore(t, "local.db")
	for _, tag := range []string{"", "snapshot/local"} {
		opts.Tag = tag
		result, err := Push(ctx, st, opts)
		if err != nil {
			t.Fatal(err)
		}
		if result.Pushed || result.Tag != tag {
			t.Fatalf("unexpected local result: %+v", result)
		}
	}
	if got := strings.TrimSpace(string(auditGitBytes(t, opts.Repo, "tag", "--list"))); got != "snapshot/local" {
		t.Fatalf("local tag=%q", got)
	}
	logs, err := os.ReadFile(trace) // #nosec G304 -- reads the Git trace generated in this test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"git clone", "git fetch", "git push", "git ls-remote", "upload-pack", "receive-pack"} {
		if bytes.Contains(logs, []byte(transport)) {
			t.Fatalf("local backup attempted transport %q", transport)
		}
	}
}

func TestBackupUsesExistingOriginWithoutSavedRemote(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "--bare", remote)
	opts := Options{ConfigPath: filepath.Join(root, "backup.json"), Repo: filepath.Join(root, "repo"), Identity: filepath.Join(root, "age.key"), Remote: remote}
	cfg, _, err := Init(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remote = ""
	if err := SaveConfig(opts.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	opts.Remote = ""
	opts.Push = true
	st := openFixtureStore(t, "origin.db")
	for _, tag := range []string{"", "snapshot/origin"} {
		opts.Tag = tag
		result, err := Push(ctx, st, opts)
		if err != nil || !result.Pushed {
			t.Fatalf("origin push=%+v, error=%v", result, err)
		}
	}
	if !bytes.Equal(auditGitBytes(t, opts.Repo, "rev-parse", "HEAD"), auditGitBytes(t, remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("origin did not receive snapshot")
	}
	if got := strings.TrimSpace(string(auditGitBytes(t, opts.Repo, "remote", "get-url", "origin"))); got != remote {
		t.Fatalf("origin changed to %q", got)
	}
	after, err := os.ReadFile(opts.ConfigPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("push rewrote saved config")
	}
}
