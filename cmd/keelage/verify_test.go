package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// row finds the report line for anchor and returns its columns
// (before, after, change, lang…) split on whitespace.
func row(out, anchor string) []string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == anchor {
			return f[1:]
		}
	}
	return nil
}

func expect(t *testing.T, out, anchor, before, after, change string) {
	t.Helper()
	f := row(out, anchor)
	if len(f) < 3 || f[0] != before || f[1] != after || f[2] != change {
		t.Fatalf("%s: want %s %s %s, got %v\n%s", anchor, before, after, change, f, out)
	}
}

// e2e: a temp repo with TypeScript and Go files, the personal ledger in a
// temp KEELAGE_HOME, and the real commands. Covers the staging rules end
// to end: file → verified, body → review, signature → stale, generic,
// move detection, idempotent re-run.
func TestVerify_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("KEELAGE_HOME", t.TempDir())
	repo := t.TempDir()
	gitc := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(repo, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitc("init", "-q")
	gitc("config", "user.email", "dev@example.com")
	gitc("config", "user.name", "dev")
	const calc = "// pricing\nexport function fee(a: number, r: number): number {\n  return a * r;\n}\nexport class Ledger {\n  add(n: number): number { return n + 1; }\n}\n"
	write("src/calc.ts", calc)
	write("main.go", "package main\n\nfunc main() {}\n")
	gitc("add", ".")
	gitc("commit", "-q", "-m", "init")

	run := func(args ...string) (string, error) {
		t.Helper()
		root := rootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append(args, "--repo", repo))
		err := root.ExecuteContext(context.Background())
		return out.String(), err
	}

	out, err := run("anchor", "add", "code://src/calc.ts#fee", "code://src/calc.ts#Ledger.add", "code://src/calc.ts", "code://main.go#main")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "typescript") || !strings.Contains(out, "degraded") {
		t.Fatalf("add output:\n%s", out)
	}
	if _, err := run("anchor", "add", "code://src/calc.ts#nope"); err == nil {
		t.Fatal("missing symbol must fail to record")
	}

	out, err = run("verify")
	if err != nil {
		t.Fatalf("clean verify: %v\n%s", err, out)
	}
	for _, a := range []string{"code://src/calc.ts#fee", "code://src/calc.ts#Ledger.add", "code://src/calc.ts", "code://main.go#main"} {
		expect(t, out, a, "recorded", "verified", "none")
	}

	// body edit + comment edit: fee → review, file anchor → review (surface body), Ledger.add → verified (file layer only)
	write("src/calc.ts", strings.Replace(strings.Replace(calc, "return a * r;", "return r * a;", 1), "// pricing", "// prices", 1))
	out, err = run("verify", "--changed", "--fail-on", "review")
	if err == nil {
		t.Fatalf("review must fail with --fail-on review:\n%s", out)
	}
	expect(t, out, "code://src/calc.ts#fee", "verified", "review", "body")
	expect(t, out, "code://src/calc.ts#Ledger.add", "verified", "verified", "file")
	expect(t, out, "code://src/calc.ts", "verified", "review", "body")
	if strings.Contains(out, "main.go") {
		t.Fatalf("--changed must skip untouched files:\n%s", out)
	}

	// signature edit → stale (default --fail-on stale exits non-zero)
	write("src/calc.ts", strings.Replace(calc, "fee(a: number, r: number): number", "fee(a: number, r: number, cap?: number): number", 1))
	out, err = run("verify", "--changed")
	if err == nil {
		t.Fatalf("stale must fail by default:\n%s", out)
	}
	expect(t, out, "code://src/calc.ts#fee", "review", "stale", "signature")

	// generic language: any change is a signature change
	write("main.go", "package main\n\nfunc main() { println(1) }\n")
	out, err = run("verify", "--changed", "--fail-on", "none")
	if err != nil {
		t.Fatalf("generic: %v\n%s", err, out)
	}
	expect(t, out, "code://main.go#main", "verified", "stale", "signature")
	if f := row(out, "code://main.go#main"); len(f) < 5 || f[3] != "generic" || f[4] != "(degraded)" {
		t.Fatalf("generic lang column: %v", f)
	}

	// move: Ledger goes to its own file; the same signature vanishes here and appears there
	gitc("checkout", "--", "src/calc.ts", "main.go")
	write("src/calc.ts", calc[:strings.Index(calc, "export class Ledger")])
	write("src/ledger.ts", "export class Ledger {\n  add(n: number): number { return n + 1; }\n}\n")
	out, err = run("verify", "--changed", "--fail-on", "none")
	if err != nil || !strings.Contains(out, "moved: code://src/calc.ts#Ledger.add → code://src/ledger.ts#Ledger.add") {
		t.Fatalf("move: %v\n%s", err, out)
	}
	expect(t, out, "code://src/calc.ts#Ledger.add", "verified", "unrealized", "missing")

	// idempotent at the same ref: the second run writes nothing new
	first, _ := run("anchor", "list")
	_, _ = run("verify", "--changed", "--fail-on", "none", "--json")
	second, _ := run("anchor", "list")
	if first != second {
		t.Fatalf("re-run changed state:\n%s\n---\n%s", first, second)
	}
}
