package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
