package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func ensureRepo(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	if _, err := os.Stat(filepath.Join(cfg.Repo, ".git")); err == nil {
		pullErr := git(ctx, cfg.Repo, "pull", "--rebase")
		if pullErr != nil {
			hasHead := git(ctx, cfg.Repo, "rev-parse", "--verify", "HEAD") == nil
			if !hasHead {
				return nil
			}
			if strings.Contains(pullErr.Error(), "no tracking information") ||
				strings.Contains(pullErr.Error(), "No remote repository specified") ||
				strings.Contains(pullErr.Error(), "no such ref was fetched") {
				return nil
			}
			return pullErr
		}
		return nil
	}
	if strings.TrimSpace(cfg.Remote) != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Repo), 0o700); err != nil {
			return err
		}
		if err := git(ctx, "", "clone", cfg.Remote, cfg.Repo); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(cfg.Repo, 0o700); err != nil {
		return err
	}
	if err := git(ctx, cfg.Repo, "init"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Remote) != "" {
		if err := git(ctx, cfg.Repo, "remote", "add", "origin", cfg.Remote); err != nil {
			return err
		}
	}
	return nil
}

func ensureRepoForRead(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return fmt.Errorf("backup repo path is required")
	}
	if _, err := os.Stat(filepath.Join(cfg.Repo, ".git")); err != nil {
		return ensureRepo(ctx, cfg)
	}
	remotes, err := gitOutput(ctx, cfg.Repo, "remote")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(remotes)), "origin") {
		return nil
	}
	return git(ctx, cfg.Repo, "fetch", "--prune", "--tags", "origin")
}

func commitAndPush(ctx context.Context, cfg Config, message string, push bool) (bool, error) {
	if err := git(ctx, cfg.Repo, "add", "."); err != nil {
		return false, err
	}
	if err := git(ctx, cfg.Repo, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	if err := git(ctx, cfg.Repo, "commit", "-m", message); err != nil {
		return false, err
	}
	if push {
		if err := git(ctx, cfg.Repo, "push", "-u", "origin", "HEAD"); err != nil {
			return true, err
		}
	}
	return true, nil
}

func git(ctx context.Context, dir string, args ...string) error {
	_, err := gitOutput(ctx, dir, args...)
	return err
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- wacrawl only passes fixed git subcommands plus configured repo paths.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wacrawl",
		"GIT_AUTHOR_EMAIL=wacrawl@example.invalid",
		"GIT_COMMITTER_NAME=wacrawl",
		"GIT_COMMITTER_EMAIL=wacrawl@example.invalid",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}
