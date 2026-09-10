package accountability

import (
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

var (
	t0     = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	human  = core.ActorRef{Kind: core.ActorHuman, ID: "alice"}
	bob    = core.ActorRef{Kind: core.ActorHuman, ID: "bob"}
	agent  = core.ActorRef{Kind: core.ActorAgent, ID: "cc-1", Owner: "alice"}
	team   = core.ScopeKey{Org: "acme", Team: "core"}
	impact = Impact{Constraints: []ImpactRef{{Constraint: "c1", Relation: RelReferences}}, Anchors: []string{"code://src/fee.ts#calc"}}
)

func dctx(actor core.ActorRef, level core.Level) core.DecideContext {
	return core.DecideContext{Now: t0, Actor: actor, Autonomy: level, Cap: core.MaxLevel, Policy: core.DefaultPolicy()}
}

func code(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	r, ok := core.AsRejection(err)
	if !ok {
		t.Fatalf("not a rejection: %v", err)
	}
	return r.Code
}

func opened(t *testing.T, kind ChangeKind, level core.Level, imp Impact) Change {
	t.Helper()
	cmd := OpenChange{ChangeCmd: NewChangeCmd("ch1", ""), ChangeKind: kind, Mode: ModeSession, Scope: team, IntentRef: "i1", Impact: imp}
	evs, err := (Change{}).Decide(cmd, dctx(human, level))
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(Change{}, evs)
}

func judgedBy(t *testing.T, c Change, by core.ActorRef, d JudgmentDecision, level core.Level) Change {
	t.Helper()
	j := Judgment{ID: core.ID("j-" + string(by.ID) + "-" + string(d)), Decision: d, Reason: "because", By: by, At: t0, Source: SourceReview}
	evs, err := c.Decide(Judge{NewChangeCmd("ch1", ""), j}, dctx(by, level))
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(c, evs)
}

func TestChange_Open(t *testing.T) {
	ok := OpenChange{ChangeCmd: NewChangeCmd("ch1", ""), ChangeKind: KindRealization, Mode: ModeSession, Scope: team, IntentRef: "i1", Impact: impact}
	cases := []struct {
		name  string
		state Change
		mod   func(*OpenChange)
		ctx   core.DecideContext
		want  string
		level core.Level
	}{
		{"ok", Change{}, func(*OpenChange) {}, dctx(human, core.L1), "", core.L1},
		{"no intent, explicit", Change{}, func(m *OpenChange) { m.IntentRef = ""; m.NoIntent = true }, dctx(human, core.L0), "", core.L0},
		{"no intent, implicit", Change{}, func(m *OpenChange) { m.IntentRef = "" }, dctx(human, core.L0), "no-intent", 0},
		{"harness forced to L0", Change{}, func(m *OpenChange) { m.ChangeKind = KindHarness }, dctx(human, core.L3), "", core.L0},
		{"exists", Change{State: StateProposed}, func(*OpenChange) {}, dctx(human, core.L0), "exists", 0},
		{"no id", Change{}, func(m *OpenChange) { m.ID = "" }, dctx(human, core.L0), "invalid", 0},
		{"bad kind", Change{}, func(m *OpenChange) { m.ChangeKind = "x" }, dctx(human, core.L0), "invalid", 0},
		{"bad mode", Change{}, func(m *OpenChange) { m.Mode = "x" }, dctx(human, core.L0), "invalid", 0},
		{"bad scope", Change{}, func(m *OpenChange) { m.Scope = core.ScopeKey{PathGlob: "a"} }, dctx(human, core.L0), "invalid", 0},
		{"bad anchor", Change{}, func(m *OpenChange) { m.Impact.Anchors = []string{"nope"} }, dctx(human, core.L0), "invalid", 0},
		{"autonomous below L2", Change{}, func(m *OpenChange) { m.Mode = ModeAutonomous }, dctx(agent, core.L1), "autonomy", 0},
		{"autonomous at L2", Change{}, func(m *OpenChange) { m.Mode = ModeAutonomous }, dctx(agent, core.L2), "", core.L2},
		{"autonomous harness never", Change{}, func(m *OpenChange) { m.Mode = ModeAutonomous; m.ChangeKind = KindHarness }, dctx(agent, core.L3), "autonomy", 0},
	}
	for _, c := range cases {
		cmd := ok
		c.mod(&cmd)
		evs, err := c.state.Decide(cmd, c.ctx)
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, got, c.want, err)
			continue
		}
		if c.want == "" {
			s := core.Replay(Change{}, evs)
			if s.State != StateProposed || s.AutonomyApplied != c.level || s.ID != "ch1" {
				t.Errorf("%s: after open %+v", c.name, s)
			}
		}
	}
}

