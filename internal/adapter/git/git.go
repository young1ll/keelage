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
	"time"

	"github.com/young1ll/keelage/internal/port"
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

// Commit implements port.Commits.
func (r *Repo) Commit(ctx context.Context, ref string) (port.Commit, error) {
	out, err := run(ctx, r.Root, "show", "-s", "--format=%H%x00%P%x00%ae%x00%aI%x00%B", ref)
	if err != nil {
		return port.Commit{}, err
	}
	parts := strings.SplitN(out, "\x00", 5)
	if len(parts) < 5 {
		return port.Commit{}, fmt.Errorf("git show: unexpected output for %s", ref)
	}
	c := port.Commit{SHA: parts[0], AuthorEmail: parts[2], Message: strings.TrimSpace(parts[4])}
	if parts[1] != "" {
		c.Parents = strings.Fields(parts[1])
	}
	if t, err := time.Parse(time.RFC3339, parts[3]); err == nil {
		c.At = t
	}
	return c, nil
}

// ChangedIn implements port.Commits.
func (r *Repo) ChangedIn(ctx context.Context, sha string) ([]port.FileChange, error) {
	out, err := run(ctx, r.Root, "diff-tree", "--no-commit-id", "--name-status", "-r", "-M", "--root", sha)
	if err != nil {
		return nil, err
	}
	var files []port.FileChange
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "\t")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		fc := port.FileChange{Status: f[0][:1], Path: f[1]}
		if fc.Status == "R" && len(f) >= 3 {
			fc.OldPath, fc.Path = f[1], f[2]
		}
		files = append(files, fc)
	}
	return files, nil
}

// Content implements port.Commits.
func (r *Repo) Content(ctx context.Context, sha, path string) ([]byte, bool, error) {
	cmd := exec.CommandContext(ctx, "git", "show", sha+":"+path)
	cmd.Dir = r.Root
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "does not exist") || strings.Contains(msg, "exists on disk, but not in") || strings.Contains(msg, "invalid object name") || strings.Contains(msg, "bad object") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("git show %s:%s: %w: %s", sha, path, err, strings.TrimSpace(msg))
	}
	return out.Bytes(), true, nil
}

var _ port.Commits = (*Repo)(nil)
