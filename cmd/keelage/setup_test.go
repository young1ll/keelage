package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// snapshot hashes every regular file under dirs (relative path → sha256).
func snapshot(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			out[p] = hex.EncodeToString(sum[:])
			return nil
		})
	}
	return out
}

// setup → init → uninstall must leave the user's files byte-identical
// (implementation plan §3.6 "설치→제거 왕복이 원상태").
func TestSetupInitUninstall_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KEELAGE_HOME", filepath.Join(home, ".keelage"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	// a PATH with git but no `claude` CLI: MCP registration is manual
	gitPath, _ := exec.LookPath("git")
	binDir := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(binDir, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	settings := "{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"PreToolUse\": [{\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"/usr/local/bin/lint.sh\"}]}]\n  }\n}\n"
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("Always answer in Korean.\n"), 0o644)

	root := filepath.Join(t.TempDir(), "roundtrip")
	_ = os.MkdirAll(filepath.Join(root, "docs", "adr"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CLAUDE.md", "# Project rules\n\nRun `make test` before committing.\n")
	write("pkg/CLAUDE.md", "Keep pkg dependency-free.\n")
	write("docs/adr/0001-unit-is-change.md", "# 0001. Unit is change\nStatus: accepted\n## Decision\nChange is the unit.\n")
	write("docs/adr/README.md", "index\n")
	write(".gitignore", "/bin/\n")
	write("README.md", "hello\n")
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "dev@example.com"}, {"config", "user.name", "dev"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	before := snapshot(t, root, filepath.Join(home, ".claude"))

	run := func(stdin string, args ...string) (string, error) {
		t.Helper()
		c := rootCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetIn(strings.NewReader(stdin))
		c.SetArgs(args)
		err := c.ExecuteContext(context.Background())
		return out.String(), err
	}

	// declining leaves everything alone
	out, err := run("n\n", "setup")
	if err != nil || !strings.Contains(out, "hooks    skipped") {
		t.Fatalf("setup declined: %v\n%s", err, out)
	}
	if cur := snapshot(t, root, filepath.Join(home, ".claude")); !equalMaps(cur, before) {
		t.Fatal("declined setup must not touch files")
	}

	out, err = run("", "setup", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	// absorption of the global rules is unconditional (spec §3.6): the declined run already imported them
	if !strings.Contains(out, "hooks    installed") || !strings.Contains(out, "claude CLI not found") || !strings.Contains(out, "personal layer") {
		t.Fatalf("setup output:\n%s", out)
	}
	merged, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(merged), "/usr/local/bin/lint.sh") || !strings.Contains(string(merged), "keelage hook claude-code PreToolUse") || !strings.Contains(string(merged), `"model": "opus"`) {
		t.Fatalf("settings after setup:\n%s", merged)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".keelage", "backup")); len(entries) != 1 {
		t.Fatal("setup must back up settings.json once")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")); string(b) != "Always answer in Korean.\n" {
		t.Fatal("global rules file must not be rewritten")
	}

	out, err = run("", "status", "--repo", root)
	if err != nil || !strings.Contains(out, "hooks    installed") || !strings.Contains(out, "observe mode") || !strings.Contains(out, "daemon   not running") {
		t.Fatalf("status before init: %v\n%s", err, out)
	}

	out, err = run("", "init", "--repo", root, "--dry-run")
	if err != nil || !strings.Contains(out, "dry run") || !strings.Contains(out, "render  block     CLAUDE.md") {
		t.Fatalf("init dry-run: %v\n%s", err, out)
	}
	if cur := snapshot(t, root); !equalMaps(cur, filterPrefix(before, root)) {
		t.Fatal("dry run must not write")
	}

	out, err = run("", "init", "--repo", root, "--yes", "--verify")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, want := range []string{"absorb  imported  rule CLAUDE.md", "absorb  imported  rule pkg/CLAUDE.md", "absorb  imported  adr  docs/adr/0001-unit-is-change.md", "render  written   .keelage/context/claude-code/context.md", "render  written   CLAUDE.md", "gitignore .keelage/context/ added", "sidecar .keelage/manifest.json written"} {
		if !strings.Contains(out, want) {
			t.Fatalf("init lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "README.md") {
		t.Fatalf("docs/adr/README.md is not an ADR:\n%s", out)
	}
	claude, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if !strings.HasPrefix(string(claude), "# Project rules\n\nRun `make test` before committing.\n") || !strings.Contains(string(claude), "<!-- keelage:begin context ") || !strings.Contains(string(claude), "@.keelage/context/claude-code/context.md") {
		t.Fatalf("CLAUDE.md after init:\n%s", claude)
	}
	ctxMD, _ := os.ReadFile(filepath.Join(root, ".keelage", "context", "claude-code", "context.md"))
	if strings.Contains(string(ctxMD), "Run `make test`") {
		t.Fatalf("the repo's own rule must not be rendered back into it:\n%s", ctxMD)
	}
	if !strings.Contains(string(ctxMD), "Always answer in Korean.") || !strings.Contains(string(ctxMD), "0001. Unit is change (verified)") {
		t.Fatalf("personal rule and the ADR must be rendered:\n%s", ctxMD)
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(gi) != "/bin/\n.keelage/context/\n" {
		t.Fatalf(".gitignore: %q", gi)
	}
	out, _ = run("", "constraint", "list", "--repo", root)
	if strings.Count(out, "verified") < 3 || !strings.Contains(out, "path=pkg/**") {
		t.Fatalf("constraints after init:\n%s", out)
	}

	// idempotent
	out, err = run("", "init", "--repo", root, "--yes", "--verify")
	if err != nil || !strings.Contains(out, "absorb  unchanged rule CLAUDE.md") || !strings.Contains(out, "render  unchanged CLAUDE.md") {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	// a changed rule file supersedes its constraint
	write("pkg/CLAUDE.md", "Keep pkg dependency-free. Prefer stdlib.\n")
	out, _ = run("", "init", "--repo", root, "--yes", "--verify")
	if !strings.Contains(out, "absorb  updated   rule pkg/CLAUDE.md") {
		t.Fatalf("updated rule:\n%s", out)
	}
	out, _ = run("", "constraint", "list", "--repo", root)
	if !strings.Contains(out, "retired") || !strings.Contains(out, "Prefer stdlib") {
		t.Fatalf("supersede after edit:\n%s", out)
	}
	// divergence: an edit inside the managed block is reported, not overwritten
	tampered := strings.Replace(string(claude), "@.keelage/context/claude-code/context.md", "@.keelage/context/claude-code/context.md\nmy note", 1)
	write("CLAUDE.md", tampered)
	out, _ = run("", "status", "--repo", root)
	if !strings.Contains(out, "diverged  CLAUDE.md") {
		t.Fatalf("status must report divergence:\n%s", out)
	}
	out, _ = run("", "init", "--repo", root, "--yes")
	if !strings.Contains(out, "render  diverged  CLAUDE.md") {
		t.Fatalf("init must not overwrite a diverged block:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); !strings.Contains(string(b), "my note") {
		t.Fatal("diverged block overwritten")
	}
	out, _ = run("", "init", "--repo", root, "--yes", "--force")
	if !strings.Contains(out, "render  written   CLAUDE.md") {
		t.Fatalf("--force:\n%s", out)
	}

	// uninstall: every user file byte-identical, keelage's files gone, ledger kept
	write("pkg/CLAUDE.md", "Keep pkg dependency-free.\n") // back to the original content
	out, err = run("", "uninstall", "--yes")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	after := snapshot(t, root, filepath.Join(home, ".claude"))
	if !equalMaps(after, before) {
		t.Fatalf("round trip differs:\nbefore %v\nafter  %v\n%s", before, after, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".keelage")); !os.IsNotExist(err) {
		t.Fatal(".keelage must be gone")
	}
	if _, err := os.Stat(filepath.Join(home, ".keelage", "ledger.db")); err != nil {
		t.Fatal("the ledger must survive uninstall without --purge")
	}
	out, _ = run("", "status", "--repo", root)
	if !strings.Contains(out, "hooks    not installed") || !strings.Contains(out, "observe mode") {
		t.Fatalf("status after uninstall:\n%s", out)
	}
	out, err = run("", "uninstall", "--yes", "--purge")
	if err != nil || !strings.Contains(out, "purged") {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".keelage")); !os.IsNotExist(err) {
		t.Fatal("purge must delete the home")
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func filterPrefix(m map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out
}
