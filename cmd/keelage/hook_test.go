package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/supply"
)

var update = flag.Bool("update", false, "rewrite testdata/hooks/*.expected.jsonl")

// hook simulator (patterns §6): replay a recorded Claude Code hook sequence
// against a live daemon and compare stdout with the golden file.
func TestHookSimulator_ClaudeCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("KEELAGE_HOME", home)
	root := filepath.Join(t.TempDir(), "keelage-sim") // fixed base name = stable repo id in the golden
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitc := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitc("init", "-q")
	gitc("config", "user.email", "dev@example.com")
	gitc("config", "user.name", "dev")
	write("src/calc.ts", "export function fee(amount: number, rate: number): number {\n  return amount * rate;\n}\n")
	write("src/vault/keys.ts", "export const KEY = 'k';\n")
	write("README.md", "a\n")
	gitc("add", ".")
	gitc("commit", "-q", "-m", "init")

	// seed the charter with fixed ids so the golden file is stable
	ctx := context.Background()
	h, err := openHeadless(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Base(root)
	if _, err := h.verifier.Record(ctx, h.actor, "code://src/calc.ts#fee", "sim"); err != nil {
		t.Fatal(err)
	}
	must := func(cmd core.Command) {
		t.Helper()
		if _, err := h.pipeline.Handle(ctx, h.actor, cmd); err != nil {
			t.Fatal(err)
		}
	}
	must(harness.DraftConstraint{ID: "c-billing", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/*"}, Body: "no retries in billing", Explanation: "retries double-charged customers in 2025", Authored: true, Anchors: []string{"code://src/calc.ts#fee"}})
	must(harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c-billing"}})
	must(harness.DraftConstraint{ID: "c-vault", ConstraintKind: harness.KindAutonomy, Level: core.L0, Checkable: supply.DenyEditMarker, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/vault/*"}, Body: "vault code is edited by humans", Authored: true})
	must(harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c-vault"}})
	h.close(ctx)

	sock := filepath.Join(home, "keelage.sock")
	dctx, stop := context.WithCancel(ctx)
	defer stop()
	daemonDone := make(chan error, 1)
	go func() {
		c := daemonCmd()
		c.SetArgs([]string{"--socket", sock})
		c.SetOut(new(bytes.Buffer))
		daemonDone <- c.ExecuteContext(dctx)
	}()
	waitSocket(t, sock)

	in, err := os.ReadFile("../../testdata/hooks/claude-code/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	sc := bufio.NewScanner(bytes.NewReader(in))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.ReplaceAll(sc.Text(), "$ROOT", root)
		var rec struct {
			Event string          `json:"event"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad line: %v", err)
		}
		var out bytes.Buffer
		start := time.Now()
		runHook(ctx, bytes.NewReader(rec.Input), &out, "claude-code", rec.Event, sock)
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Errorf("%s took %v", rec.Event, d)
		}
		got = append(got, strings.TrimSpace(out.String()))
	}
	golden := "../../testdata/hooks/claude-code/session.expected.jsonl"
	if *update {
		if err := os.WriteFile(golden, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantRaw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	want := strings.Split(string(wantRaw), "\n")
	if n := len(want); n > 0 && want[n-1] == "" {
		want = want[:n-1] // the trailing newline
	}
	if len(want) != len(got) {
		t.Fatalf("golden has %d lines, got %d:\n%s", len(want), len(got), strings.Join(got, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got  %s\n want %s", i+1, got[i], want[i])
		}
	}
	// semantic spot checks independent of the golden text
	var edit map[string]any
	_ = json.Unmarshal([]byte(got[3]), &edit)
	hso, _ := edit["hookSpecificOutput"].(map[string]any)
	if hso == nil || !strings.Contains(hso["additionalContext"].(string), "no retries in billing") || hso["permissionDecision"] != nil {
		t.Fatalf("pre-edit inject: %s", got[3])
	}
	var vault map[string]any
	_ = json.Unmarshal([]byte(got[5]), &vault)
	if hso, _ := vault["hookSpecificOutput"].(map[string]any); hso == nil || hso["permissionDecision"] != "deny" {
		t.Fatalf("deny-edit: %s", got[5])
	}
	for _, i := range []int{0, 1, 2, 4, 6, 7, 8} {
		if got[i] != "" {
			t.Errorf("line %d should be silent: %s", i+1, got[i])
		}
	}

	// catch-up: a constraint added by the CLI while the daemon runs is
	// visible to the next hook without a restart (ADR 0012)
	addCmd := rootCmd()
	addOut := new(bytes.Buffer)
	addCmd.SetOut(addOut)
	addCmd.SetArgs([]string{"constraint", "add", "--repo", root, "--scope", "path=README.md", "--verify", "README stays in English"})
	if err := addCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("constraint add: %v", err)
	}
	var out bytes.Buffer
	readme := strings.ReplaceAll(`{"session_id":"sim-1","cwd":"$ROOT","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"$ROOT/README.md","old_string":"a","new_string":"b"}}`, "$ROOT", root)
	runHook(ctx, strings.NewReader(readme), &out, "claude-code", "PreToolUse", sock)
	if !strings.Contains(out.String(), "README stays in English") {
		t.Fatalf("daemon did not catch up on the CLI write: %q", out.String())
	}
	listCmd := rootCmd()
	listOut := new(bytes.Buffer)
	listCmd.SetOut(listOut)
	listCmd.SetArgs([]string{"constraint", "list", "--repo", root})
	if err := listCmd.ExecuteContext(ctx); err != nil || !strings.Contains(listOut.String(), strings.TrimSpace(addOut.String())) {
		t.Fatalf("constraint list: %v\n%s", err, listOut.String())
	}

	stop()
	select {
	case err := <-daemonDone:
		if err != nil {
			t.Fatalf("daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func waitSocket(t *testing.T, sock string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon socket did not appear")
}

// fail-open: no daemon, and a daemon that is too slow, both yield silence
// within the budget.
func TestHook_FailOpen(t *testing.T) {
	ctx := context.Background()
	input := `{"session_id":"s","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/a.ts"}}`

	var out bytes.Buffer
	start := time.Now()
	runHook(ctx, strings.NewReader(input), &out, "claude-code", "PreToolUse", filepath.Join(t.TempDir(), "missing.sock"))
	if out.Len() != 0 || time.Since(start) > 150*time.Millisecond {
		t.Fatalf("no daemon: out=%q took %v", out.String(), time.Since(start))
	}

	sock := filepath.Join(t.TempDir(), "slow.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{"inject":"too late"}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	out.Reset()
	start = time.Now()
	runHook(ctx, strings.NewReader(input), &out, "claude-code", "PreToolUse", sock)
	if out.Len() != 0 || time.Since(start) > 150*time.Millisecond {
		t.Fatalf("slow daemon: out=%q took %v", out.String(), time.Since(start))
	}

	// unknown tool / ignored event / garbage: silent
	for _, c := range []struct{ tool, event, in string }{{"cursor", "PreToolUse", input}, {"claude-code", "Notification", input}, {"claude-code", "PreToolUse", "{garbage"}} {
		out.Reset()
		runHook(ctx, strings.NewReader(c.in), &out, c.tool, c.event, sock)
		if out.Len() != 0 {
			t.Errorf("%+v: %q", c, out.String())
		}
	}
}
