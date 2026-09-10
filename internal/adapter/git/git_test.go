package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setup(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRepo(t *testing.T) {
	ctx := context.Background()
	r := setup(t)
	if head, err := r.Head(ctx); err != nil || head != "" {
		t.Fatalf("empty head: %q %v", head, err)
	}
	if email, _ := r.UserEmail(ctx); email != "t@example.com" {
		t.Fatalf("email %q", email)
	}
	_ = os.WriteFile(filepath.Join(r.Root, "a.ts"), []byte("export const a = 1;\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(r.Root, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(r.Root, "src", "b.ts"), []byte("export const b = 2;\n"), 0o644)
	files, err := r.ChangedFiles(ctx, "")
	if err != nil || !reflect.DeepEqual(files, []string{"a.ts", "src/b.ts"}) {
		t.Fatalf("untracked: %v %v", files, err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = r.Root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, _ := r.Head(ctx)
	if len(head) != 40 {
		t.Fatalf("head %q", head)
	}
	if files, _ := r.ChangedFiles(ctx, ""); len(files) != 0 {
		t.Fatalf("clean tree: %v", files)
	}
	_ = os.WriteFile(filepath.Join(r.Root, "a.ts"), []byte("export const a = 2;\n"), 0o644)
	if files, _ := r.ChangedFiles(ctx, ""); !reflect.DeepEqual(files, []string{"a.ts"}) {
		t.Fatalf("modified: %v", files)
	}
	if files, _ := r.ChangedFiles(ctx, head); !reflect.DeepEqual(files, []string{"a.ts"}) {
		t.Fatalf("since base: %v", files)
	}
}

func TestCommits(t *testing.T) {
	ctx := context.Background()
	r := setup(t)
	gitc := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = r.Root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(r.Root, "a.ts"), []byte("export const a = 1;\n"), 0o644)
	_ = os.WriteFile(filepath.Join(r.Root, "gone.ts"), []byte("x\n"), 0o644)
	gitc("add", ".")
	gitc("commit", "-q", "-m", "init")
	first, _ := r.Head(ctx)
	_ = os.WriteFile(filepath.Join(r.Root, "a.ts"), []byte("export const a = 2;\n"), 0o644)
	_ = os.WriteFile(filepath.Join(r.Root, "b.ts"), []byte("export const b = 1;\n"), 0o644)
	_ = os.Remove(filepath.Join(r.Root, "gone.ts"))
	gitc("add", "-A")
	gitc("commit", "-q", "-m", "second\n\nbody line")
	head, _ := r.Head(ctx)

	c, err := r.Commit(ctx, "HEAD")
	if err != nil || c.SHA != head || len(c.Parents) != 1 || c.Parents[0] != first || c.AuthorEmail != "t@example.com" || !strings.HasPrefix(c.Message, "second") || c.At.IsZero() {
		t.Fatalf("commit: %+v %v", c, err)
	}
	root, _ := r.Commit(ctx, first)
	if len(root.Parents) != 0 {
		t.Fatalf("root commit parents: %v", root.Parents)
	}
	files, err := r.ChangedIn(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	if !reflect.DeepEqual(got, map[string]string{"a.ts": "M", "b.ts": "A", "gone.ts": "D"}) {
		t.Fatalf("changed: %v", got)
	}
	if files, _ := r.ChangedIn(ctx, first); len(files) != 2 || files[0].Status != "A" {
		t.Fatalf("root commit changes: %v", files)
	}
	old, ok, err := r.Content(ctx, first, "a.ts")
	if err != nil || !ok || string(old) != "export const a = 1;\n" {
		t.Fatalf("content at first: %q %v %v", old, ok, err)
	}
	if _, ok, err := r.Content(ctx, first, "b.ts"); err != nil || ok {
		t.Fatalf("missing at first: %v %v", ok, err)
	}
	if _, ok, err := r.Content(ctx, head, "gone.ts"); err != nil || ok {
		t.Fatalf("deleted at head: %v %v", ok, err)
	}
}
