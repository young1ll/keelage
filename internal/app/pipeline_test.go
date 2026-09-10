package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/adapter/memory"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/port"
)

var (
	t0    = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	alice = core.ActorRef{Kind: core.ActorHuman, ID: "alice"}
	agent = core.ActorRef{Kind: core.ActorAgent, ID: "cc-1", Owner: "alice"}
	org   = core.ScopeKey{Org: "acme"}
	team  = core.ScopeKey{Org: "acme", Team: "core"}
	repo  = core.ScopeKey{Org: "acme", Team: "core", Repo: "acme/api"}
)

type fixture struct {
	p           *Pipeline
	ledger      *memory.Ledger
	clock       *memory.Clock
	scopes      *ScopeIndex
	constraints *ConstraintIndex
	changes     *ChangeIndex
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{
		ledger: memory.NewLedger(nil), clock: memory.NewClock(t0),
		scopes: NewScopeIndex(), constraints: NewConstraintIndex(), changes: NewChangeIndex(),
	}
	f.p = New(Options{
		Ledger: f.ledger, Codec: NewCodec(), Clock: f.clock,
		Context:    ProjectionContext{Scopes: f.scopes, Constraints: f.constraints},
		Projectors: []port.Projector{f.scopes, f.constraints, f.changes},
	})
	RegisterAll(f.p)
	return f
}

func (f fixture) must(t *testing.T, actor core.ActorRef, cmd core.Command) Result {
	t.Helper()
	r, err := f.p.Handle(context.Background(), actor, cmd)
	if err != nil {
		t.Fatalf("%s: %v", cmd.Kind(), err)
	}
	return r
}

func (f fixture) rejected(t *testing.T, actor core.ActorRef, cmd core.Command, wantCode string) {
	t.Helper()
	_, err := f.p.Handle(context.Background(), actor, cmd)
	r, ok := core.AsRejection(err)
	if !ok || r.Code != wantCode {
		t.Fatalf("%s: want rejection %q, got %v", cmd.Kind(), wantCode, err)
	}
}

func (f fixture) rejections(t *testing.T) []core.Rejected {
	t.Helper()
	envs, _ := f.ledger.Read(context.Background(), core.RejectedStream, 0)
	out := []core.Rejected{}
	for _, e := range envs {
		ev, err := f.p.o.Codec.Decode(e.Kind, e.V, e.Body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev.(core.Rejected))
	}
	return out
}

