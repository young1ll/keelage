package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/adapter/memory"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/supply"
	"github.com/young1ll/keelage/internal/port"
)

type captureFixture struct {
	fixture
	sessions *SessionIndex
	anchors  *AnchorIndex
	hooks    *Hooks
	root     string
}

func newCapture(t *testing.T) captureFixture {
	t.Helper()
	f := newFixture(t)
	c := captureFixture{fixture: f, sessions: NewSessionIndex(), anchors: NewAnchorIndex()}
	c.p.o.Projectors = append(c.p.o.Projectors, c.sessions, c.anchors)
	c.root = t.TempDir()
	_ = os.MkdirAll(filepath.Join(c.root, ".git"), 0o755)
	c.hooks = NewHooks(c.anchors, c.constraints, c.clock).WithCapture(c.p, "alice")
	return c
}

func (c captureFixture) ev(kind supply.EventKind) supply.Event {
	return supply.Event{Kind: kind, Tool: "claude-code", SessionID: "s1", CWD: c.root, At: c.clock.Now()}
}

func (c captureFixture) hook(t *testing.T, ev supply.Event) {
	t.Helper()
	if _, err := c.hooks.Hook(context.Background(), ev); err != nil {
		t.Fatalf("%s: %v", ev.Kind, err)
	}
}

// A session: prompt → edit a.ts → prompt "아니, …" (redirect) → edit b.ts →
// git checkout (revert) → prompt → edit c.ts → end (accept).
func TestCapture_TurnsAndSession(t *testing.T) {
	c := newCapture(t)
	abs := func(rel string) string { return filepath.Join(c.root, rel) }
	c.hook(t, c.ev(supply.SessionStart))
	c.hook(t, c.ev(supply.SessionStart)) // resume: idempotent
	p := c.ev(supply.Prompt)
	p.Prompt = "add a cap to fee"
	c.hook(t, p)
	e := c.ev(supply.PostEdit)
	e.Files = []string{abs("src/a.ts")}
	c.hook(t, e)
	p.Prompt = "아니, 캐시 말고 파라미터로"
	c.hook(t, p)
	e.Files = []string{abs("src/b.ts")}
	c.hook(t, e)
	r := c.ev(supply.ToolResult)
	r.ToolName, r.Command = "Bash", "git checkout -- src/b.ts"
	c.hook(t, r)
	r.Command = "git status"
	c.hook(t, r) // not a revert
	p.Prompt = "ok now add tests"
	c.hook(t, p)
	e.Files = []string{abs("src/c.ts")}
	c.hook(t, e)
	end := c.ev(supply.SessionEnd)
	end.Reason = "other"
	c.hook(t, end)

	s, ok := c.sessions.Get(accountability.SessionStream("claude-code", "s1"))
	if !ok {
		t.Fatal("session not projected")
	}
	if s.State != accountability.SessionStateEnded || s.Turns != 3 || len(s.Touched) != 3 || s.Repo != RepoID(c.root) || s.Owner != "alice" {
		t.Fatalf("session: %+v", s)
	}
	if len(s.Judgments) != 2 || s.Judgments[0].Class != accountability.TurnRedirect || s.Judgments[0].Signal == "" || s.Judgments[1].Class != accountability.TurnRevert {
		t.Fatalf("judgments: %+v", s.Judgments)
	}
	if s.Judgments[0].Files[0] != "src/a.ts" || s.Judgments[1].Files[0] != "src/b.ts" {
		t.Fatalf("judgment files: %+v", s.Judgments)
	}
	if s.Summary != "claude-code session: 3 turns, 3 files, 2 judgment candidates" {
		t.Fatalf("summary: %q", s.Summary)
	}
	// the ledger holds no prompt text
	all, _ := c.ledger.ReadAll(context.Background(), 0, 0)
	for _, env := range all {
		if containsBytes(env.Body, "캐시") || containsBytes(env.Body, "add a cap") {
			t.Fatalf("prompt text leaked into the ledger: %s", env.Body)
		}
	}
	// the agent actor is owned by the user
	if all[0].Actor.Kind != core.ActorAgent || all[0].Actor.Owner != "alice" || all[0].Actor.ID != "claude-code:s1" {
		t.Fatalf("actor: %+v", all[0].Actor)
	}
	// sessions touching a file within the window
	if got := c.sessions.Touching(RepoID(c.root), []string{"src/c.ts"}, t0.Add(-time.Hour)); len(got) != 1 {
		t.Fatalf("touching: %v", got)
	}
	if got := c.sessions.Touching(RepoID(c.root), []string{"src/zzz.ts"}, t0.Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("touching none: %v", got)
	}
	if got := c.sessions.Touching("other-repo", []string{"src/c.ts"}, t0.Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("touching other repo: %v", got)
	}
	// ending twice / a prompt after the end are harmless
	c.hook(t, end)
	items := Inbox(c.changes, c.anchors, c.constraints, c.sessions)
	if len(items) != 1 || items[0].Kind != "session" || items[0].Cause != "2 judgment candidate(s) from 3 turns" {
		t.Fatalf("inbox: %+v", items)
	}
}

