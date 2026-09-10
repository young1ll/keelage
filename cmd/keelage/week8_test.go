package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
)

// e2e for week 8: commits become Changes (settled outside the harness,
// proposed inside it), symbol-level anchors from the diff, inbox/history/
// sessions output.
func TestDerive_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("KEELAGE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "derive-e2e")
	_ = os.MkdirAll(root, 0o755)
	gitc := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = root
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		c := rootCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(append(args, "--repo", root))
		err := c.ExecuteContext(context.Background())
		return out.String(), err
	}
	gitc("init", "-q")
	gitc("config", "user.email", "dev@example.com")
	gitc("config", "user.name", "dev")
	const calc = "// pricing\nexport function fee(a: number, r: number): number {\n  return a * r;\n}\nexport function tax(a: number): number {\n  return a * 0.1;\n}\n"
	write("src/calc.ts", calc)
	write("README.md", "hello\n")
	gitc("add", ".")
	gitc("commit", "-q", "-m", "init")

	// first commit: no constraints → settled outside the harness
	out, err := run("change", "derive")
	if err != nil || !strings.Contains(out, "settled (outside the harness)") || !strings.Contains(out, "anchor code://src/calc.ts#fee") {
		t.Fatalf("derive init: %v\n%s", err, out)
	}

	// a constraint bound to fee; edit tax's body (not fee) and a comment
	ctx := context.Background()
	h, err := openHeadless(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Base(root)
	if _, err := h.pipeline.Handle(ctx, h.actor, harness.DraftConstraint{ID: "c-fee", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo}, Body: "fee never exceeds the amount", Authored: true, Anchors: []string{"code://src/calc.ts#fee"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pipeline.Handle(ctx, h.actor, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c-fee"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.verifier.Record(ctx, h.actor, "code://src/calc.ts#fee", "init"); err != nil {
		t.Fatal(err)
	}
	h.close(ctx)

	write("src/calc.ts", strings.Replace(strings.Replace(calc, "return a * 0.1;", "return a * 0.2;", 1), "// pricing", "// prices", 1))
	gitc("commit", "-q", "-am", "tax 20%")
	out, err = run("change", "derive", "--json")
	if err != nil {
		t.Fatalf("derive tax: %v\n%s", err, out)
	}
	var d struct {
		Anchors     []string
		Constraints []string
		Settled     bool
	}
	_ = json.Unmarshal([]byte(out), &d)
	if len(d.Anchors) != 1 || d.Anchors[0] != "code://src/calc.ts#tax" || !d.Settled || len(d.Constraints) != 0 {
		t.Fatalf("only tax changed and no constraint binds it: %+v", d)
	}

	// now change fee's signature → the constraint is referenced → proposed, anchor stale
	write("src/calc.ts", strings.Replace(calc, "fee(a: number, r: number): number", "fee(a: number, r: number, cap?: number): number", 1))
	gitc("commit", "-q", "-am", "fee cap")
	sha := gitc("rev-parse", "HEAD")
	out, err = run("change", "derive")
	if err != nil || !strings.Contains(out, "proposed (needs a judgment)") || !strings.Contains(out, "constraint c-fee") {
		t.Fatalf("derive fee: %v\n%s", err, out)
	}
	again, _ := run("change", "derive", "--commit", sha)
	if !strings.Contains(again, "already derived") {
		t.Fatalf("idempotent: %s", again)
	}

	out, _ = run("inbox")
	if !strings.Contains(out, "change") || !strings.Contains(out, "needs a judgment") || !strings.Contains(out, "code://src/calc.ts#fee") || !strings.Contains(out, "stale") {
		t.Fatalf("inbox:\n%s", out)
	}
	out, _ = run("history", "code://src/calc.ts#fee")
	for _, want := range []string{"AnchorRecorded", "ConstraintDrafted", "ChangeOpened", "AnchorStaled", "AnchorVerified"} {
		if !strings.Contains(out, want) {
			t.Fatalf("history lacks %s:\n%s", want, out)
		}
	}
	out, _ = run("change", "list")
	if strings.Count(out, "\n") < 4 || !strings.Contains(out, "proposed") || !strings.Contains(out, "settled") {
		t.Fatalf("change list:\n%s", out)
	}
	out, _ = run("sessions")
	if !strings.Contains(out, "SESSION") {
		t.Fatalf("sessions header:\n%s", out)
	}
	snippet := rootCmd()
	var buf bytes.Buffer
	snippet.SetOut(&buf)
	snippet.SetArgs([]string{"adapter", "git", "print", "post-commit"})
	if err := snippet.ExecuteContext(context.Background()); err != nil || !strings.HasPrefix(buf.String(), "#!/bin/sh") || !strings.Contains(buf.String(), "keelage change derive") {
		t.Fatalf("post-commit snippet: %v\n%s", err, buf.String())
	}
}
