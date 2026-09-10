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
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/core/supply"
)

func hooksFixture(t *testing.T) (*Hooks, string) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "src", "vault"), 0o755)
	repo := RepoID(root)
	anchors, constraints := NewAnchorIndex(), NewConstraintIndex()
	ctx := context.Background()
	feed := func(evs ...core.Event) {
		for _, e := range evs {
			_ = anchors.Handle(ctx, core.Envelope{}, e)
			_ = constraints.Handle(ctx, core.Envelope{}, e)
		}
	}
	feed(
		realization.AnchorRecorded{Key: "code://src/calc.ts#fee", Hash: core.Hash3{Signature: "s", Body: "b", File: "f"}, At: t0},
		realization.AnchorRecorded{Key: "code://src/calc.ts#Ledger.add", Hash: core.Hash3{Signature: "s", Body: "b", File: "f"}, At: t0},
		realization.AnchorStaled{Key: "code://src/calc.ts#Ledger.add", State: realization.StateStale, Cause: core.HashSignature, At: t0},
		harness.ConstraintDrafted{ID: "c-billing", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/*"}, Body: "no retries in billing", Explanation: "double charges", Authored: true, Anchors: []string{"code://src/calc.ts#fee"}, At: t0},
		harness.ConstraintVerified{ID: "c-billing", At: t0},
		harness.ConstraintDrafted{ID: "c-draft", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo}, Body: "unverified draft", Authored: true, At: t0},
		harness.ConstraintDrafted{ID: "c-vault", ConstraintKind: harness.KindAutonomy, Level: core.L0, Checkable: supply.DenyEditMarker, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/vault/*"}, Body: "vault code is edited by humans", Authored: true, At: t0},
		harness.ConstraintVerified{ID: "c-vault", At: t0},
		harness.ConstraintDrafted{ID: "c-file", ConstraintKind: harness.KindInvariant, Scope: core.ScopeKey{Team: "core"}, Body: "calc.ts stays pure", Authored: true, Anchors: []string{"code://src/calc.ts"}, At: t0},
		harness.ConstraintVerified{ID: "c-file", At: t0},
		harness.ConstraintDrafted{ID: "c-path", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Repo: repo, PathGlob: "src/*"}, Body: "src is TypeScript strict", Authored: true, At: t0},
		harness.ConstraintVerified{ID: "c-path", At: t0},
	)
	return NewHooks(anchors, constraints, memory.NewClock(t0)), root
}

func TestHooks_PreEdit(t *testing.T) {
	h, root := hooksFixture(t)
	ctx := context.Background()
	ev := supply.Event{Kind: supply.SessionStart, Tool: "claude-code", SessionID: "s1", CWD: filepath.Join(root, "src")}
	if r, err := h.Hook(ctx, ev); err != nil || !r.Empty() {
		t.Fatalf("session start: %v %+v", err, r)
	}
	ev.Kind, ev.Files = supply.PreEdit, []string{filepath.Join(root, "src", "calc.ts")}
	r, err := h.Hook(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"keelage · src/calc.ts:", "anchor code://src/calc.ts#fee is recorded", "anchor code://src/calc.ts#Ledger.add is stale",
		"rule (verified, " + RepoID(root) + ":src/*): no retries in billing — double charges", "invariant (verified, team core): calc.ts stays pure", "src is TypeScript strict"} {
		if !strings.Contains(r.Inject, want) {
			t.Errorf("inject lacks %q:\n%s", want, r.Inject)
		}
	}
	if strings.Contains(r.Inject, "unverified draft") {
		t.Error("generated constraints are not injected")
	}
	if len(r.Warn) != 1 || !strings.Contains(r.Warn[0], "Ledger.add is stale") {
		t.Errorf("warn: %v", r.Warn)
	}
	if r.Block != nil {
		t.Error("no block on a rule")
	}
	// wider scope first
	if strings.Index(r.Inject, "team core") > strings.Index(r.Inject, "no retries") {
		t.Errorf("wider constraint must come first:\n%s", r.Inject)
	}
	// deny-edit autonomy constraint blocks agent edits in its scope
	ev.Files = []string{filepath.Join(root, "src", "vault", "keys.ts")}
	r, _ = h.Hook(ctx, ev)
	if r.Block == nil || !strings.Contains(r.Block.Reason, "vault code is edited by humans") {
		t.Fatalf("deny-edit: %+v", r)
	}
	// nothing known → empty
	ev.Files = []string{filepath.Join(root, "README.md")}
	if r, _ = h.Hook(ctx, ev); !r.Empty() {
		t.Fatalf("empty expected: %+v", r)
	}
	// outside the repo → ignored
	ev.Files = []string{"/elsewhere/x.ts"}
	if r, _ = h.Hook(ctx, ev); !r.Empty() {
		t.Fatalf("outside repo: %+v", r)
	}
}

func TestHooks_SessionsAndPrompt(t *testing.T) {
	h, root := hooksFixture(t)
	ctx := context.Background()
	base := supply.Event{Tool: "claude-code", SessionID: "s2", CWD: root}
	ev := base
	ev.Kind, ev.Prompt = supply.Prompt, "secret plan"
	if _, err := h.Hook(ctx, ev); err != nil {
		t.Fatal(err)
	}
	ev = base
	ev.Kind, ev.Files = supply.PostEdit, []string{filepath.Join(root, "src", "calc.ts")}
	_, _ = h.Hook(ctx, ev)
	_, _ = h.Hook(ctx, ev)
	ev = base
	ev.Kind, ev.Reason = supply.SessionEnd, "other"
	_, _ = h.Hook(ctx, ev)
	ss := h.Sessions()
	if len(ss) != 1 || ss[0].Prompts != 1 || len(ss[0].Touched) != 1 || ss[0].Touched[0] != "src/calc.ts" || !ss[0].Ended || ss[0].Root != root {
		t.Fatalf("session: %+v", ss)
	}
	if strings.Contains(strings.Join(ss[0].Touched, ""), "secret") {
		t.Fatal("prompt text must not be kept")
	}
	if _, err := h.Hook(ctx, supply.Event{Kind: supply.PreEdit}); err == nil {
		t.Fatal("session id required")
	}
	if _, err := h.Hook(ctx, supply.Event{Kind: "weird", SessionID: "x"}); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestHooks_Queries(t *testing.T) {
	h, root := hooksFixture(t)
	ctx := context.Background()
	repo := RepoID(root)
	tch, err := h.WhatTouches(ctx, "code://src/calc.ts#fee", repo)
	if err != nil || tch.State != "recorded" || len(tch.Constraints) != 3 {
		t.Fatalf("what_touches: %v %+v", err, tch)
	}
	tch, _ = h.WhatTouches(ctx, "code://src/calc.ts#fee", "")
	if len(tch.Constraints) != 2 { // bound directly + via the file anchor; the path-scoped one needs the repo id
		t.Fatalf("without repo: %+v", tch.Constraints)
	}
	if _, err := h.WhatTouches(ctx, "nope", repo); err == nil {
		t.Fatal("bad anchor")
	}
	rel, err := h.Related(ctx, "c-billing")
	if err != nil || rel.Kind != "constraint" || len(rel.Anchors) != 1 || rel.State != "verified" {
		t.Fatalf("related constraint: %v %+v", err, rel)
	}
	rel, _ = h.Related(ctx, "code://src/calc.ts#Ledger.add")
	if rel.Kind != "anchor" || rel.State != "stale" || len(rel.Constraints) != 1 || rel.Constraints[0] != "c-file" {
		t.Fatalf("related anchor: %+v", rel)
	}
	if _, err := h.Related(ctx, "???"); err == nil {
		t.Fatal("unknown id")
	}
	// the budget is honoured between files
	cctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	if _, err := h.Hook(cctx, supply.Event{Kind: supply.PreEdit, SessionID: "s9", CWD: root, Files: []string{filepath.Join(root, "src", "calc.ts")}}); err == nil {
		t.Fatal("expired deadline must surface")
	}
}
