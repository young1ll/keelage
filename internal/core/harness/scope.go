package harness

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// Scope holds a scope's autonomy level and cap (spec §4.2). Default is L0
// with no cap (L3). Raising the level is a harness change: it needs a
// human, a Change reference and re-accumulated outcome evidence since the
// last demotion. One regression demotes one step automatically and raises an
// exception.
type Scope struct {
	Key           core.ScopeKey `json:"key"`
	Level         core.Level    `json:"level"`
	Cap           core.Level    `json:"cap"`
	Initialized   bool          `json:"initialized"`
	LastDemotedAt time.Time     `json:"last_demoted_at,omitempty"`
	Regressions   int           `json:"regressions"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// ScopeStream is the ledger stream for a scope key.
func ScopeStream(k core.ScopeKey) string { return "scope/" + k.String() }

// NewScope is the zero state for key.
func NewScope(k core.ScopeKey) Scope { return Scope{Key: k, Level: core.L0, Cap: core.MaxLevel} }

// Evidence is the outcome history offered for a promotion.
type Evidence struct {
	// Since is the start of the window the evidence covers; it must not
	// precede the last demotion (re-promotion re-accumulates history).
	Since       time.Time
	Outcomes    int
	Regressions int
}

// ScopeCmd is the identity part of every scope command (embed it).
type ScopeCmd struct {
	Key  core.ScopeKey
	Idem string
}

func (c ScopeCmd) Stream() string             { return ScopeStream(c.Key) }
func (c ScopeCmd) IdempotencyKey() string     { return c.Idem }
func (c ScopeCmd) TargetScope() core.ScopeKey { return c.Key }

// NewScopeCmd builds the identity part of a scope command.
func NewScopeCmd(k core.ScopeKey, idem string) ScopeCmd {
	return ScopeCmd{Key: k, Idem: idem}
}

// SetAutonomy changes the level. Raising needs ChangeRef and Evidence.
type SetAutonomy struct {
	ScopeCmd
	Level     core.Level
	ChangeRef core.ID
	Evidence  Evidence
}

func (SetAutonomy) Kind() string { return "SetAutonomy" }

// SetCap changes the upper bound; the level is lowered to fit.
type SetCap struct {
	ScopeCmd
	Cap       core.Level
	ChangeRef core.ID
}

func (SetCap) Kind() string { return "SetCap" }

// RecordRegression is issued when an outcome regressed in this scope.
type RecordRegression struct {
	ScopeCmd
	ChangeRef core.ID
}

func (RecordRegression) Kind() string { return "RecordRegression" }

// AutonomyChanged records a level transition.
type AutonomyChanged struct {
	Key       core.ScopeKey `json:"key"`
	From      core.Level    `json:"from"`
	To        core.Level    `json:"to"`
	Trigger   core.Trigger  `json:"trigger"`
	ChangeRef core.ID       `json:"change_ref,omitempty"`
	By        core.ActorRef `json:"by"`
	At        time.Time     `json:"at"`
}

func (AutonomyChanged) Kind() string { return "AutonomyChanged" }
func (AutonomyChanged) Version() int { return 1 }

// CapChanged records a cap change.
type CapChanged struct {
	Key       core.ScopeKey `json:"key"`
	From      core.Level    `json:"from"`
	To        core.Level    `json:"to"`
	ChangeRef core.ID       `json:"change_ref"`
	By        core.ActorRef `json:"by"`
	At        time.Time     `json:"at"`
}

func (CapChanged) Kind() string { return "CapChanged" }
func (CapChanged) Version() int { return 1 }

// ExceptionRaised records a regression that needs a human.
type ExceptionRaised struct {
	Key       core.ScopeKey `json:"key"`
	Category  string        `json:"category"`
	ChangeRef core.ID       `json:"change_ref,omitempty"`
	Level     core.Level    `json:"level"`
	At        time.Time     `json:"at"`
}

func (ExceptionRaised) Kind() string { return "ExceptionRaised" }
func (ExceptionRaised) Version() int { return 1 }

// Decide applies the scope rules.
func (s Scope) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case SetAutonomy:
		return s.setAutonomy(m, ctx)
	case SetCap:
		return s.setCap(m, ctx)
	case RecordRegression:
		return s.regress(m, ctx)
	}
	return nil, core.Reject("unknown-command", "scope: %s", cmd.Kind())
}

func (s Scope) setAutonomy(m SetAutonomy, ctx core.DecideContext) ([]core.Event, error) {
	if err := m.Key.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !m.Level.Valid() {
		return nil, core.Reject("invalid", "invalid level")
	}
	if !ctx.Actor.IsHuman() {
		return nil, core.Reject("human-only", "autonomy is set by a human")
	}
	if m.Level > s.Cap {
		return nil, core.Reject("over-cap", "level %s exceeds cap %s", m.Level, s.Cap)
	}
	if m.Level == s.Level {
		return nil, nil
	}
	if m.Level < s.Level {
		return []core.Event{AutonomyChanged{Key: m.Key, From: s.Level, To: m.Level, Trigger: core.TriggerPromote, ChangeRef: m.ChangeRef, By: ctx.Actor, At: ctx.Now}}, nil
	}
	// raising
	if m.ChangeRef.IsZero() {
		return nil, core.Reject("no-change", "raising autonomy is a harness Change; ChangeRef required")
	}
	next, _ := core.Transition(s.Level, core.TriggerPromote)
	if m.Level != next {
		return nil, core.Reject("one-step", "autonomy rises one step at a time (%s → %s)", s.Level, next)
	}
	ev := m.Evidence
	if ev.Since.Before(s.LastDemotedAt) {
		return nil, core.Reject("stale-evidence", "evidence must be re-accumulated after the last demotion at %s", s.LastDemotedAt.Format(time.RFC3339))
	}
	if ev.Regressions > 0 {
		return nil, core.Reject("regressed", "evidence contains %d regression(s)", ev.Regressions)
	}
	if ev.Outcomes < ctx.Policy.PromotionMinOutcomes {
		return nil, core.Reject("insufficient-evidence", "need %d scored outcomes, have %d", ctx.Policy.PromotionMinOutcomes, ev.Outcomes)
	}
	return []core.Event{AutonomyChanged{Key: m.Key, From: s.Level, To: m.Level, Trigger: core.TriggerPromote, ChangeRef: m.ChangeRef, By: ctx.Actor, At: ctx.Now}}, nil
}

func (s Scope) setCap(m SetCap, ctx core.DecideContext) ([]core.Event, error) {
	if err := m.Key.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !m.Cap.Valid() {
		return nil, core.Reject("invalid", "invalid cap")
	}
	if !ctx.Actor.IsHuman() {
		return nil, core.Reject("human-only", "the cap is set by a human")
	}
	if m.ChangeRef.IsZero() {
		return nil, core.Reject("no-change", "a cap change is a harness Change; ChangeRef required")
	}
	if m.Cap == s.Cap {
		return nil, nil
	}
	evs := []core.Event{CapChanged{Key: m.Key, From: s.Cap, To: m.Cap, ChangeRef: m.ChangeRef, By: ctx.Actor, At: ctx.Now}}
	if s.Level > m.Cap {
		evs = append(evs, AutonomyChanged{Key: m.Key, From: s.Level, To: m.Cap, Trigger: core.TriggerPromote, ChangeRef: m.ChangeRef, By: ctx.Actor, At: ctx.Now})
	}
	return evs, nil
}

func (s Scope) regress(m RecordRegression, ctx core.DecideContext) ([]core.Event, error) {
	if err := m.Key.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	to, _ := core.Transition(s.Level, core.TriggerRegress)
	evs := []core.Event{}
	if to != s.Level {
		evs = append(evs, AutonomyChanged{Key: m.Key, From: s.Level, To: to, Trigger: core.TriggerRegress, ChangeRef: m.ChangeRef, By: ctx.Actor, At: ctx.Now})
	}
	evs = append(evs, ExceptionRaised{Key: m.Key, Category: "regression", ChangeRef: m.ChangeRef, Level: to, At: ctx.Now})
	return evs, nil
}

// Apply folds an event.
func (s Scope) Apply(e core.Event) Scope {
	switch v := e.(type) {
	case AutonomyChanged:
		s.Key = v.Key
		s.Level = v.To
		s.Initialized = true
		s.UpdatedAt = v.At
		if v.Trigger == core.TriggerRegress {
			s.LastDemotedAt = v.At
		}
	case CapChanged:
		s.Key = v.Key
		s.Cap = v.To
		s.Initialized = true
		s.UpdatedAt = v.At
	case ExceptionRaised:
		s.Regressions++
		if v.Category == "regression" && s.LastDemotedAt.Before(v.At) {
			s.LastDemotedAt = v.At
		}
		s.UpdatedAt = v.At
	}
	return s
}