func TestChange_Judge(t *testing.T) {
	j := Judgment{ID: "j1", Decision: DecisionAccept, Reason: "fine", By: human, At: t0, Source: SourceReview}
	cases := []struct {
		name  string
		state Change
		mod   func(*Judgment)
		want  string
	}{
		{"human ok", opened(t, KindRealization, core.L0, impact), func(*Judgment) {}, ""},
		{"agent at L0", opened(t, KindRealization, core.L0, impact), func(j *Judgment) { j.By = agent }, "autonomy"},
		{"agent at L1", opened(t, KindRealization, core.L1, impact), func(j *Judgment) { j.By = agent }, ""},
		{"agent on harness", opened(t, KindHarness, core.L3, impact), func(j *Judgment) { j.By = agent }, "human-only"},
		{"impact needs reason", opened(t, KindRealization, core.L0, impact), func(j *Judgment) { j.Reason = "" }, "no-reason"},
		{"no impact no reason ok", opened(t, KindRealization, core.L0, Impact{}), func(j *Judgment) { j.Reason = "" }, ""},
		{"bad decision", opened(t, KindRealization, core.L0, impact), func(j *Judgment) { j.Decision = "meh" }, "invalid"},
		{"no id", opened(t, KindRealization, core.L0, impact), func(j *Judgment) { j.ID = "" }, "invalid"},
		{"bad actor", opened(t, KindRealization, core.L0, impact), func(j *Judgment) { j.By = core.ActorRef{Kind: core.ActorAgent, ID: "x"} }, "invalid"},
		{"duplicate", judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionAccept, core.L0), func(j *Judgment) { j.ID = "j-alice-accept" }, "duplicate"},
		{"missing", Change{}, func(*Judgment) {}, "not-found"},
	}
	for _, c := range cases {
		jj := j
		c.mod(&jj)
		evs, err := c.state.Decide(Judge{NewChangeCmd("ch1", ""), jj}, dctx(jj.By, c.state.AutonomyApplied))
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, got, c.want, err)
			continue
		}
		if c.want == "" {
			s := core.Replay(c.state, evs)
			if s.State != StateJudged || len(s.Judgments) == 0 {
				t.Errorf("%s: after judge %+v", c.name, s)
			}
		}
	}
}

func TestChange_Deploy(t *testing.T) {
	withOwner := dctx(human, core.L0)
	withOwner.Judges = []core.ID{"bob"}
	cases := []struct {
		name  string
		state Change
		ctx   core.DecideContext
		want  string
	}{
		{"no impact from proposed", opened(t, KindRealization, core.L0, Impact{}), dctx(human, core.L0), ""},
		{"impact unjudged", opened(t, KindRealization, core.L0, impact), dctx(human, core.L0), "unjudged"},
		{"human accepted", judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionAccept, core.L0), dctx(human, core.L0), ""},
		{"human rejected", judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionReject, core.L0), dctx(human, core.L0), "rejected"},
		{"agent only at L1", judgedBy(t, opened(t, KindRealization, core.L1, impact), agent, DecisionAccept, core.L1), dctx(agent, core.L1), "human-required"},
		{"agent then human at L1", judgedBy(t, judgedBy(t, opened(t, KindRealization, core.L1, impact), agent, DecisionAccept, core.L1), human, DecisionAccept, core.L1), dctx(agent, core.L1), ""},
		{"agent only at L2", judgedBy(t, opened(t, KindRealization, core.L2, impact), agent, DecisionAccept, core.L2), dctx(agent, core.L2), ""},
		{"owner missing", judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionAccept, core.L0), withOwner, "owner-required"},
		{"owner present", judgedBy(t, judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionAccept, core.L0), bob, DecisionAccept, core.L0), withOwner, ""},
		{"no ref", opened(t, KindRealization, core.L0, Impact{}), dctx(human, core.L0), "invalid"},
	}
	for _, c := range cases {
		ref := "deploy-1"
		if c.name == "no ref" {
			ref = ""
		}
		evs, err := c.state.Decide(Deploy{NewChangeCmd("ch1", ""), ref}, c.ctx)
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, got, c.want, err)
			continue
		}
		if c.want == "" {
			s := core.Replay(c.state, evs)
			if s.State != StateDeployed || s.Deployment == nil || !s.Deployment.At.Equal(t0) {
				t.Errorf("%s: after deploy %+v", c.name, s)
			}
			if _, err := s.Decide(Deploy{NewChangeCmd("ch1", ""), ref}, c.ctx); code(t, err) != "bad-state" {
				t.Errorf("%s: redeploy: %v", c.name, err)
			}
		}
	}
}

func deployed(t *testing.T, level core.Level, by core.ActorRef) Change {
	t.Helper()
	c := judgedBy(t, opened(t, KindRealization, level, impact), by, DecisionAccept, level)
	if level <= core.L1 && by.IsAgent() {
		c = judgedBy(t, c, human, DecisionAccept, level)
	}
	evs, err := c.Decide(Deploy{NewChangeCmd("ch1", ""), "d1"}, dctx(human, level))
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(c, evs)
}