func containsBytes(b []byte, s string) bool { return strings.Contains(string(b), s) }

// fake commits: a two-commit history in memory
type fakeCommits struct {
	commits map[string]port.Commit
	changed map[string][]port.FileChange
	content map[string]map[string][]byte
}

func (f fakeCommits) Commit(_ context.Context, ref string) (port.Commit, error) {
	c, ok := f.commits[ref]
	if !ok {
		return port.Commit{}, os.ErrNotExist
	}
	return c, nil
}
func (f fakeCommits) ChangedIn(_ context.Context, sha string) ([]port.FileChange, error) {
	return f.changed[sha], nil
}
func (f fakeCommits) Content(_ context.Context, sha, path string) ([]byte, bool, error) {
	b, ok := f.content[sha][path]
	return b, ok, nil
}

func TestDeriver_GenericAndSettle(t *testing.T) {
	c := newCapture(t)
	ctx := context.Background()
	repo := RepoID(c.root)
	commits := fakeCommits{
		commits: map[string]port.Commit{
			"c1": {SHA: "c1", AuthorEmail: "a@x", Message: "init", At: t0},
			"c2": {SHA: "c2", Parents: []string{"c1"}, AuthorEmail: "a@x", Message: "touch billing", At: t0.Add(time.Hour)},
		},
		changed: map[string][]port.FileChange{
			"c1": {{Path: "README.md", Status: "A"}},
			"c2": {{Path: "src/billing.go", Status: "M"}, {Path: "docs/x.md", Status: "A"}},
		},
		content: map[string]map[string][]byte{},
	}
	d := &Deriver{Pipeline: c.p, Commits: commits, Constraints: c.constraints, Sessions: c.sessions, Changes: c.changes, IDs: &memory.IDGen{Prefix: "ch"}, Repo: repo}

	// no constraints anywhere: settles at once
	out, err := d.Derive(ctx, alice, "c1")
	if err != nil || !out.Settled || out.Replayed || len(out.Anchors) != 1 || out.Anchors[0] != "code://README.md" {
		t.Fatalf("c1: %+v %v", out, err)
	}
	if ch, _ := c.changes.Get(out.ChangeID); ch.State != "settled" || ch.Mode != "manual" || ch.Refs[0] != "c1" {
		t.Fatalf("c1 change: %+v", ch)
	}
	again, err := d.Derive(ctx, alice, "c1")
	if err != nil || !again.Replayed || again.ChangeID != out.ChangeID {
		t.Fatalf("replay: %+v %v", again, err)
	}

	// a verified constraint on src/* → the change waits for a judgment and bundles the session
	c.must(t, alice, harness.DraftConstraint{ID: "c-billing", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/*"}, Body: "billing is reviewed", Authored: true})
	c.must(t, alice, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c-billing"}})
	c.hook(t, c.ev(supply.SessionStart))
	e := c.ev(supply.PostEdit)
	e.Files = []string{filepath.Join(c.root, "src/billing.go")}
	c.hook(t, e)
	end := c.ev(supply.SessionEnd)
	c.hook(t, end)
	out, err = d.Derive(ctx, alice, "c2")
	if err != nil || out.Settled || len(out.Constraints) != 1 || out.Constraints[0] != "c-billing" || len(out.Sessions) != 1 {
		t.Fatalf("c2: %+v %v", out, err)
	}
	if ch, _ := c.changes.Get(out.ChangeID); ch.State != "proposed" || ch.Mode != "session" || len(ch.Anchors) != 2 {
		t.Fatalf("c2 change: %+v", ch)
	}
	items := Inbox(c.changes, c.anchors, c.constraints, c.sessions)
	found := false
	for _, it := range items {
		if it.Kind == "change" && it.ID == string(out.ChangeID) && it.State == "proposed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inbox lacks the proposed change: %+v", items)
	}
	// history of the touched file anchor mentions the change
	entries, err := History(ctx, c.ledger, c.p.o.Codec, "code://src/billing.go")
	if err != nil || len(entries) != 1 || entries[0].Kind != "ChangeOpened" {
		t.Fatalf("history: %+v %v", entries, err)
	}
	if _, err := d.Derive(ctx, alice, "nope"); err == nil {
		t.Fatal("unknown commit")
	}
}
