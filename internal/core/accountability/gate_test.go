package accountability

import (
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

func req() RequestGate {
	return RequestGate{GateCmd: NewGateCmd("g1", ""), Actor: agent, Scope: team, Anchors: []string{"code://src/a.ts#f"}, ProposalRef: "p1", RequestedLevel: core.L1}
}

func TestGate_Policy(t *testing.T) {
	cases := []struct {
		name      string
		requested core.Level
		autonomy  core.Level
		cap       core.Level
		judges    []core.ID
		want      GateDecision
	}{
		{"at level allow", core.L1, core.L1, core.L3, nil, GateAllow},
		{"below level allow", core.L0, core.L2, core.L3, nil, GateAllow},
		{"above level ask", core.L2, core.L1, core.L3, nil, GateAsk},
		{"above cap deny", core.L2, core.L3, core.L1, nil, GateDeny},
		{"other owner ask even if allowed", core.L1, core.L2, core.L3, []core.ID{"bob"}, GateAsk},
		{"other owner over cap deny", core.L3, core.L2, core.L2, []core.ID{"bob"}, GateDeny},
	}
	for _, c := range cases {
		ctx := dctx(agent, c.autonomy)
		ctx.Cap = c.cap
		ctx.Judges = c.judges
		m := req()
		m.RequestedLevel = c.requested
		evs, err := (Gate{}).Decide(m, ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		g := core.Replay(Gate{}, evs)
		if g.Decision != c.want {
			t.Errorf("%s: got %s want %s (%s)", c.name, g.Decision, c.want, g.PolicyRef)
		}
		if c.want == GateAsk {
			if g.State != GateStateOpen || !g.ExpiresAt.Equal(t0.Add(core.DefaultPolicy().GateTTL)) {
				t.Errorf("%s: open gate %+v", c.name, g)
			}
		} else if g.State != GateStateDecided {
			t.Errorf("%s: state %s", c.name, g.State)
		}
		// idempotent re-request: no new events
		if evs, err := g.Decide(m, ctx); err != nil || len(evs) != 0 {
			t.Errorf("%s: re-request: %v %d", c.name, err, len(evs))
		}
	}
}

func TestGate_RequestValidation(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*RequestGate)
		want string
	}{
		{"human asks", func(m *RequestGate) { m.Actor = human }, "agent-only"},
		{"no owner", func(m *RequestGate) { m.Actor = core.ActorRef{Kind: core.ActorAgent, ID: "x"} }, "invalid"},
		{"no id", func(m *RequestGate) { m.ID = "" }, "invalid"},
		{"no proposal", func(m *RequestGate) { m.ProposalRef = "" }, "invalid"},
		{"bad level", func(m *RequestGate) { m.RequestedLevel = 9 }, "invalid"},
		{"bad anchor", func(m *RequestGate) { m.Anchors = []string{"x"} }, "invalid"},
		{"bad scope", func(m *RequestGate) { m.Scope = core.ScopeKey{PathGlob: "a"} }, "invalid"},
	}
	for _, c := range cases {
		m := req()
		c.mod(&m)
		_, err := (Gate{}).Decide(m, dctx(agent, core.L1))
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func openGate(t *testing.T, judges []core.ID) Gate {
	t.Helper()
	ctx := dctx(agent, core.L0)
	ctx.Judges = judges
	evs, err := (Gate{}).Decide(req(), ctx)
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(Gate{}, evs)
}

func TestGate_Resolve(t *testing.T) {
	res := Resolution{Decision: GateAllow, Reason: "reviewed the diff", By: human}
	late := dctx(human, core.L0)
	late.Now = t0.Add(2 * time.Hour)
	withBob := dctx(human, core.L0)
	withBob.Judges = []core.ID{"bob"}
	decided := core.Replay(Gate{}, func() []core.Event { e, _ := (Gate{}).Decide(req(), dctx(agent, core.L1)); return e }())

	cases := []struct {
		name  string
		state Gate
		mod   func(*Resolution)
		ctx   core.DecideContext
		want  string
	}{
		{"ok", openGate(t, nil), func(*Resolution) {}, dctx(human, core.L0), ""},
		{"deny ok", openGate(t, nil), func(r *Resolution) { r.Decision = GateDeny }, dctx(human, core.L0), ""},
		{"ask is not a resolution", openGate(t, nil), func(r *Resolution) { r.Decision = GateAsk }, dctx(human, core.L0), "invalid"},
		{"agent", openGate(t, nil), func(r *Resolution) { r.By = agent }, dctx(agent, core.L0), "human-only"},
		{"not a judge", openGate(t, []core.ID{"bob"}), func(*Resolution) {}, withBob, "not-a-judge"},
		{"the judge", openGate(t, []core.ID{"bob"}), func(r *Resolution) { r.By = bob }, withBob, ""},
		{"no reason", openGate(t, nil), func(r *Resolution) { r.Reason = "" }, dctx(human, core.L0), "no-reason"},
		{"expired", openGate(t, nil), func(*Resolution) {}, late, "expired"},
		{"already decided", decided, func(*Resolution) {}, dctx(human, core.L0), "bad-state"},
		{"missing", Gate{}, func(*Resolution) {}, dctx(human, core.L0), "not-found"},
	}
	for _, c := range cases {
		r := res
		c.mod(&r)
		evs, err := c.state.Decide(ResolveGate{NewGateCmd("g1", ""), r}, c.ctx)
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, got, c.want, err)
			continue
		}
		if c.want == "" {
			g := core.Replay(c.state, evs)
			if g.State != GateStateResolved || g.Resolution == nil || g.Decision != r.Decision || !g.Resolution.At.Equal(t0) {
				t.Errorf("%s: after resolve %+v", c.name, g)
			}
			if _, err := g.Decide(ResolveGate{NewGateCmd("g1", ""), r}, c.ctx); code(t, err) != "bad-state" {
				t.Errorf("%s: resolve twice: %v", c.name, err)
			}
		}
	}
}

func TestGate_Expire(t *testing.T) {
	g := openGate(t, nil)
	if _, err := g.Decide(ExpireGate{NewGateCmd("g1", "")}, dctx(agent, core.L0)); code(t, err) != "not-yet" {
		t.Errorf("early: %v", err)
	}
	late := dctx(agent, core.L0)
	late.Now = g.ExpiresAt
	evs, err := g.Decide(ExpireGate{NewGateCmd("g1", "")}, late)
	if err != nil {
		t.Fatal(err)
	}
	e := core.Replay(g, evs)
	if e.State != GateStateExpired {
		t.Errorf("state %s", e.State)
	}
	if evs, err := e.Decide(ExpireGate{NewGateCmd("g1", "")}, late); err != nil || len(evs) != 0 {
		t.Errorf("expire twice: %v %d", err, len(evs))
	}
	if _, err := (Gate{}).Decide(ExpireGate{NewGateCmd("g1", "")}, late); code(t, err) != "not-found" {
		t.Errorf("missing: %v", err)
	}
}

func TestGateIDFor(t *testing.T) {
	a, b := GateIDFor(agent, "p1"), GateIDFor(agent, "p1")
	if a != b || a == GateIDFor(agent, "p2") || a == GateIDFor(human, "p1") {
		t.Error("gate id must be a deterministic function of (actor, proposal_ref)")
	}
}