func TestPipeline_ConstraintLifecycle(t *testing.T) {
	f := newFixture(t)
	f.must(t, alice, harness.DraftConstraint{ID: "c1", Idem: "d1", ConstraintKind: harness.KindRule, Scope: repo, Body: "no retries in billing", Authored: true})
	if s, ok := f.constraints.Get("c1"); !ok || s.State != harness.StateGenerated {
		t.Fatalf("projection after draft: %+v %v", s, ok)
	}
	if f.constraints.SnapshotHash(repo) != "" {
		t.Fatal("generated constraints are not in force")
	}
	f.rejected(t, agent, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1", Idem: ""}}, "human-only")
	r := f.must(t, alice, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1", Idem: "v1"}})
	if r.Version != 2 || len(r.Events) != 1 {
		t.Fatalf("verify result %+v", r)
	}
	if in := f.constraints.InForce(repo); len(in) != 1 || in[0].ID != "c1" {
		t.Fatalf("in force: %v", in)
	}
	if f.constraints.InForce(core.ScopeKey{Org: "acme", Team: "web"}) != nil && len(f.constraints.InForce(core.ScopeKey{Org: "acme", Team: "web"})) != 0 {
		t.Fatal("constraint must not apply to another team")
	}
	// the rejection was recorded, not swallowed
	rej := f.rejections(t)
	if len(rej) != 1 || rej[0].Code != "human-only" || rej[0].Command != "VerifyConstraint" || rej[0].Actor.ID != "cc-1" {
		t.Fatalf("rejections: %+v", rej)
	}
}

func TestPipeline_ChangeFlowWithMeta(t *testing.T) {
	f := newFixture(t)
	f.must(t, alice, harness.DraftConstraint{ID: "c1", ConstraintKind: harness.KindRule, Scope: team, Body: "b", Authored: true})
	f.must(t, alice, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1", Idem: ""}})
	snapshot := f.constraints.SnapshotHash(repo)

	imp := accountability.Impact{Constraints: []accountability.ImpactRef{{Constraint: "c1", Relation: accountability.RelReferences}}, Anchors: []string{"code://src/fee.ts#calc"}}
	open := accountability.OpenChange{ChangeKind: accountability.KindRealization, Mode: accountability.ModeSession, Scope: repo, IntentRef: "i1", Impact: imp}
	open.ID, open.Idem = "ch1", "open-1"
	r := f.must(t, agent, open)
	if r.Version != 1 || r.Range.FromSeq != 3 {
		t.Fatalf("open result %+v", r)
	}
	// event meta carries the snapshot, the autonomy and the anchors
	envs, _ := f.ledger.Read(context.Background(), accountability.ChangeStream("ch1"), 0)
	if m := envs[0].Meta; m.ConstraintsHash != snapshot || m.Autonomy != core.L0 || len(m.Refs) != 2 || m.Refs[1] != "code://src/fee.ts#calc" {
		t.Fatalf("meta: %+v (snapshot %s)", m, snapshot)
	}
	if envs[0].Idem != "open-1" || envs[0].Actor.ID != "cc-1" {
		t.Fatalf("envelope: %+v", envs[0])
	}
	// idempotent replay
	again := f.must(t, agent, open)
	if !again.Replayed || again.Version != 1 {
		t.Fatalf("replay: %+v", again)
	}
	if h, _ := f.ledger.Head(context.Background()); h.Seq != 3 {
		t.Fatalf("replay wrote something: head %d", h.Seq)
	}
	// at L0 the agent only proposes
	j := accountability.Judgment{ID: "j1", Decision: accountability.DecisionAccept, Reason: "ok", By: agent, At: t0, Source: accountability.SourceHarness}
	f.rejected(t, agent, accountability.Judge{ChangeCmd: accountability.ChangeCmd{ID: "ch1", Idem: ""}, Judgment: j}, "autonomy")
	j.By = alice
	f.must(t, alice, accountability.Judge{ChangeCmd: accountability.ChangeCmd{ID: "ch1", Idem: ""}, Judgment: j})
	f.must(t, alice, accountability.Deploy{ChangeCmd: accountability.ChangeCmd{ID: "ch1", Idem: ""}, Ref: "deploy-42"})
	f.rejected(t, alice, accountability.Settle{ChangeCmd: accountability.ChangeCmd{ID: "ch1", Idem: ""}}, "observing")
	f.clock.Advance(core.DefaultPolicy().OutcomeWindow + time.Minute)
	f.must(t, alice, accountability.Settle{ChangeCmd: accountability.ChangeCmd{ID: "ch1", Idem: ""}})
	if s, _ := f.changes.Get("ch1"); s.State != accountability.StateSettled || s.Judgments != 1 || s.AutonomyApplied != core.L0 {
		t.Fatalf("change projection %+v", s)
	}
	if len(f.changes.Open()) != 0 {
		t.Fatal("settled change still open")
	}
	all, _ := f.ledger.ReadAll(context.Background(), 0, 0)
	if _, err := core.VerifyChain(nil, all); err != nil {
		t.Fatal(err)
	}
}

func TestPipeline_AutonomyAndGates(t *testing.T) {
	f := newFixture(t)
	ev := harness.Evidence{Since: t0.Add(-48 * time.Hour), Outcomes: 20}
	f.rejected(t, agent, harness.SetAutonomy{ScopeCmd: harness.ScopeCmd{Key: team, Idem: ""}, Level: core.L1, ChangeRef: "ch-h1", Evidence: ev}, "human-only")
	f.must(t, alice, harness.SetAutonomy{ScopeCmd: harness.ScopeCmd{Key: team, Idem: ""}, Level: core.L1, ChangeRef: "ch-h1", Evidence: ev})
	if l, c := f.scopes.Effective(repo); l != core.L1 || c != core.MaxLevel {
		t.Fatalf("effective after raise: %s %s", l, c)
	}
	// a wider cap binds the narrower level
	f.must(t, alice, harness.SetCap{ScopeCmd: harness.ScopeCmd{Key: org, Idem: ""}, Cap: core.L0, ChangeRef: "ch-h2"})
	if l, c := f.scopes.Effective(repo); l != core.L0 || c != core.L0 {
		t.Fatalf("effective under org cap: %s %s", l, c)
	}
	f.must(t, alice, harness.SetCap{ScopeCmd: harness.ScopeCmd{Key: org, Idem: ""}, Cap: core.L2, ChangeRef: "ch-h3"})
	if l, c := f.scopes.Effective(repo); l != core.L1 || c != core.L2 {
		t.Fatalf("effective after cap lift: %s %s", l, c)
	}
	if l, _ := f.scopes.Effective(core.ScopeKey{Org: "acme", Team: "web"}); l != core.L0 {
		t.Fatal("other team must stay L0")
	}

	// gates: allow at level, ask above, deny above cap; same proposal → same gate
	ask := func(level core.Level, proposal string) accountability.Gate {
		id := accountability.GateIDFor(agent, proposal)
		req := accountability.RequestGate{Actor: agent, Scope: repo, ProposalRef: proposal, RequestedLevel: level, Anchors: []string{"code://src/a.ts#f"}}
		req.ID = id
		f.must(t, agent, req)
		envs, _ := f.ledger.Read(context.Background(), accountability.GateStream(id), 0)
		g := accountability.Gate{}
		for _, e := range envs {
			evt, _ := f.p.o.Codec.Decode(e.Kind, e.V, e.Body)
			g = g.Apply(evt)
		}
		return g
	}
	if g := ask(core.L1, "p-allow"); g.Decision != accountability.GateAllow {
		t.Fatalf("allow: %+v", g)
	}
	if g := ask(core.L3, "p-deny"); g.Decision != accountability.GateDeny {
		t.Fatalf("deny: %+v", g)
	}
	g := ask(core.L2, "p-ask")
	if g.Decision != accountability.GateAsk || g.State != accountability.GateStateOpen {
		t.Fatalf("ask: %+v", g)
	}
	if again := ask(core.L2, "p-ask"); again.OpenedAt != g.OpenedAt {
		t.Fatal("re-request must return the same gate")
	}
	res := accountability.Resolution{Decision: accountability.GateAllow, Reason: "looked at it", By: alice}
	f.must(t, alice, accountability.ResolveGate{GateCmd: accountability.GateCmd{ID: g.ID, Idem: "r1"}, Resolution: res})
	if again := ask(core.L2, "p-ask"); again.State != accountability.GateStateResolved {
		t.Fatalf("resolved: %+v", again)
	}
}

func TestPipeline_ErrorsAndRejections(t *testing.T) {
	f := newFixture(t)
	_, err := f.p.Handle(context.Background(), alice, bogus{})
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("unknown: %v", err)
	}
	f.rejected(t, core.ActorRef{Kind: core.ActorAgent, ID: "no-owner"}, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1", Idem: ""}}, "unauthenticated")
	f.rejected(t, alice, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "missing", Idem: ""}}, "not-found")
	rej := f.rejections(t)
	if len(rej) != 2 || rej[0].Code != "unauthenticated" || rej[1].Code != "not-found" || rej[1].Target != "constraint/missing" {
		t.Fatalf("rejections: %+v", rej)
	}
	// a no-op decision (same level) writes nothing and is not an error
	f.must(t, alice, harness.SetAutonomy{ScopeCmd: harness.ScopeCmd{Key: team, Idem: "x"}, Level: core.L0})
	if h, _ := f.ledger.Head(context.Background()); h.Seq != 2 {
		t.Fatalf("no-op wrote: %d", h.Seq)
	}
}

type bogus struct{}

func (bogus) Kind() string           { return "Bogus" }
func (bogus) Stream() string         { return "x" }
func (bogus) IdempotencyKey() string { return "" }
