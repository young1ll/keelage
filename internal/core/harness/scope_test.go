package harness

import (
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

func goodEvidence() Evidence { return Evidence{Since: t0.Add(-30 * 24 * time.Hour), Outcomes: 12} }

func scopeAt(level core.Level) Scope {
	s := NewScope(team)
	if level > core.L0 {
		s = s.Apply(AutonomyChanged{Key: team, From: core.L0, To: level, Trigger: core.TriggerPromote, At: t0.Add(-time.Hour)})
	}
	return s
}

func TestScope_SetAutonomy(t *testing.T) {
	ok := SetAutonomy{ScopeCmd: NewScopeCmd(team, ""), Level: core.L1, ChangeRef: "ch1", Evidence: goodEvidence()}
	demoted := scopeAt(core.L2).Apply(AutonomyChanged{Key: team, From: core.L2, To: core.L1, Trigger: core.TriggerRegress, At: t0.Add(-time.Minute)})
	capped := NewScope(team).Apply(CapChanged{Key: team, From: core.L3, To: core.L0, ChangeRef: "ch0", At: t0})

	cases := []struct {
		name  string
		state Scope
		mod   func(*SetAutonomy)
		actor core.ActorRef
		want  string
		level core.Level
		nEv   int
	}{
		{"raise one step", scopeAt(core.L0), func(*SetAutonomy) {}, human, "", core.L1, 1},
		{"same level no-op", scopeAt(core.L1), func(*SetAutonomy) {}, human, "", core.L1, 0},
		{"lower freely", scopeAt(core.L2), func(m *SetAutonomy) { m.Level = core.L0; m.ChangeRef = "" }, human, "", core.L0, 1},
		{"agent", scopeAt(core.L0), func(*SetAutonomy) {}, agent, "human-only", core.L0, 0},
		{"no change ref", scopeAt(core.L0), func(m *SetAutonomy) { m.ChangeRef = "" }, human, "no-change", core.L0, 0},
		{"two steps", scopeAt(core.L0), func(m *SetAutonomy) { m.Level = core.L2 }, human, "one-step", core.L0, 0},
		{"over cap", capped, func(*SetAutonomy) {}, human, "over-cap", core.L0, 0},
		{"evidence predates demotion", demoted, func(m *SetAutonomy) { m.Level = core.L2 }, human, "stale-evidence", core.L1, 0},
		{"evidence after demotion", demoted, func(m *SetAutonomy) { m.Level = core.L2; m.Evidence.Since = t0 }, human, "", core.L2, 1},
		{"regressions in evidence", scopeAt(core.L0), func(m *SetAutonomy) { m.Evidence.Regressions = 1 }, human, "regressed", core.L0, 0},
		{"too few outcomes", scopeAt(core.L0), func(m *SetAutonomy) { m.Evidence.Outcomes = 3 }, human, "insufficient-evidence", core.L0, 0},
		{"invalid level", scopeAt(core.L0), func(m *SetAutonomy) { m.Level = 7 }, human, "invalid", core.L0, 0},
		{"invalid key", scopeAt(core.L0), func(m *SetAutonomy) { m.Key = core.ScopeKey{PathGlob: "x"} }, human, "invalid", core.L0, 0},
	}
	for _, c := range cases {
		cmd := ok
		c.mod(&cmd)
		evs, err := c.state.Decide(cmd, dctx(c.actor))
		if got := code(t, err); got != c.want {
			t.Errorf("%s: got %q want %q (%v)", c.name, got, c.want, err)
			continue
		}
		if len(evs) != c.nEv {
			t.Errorf("%s: %d events", c.name, len(evs))
		}
		if s := core.Replay(c.state, evs); s.Level != c.level {
			t.Errorf("%s: level %s want %s", c.name, s.Level, c.level)
		}
	}
}

func TestScope_Regression(t *testing.T) {
	cases := []struct {
		from, to core.Level
		nEv      int
	}{
		{core.L3, core.L2, 2}, {core.L2, core.L1, 2}, {core.L1, core.L0, 2}, {core.L0, core.L0, 1},
	}
	for _, c := range cases {
		s := scopeAt(c.from)
		evs, err := s.Decide(RecordRegression{NewScopeCmd(team, ""), "ch9"}, dctx(agent))
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != c.nEv {
			t.Errorf("%s: %d events", c.from, len(evs))
		}
		if _, ok := evs[len(evs)-1].(ExceptionRaised); !ok {
			t.Errorf("%s: last event must be ExceptionRaised", c.from)
		}
		after := core.Replay(s, evs)
		if after.Level != c.to || !after.LastDemotedAt.Equal(t0) || after.Regressions != 1 {
			t.Errorf("%s: after %+v", c.from, after)
		}
		// immediate re-promotion with old evidence is refused
		_, err = after.Decide(SetAutonomy{ScopeCmd: NewScopeCmd(team, ""), Level: c.to + 1, ChangeRef: "ch", Evidence: goodEvidence()}, dctx(human))
		if c.to < core.MaxLevel && code(t, err) != "stale-evidence" {
			t.Errorf("%s: re-promotion: %v", c.from, err)
		}
	}
}

func TestScope_SetCap(t *testing.T) {
	s := scopeAt(core.L2)
	if _, err := s.Decide(SetCap{NewScopeCmd(team, ""), core.L1, ""}, dctx(human)); code(t, err) != "no-change" {
		t.Errorf("no change ref: %v", err)
	}
	if _, err := s.Decide(SetCap{NewScopeCmd(team, ""), core.L1, "ch"}, dctx(agent)); code(t, err) != "human-only" {
		t.Errorf("agent: %v", err)
	}
	evs, err := s.Decide(SetCap{NewScopeCmd(team, ""), core.L1, "ch"}, dctx(human))
	if err != nil || len(evs) != 2 {
		t.Fatalf("cap below level: %v %d", err, len(evs))
	}
	after := core.Replay(s, evs)
	if after.Cap != core.L1 || after.Level != core.L1 {
		t.Errorf("after cap %+v", after)
	}
	if evs, err := after.Decide(SetCap{NewScopeCmd(team, ""), core.L1, "ch"}, dctx(human)); err != nil || len(evs) != 0 {
		t.Errorf("same cap must be a no-op: %v %d", err, len(evs))
	}
	if _, err := after.Decide(SetAutonomy{ScopeCmd: NewScopeCmd(team, ""), Level: core.L2, ChangeRef: "ch", Evidence: goodEvidence()}, dctx(human)); code(t, err) != "over-cap" {
		t.Errorf("raise over cap: %v", err)
	}
}
