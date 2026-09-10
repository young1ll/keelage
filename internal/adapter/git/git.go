// Package git shells out to the git CLI for the few facts the daemon and
// headless commands need: repo root, HEAD, changed files, user identity.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Repo is a git working tree.
type Repo struct {
	Root string
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Open finds the repository containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	root, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	return &Repo{Root: root}, nil
}

// Head returns the HEAD commit sha ("" in an empty repository).
func (r *Repo) Head(ctx context.Context) (string, error) {
	sha, err := run(ctx, r.Root, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", nil //nolint:nilerr // no commits yet
	}
	return sha, nil
}

// ChangedFiles lists files that differ from HEAD (staged, unstaged and
// untracked-but-not-ignored), repo-relative with forward slashes. When
// base is non-empty, files changed since that commit are listed instead.
func (r *Repo) ChangedFiles(ctx context.Context, base string) ([]string, error) {
	set := map[string]struct{}{}
	add := func(out string) {
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				set[l] = struct{}{}
			}
		}
	}
	if base != "" {
		out, err := run(ctx, r.Root, "diff", "--name-only", base)
		if err != nil {
			return nil, err
		}
		add(out)
	} else {
		head, err := r.Head(ctx)
		if err != nil {
			return nil, err
		}
		if head != "" {
			out, err := run(ctx, r.Root, "diff", "--name-only", "HEAD")
			if err != nil {
				return nil, err
			}
			add(out)
		} else {
			out, err := run(ctx, r.Root, "diff", "--name-only", "--cached")
			if err != nil {
				return nil, err
			}
			add(out)
		}
	}
	out, err := run(ctx, r.Root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	add(out)
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

// UserEmail returns user.email, or an error when unset.
func (r *Repo) UserEmail(ctx context.Context) (string, error) {
	email, err := run(ctx, r.Root, "config", "--get", "user.email")
	if err != nil || email == "" {
		return "", errors.New("git user.email is not set")
	}
	return email, nil
}