func TestChange_OutcomeAndSettle(t *testing.T) {
	d := deployed(t, core.L0, human)
	if _, err := opened(t, KindRealization, core.L0, impact).Decide(RecordOutcome{NewChangeCmd("ch1", ""), Outcome{Verdict: Confirmed}}, dctx(human, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("outcome before deploy: %v", err)
	}
	if _, err := d.Decide(RecordOutcome{NewChangeCmd("ch1", ""), Outcome{Verdict: "x"}}, dctx(human, core.L0)); code(t, err) != "invalid" {
		t.Errorf("bad verdict: %v", err)
	}
	// settle: window still open
	if _, err := d.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(human, core.L0)); code(t, err) != "observing" {
		t.Errorf("settle in window: %v", err)
	}
	// window expired
	late := dctx(human, core.L0)
	late.Now = t0.Add(core.DefaultPolicy().OutcomeWindow + time.Minute)
	evs, err := d.Decide(Settle{NewChangeCmd("ch1", ""), []core.ID{"c9"}}, late)
	if err != nil {
		t.Fatal(err)
	}
	if s := core.Replay(d, evs); s.State != StateSettled || len(s.Derived) != 1 {
		t.Errorf("settled after window %+v", s)
	}
	// with outcome
	evs, err = d.Decide(RecordOutcome{NewChangeCmd("ch1", ""), Outcome{Verdict: Regressed, Window: time.Hour}}, dctx(agent, core.L0))
	if err != nil {
		t.Fatal(err)
	}
	obs := core.Replay(d, evs)
	if obs.State != StateObserved || obs.Outcome == nil || !obs.Outcome.At.Equal(t0) {
		t.Fatalf("observed %+v", obs)
	}
	if _, err := obs.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(agent, core.L0)); code(t, err) != "autonomy" {
		t.Errorf("agent settle at L0: %v", err)
	}
	evs, err = obs.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(human, core.L0))
	if err != nil {
		t.Fatal(err)
	}
	settled := core.Replay(obs, evs)
	if settled.State != StateSettled || !settled.SettledAt.Equal(t0) {
		t.Errorf("settled %+v", settled)
	}
	if _, err := settled.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(human, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("settle twice: %v", err)
	}
	// agent settles at L2
	d2 := deployed(t, core.L2, agent)
	evs, _ = d2.Decide(RecordOutcome{NewChangeCmd("ch1", ""), Outcome{Verdict: Confirmed}}, dctx(agent, core.L2))
	obs2 := core.Replay(d2, evs)
	if _, err := obs2.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(agent, core.L2)); err != nil {
		t.Errorf("agent settle at L2: %v", err)
	}
	// judged-but-not-deployed cannot settle when it has impact
	if _, err := judgedBy(t, opened(t, KindRealization, core.L0, impact), human, DecisionAccept, core.L0).Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(human, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("settle judged: %v", err)
	}
	// outside the harness: settles straight away, derives nothing
	out := opened(t, KindRealization, core.L0, Impact{})
	if _, err := out.Decide(Settle{NewChangeCmd("ch1", ""), []core.ID{"x"}}, dctx(human, core.L0)); code(t, err) != "no-derivation" {
		t.Errorf("derive from no impact: %v", err)
	}
	evs, err = out.Decide(Settle{NewChangeCmd("ch1", ""), nil}, dctx(agent, core.L0))
	if err != nil || core.Replay(out, evs).State != StateSettled {
		t.Errorf("settle outside harness: %v", err)
	}
}

func TestChange_Proposal(t *testing.T) {
	c := opened(t, KindRealization, core.L0, impact)
	p := Proposal{Ref: "diff-1", Actor: agent, At: t0}
	evs, err := c.Decide(AddProposal{NewChangeCmd("ch1", ""), p}, dctx(agent, core.L0))
	if err != nil {
		t.Fatal(err)
	}
	c = core.Replay(c, evs)
	if len(c.Proposals) != 1 {
		t.Fatal("proposal not applied")
	}
	if _, err := c.Decide(AddProposal{NewChangeCmd("ch1", ""), Proposal{Actor: agent}}, dctx(agent, core.L0)); code(t, err) != "invalid" {
		t.Errorf("empty ref: %v", err)
	}
	d := deployed(t, core.L0, human)
	if _, err := d.Decide(AddProposal{NewChangeCmd("ch1", ""), p}, dctx(agent, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("proposal after deploy: %v", err)
	}
}

func TestChangeOpened_Upcast(t *testing.T) {
	codec := core.NewCodec()
	RegisterEvents(codec)
	e, err := codec.Decode("ChangeOpened", 1, []byte(`{"id":"old","kind":"realization","mode":"manual","scope":{"repo":"r"},"no_intent":true,"impact":{},"autonomy_applied":"L0","by":{"kind":"human","id":"a"},"at":"2026-09-10T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	v2, ok := e.(ChangeOpened)
	if !ok || v2.ID != "old" || v2.Sessions != nil || v2.Version() != 2 || v2.Mode != ModeManual {
		t.Fatalf("upcast: %#v", e)
	}
	cmd := OpenChange{ChangeCmd: NewChangeCmd("ch1", ""), ChangeKind: KindRealization, Mode: ModeSession, Scope: team, NoIntent: true, Sessions: []string{"session/claude-code/s1"}}
	evs, err := (Change{}).Decide(cmd, dctx(human, core.L0))
	if err != nil || len(core.Replay(Change{}, evs).Sessions) != 1 {
		t.Fatalf("sessions: %v", err)
	}
}
