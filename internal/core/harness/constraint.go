package harness

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// ConstraintKind is what a constraint is (spec §3.1).
type ConstraintKind string

const (
	KindContract  ConstraintKind = "contract"
	KindInvariant ConstraintKind = "invariant"
	KindRule      ConstraintKind = "rule"
	KindBudget    ConstraintKind = "budget"
	KindAutonomy  ConstraintKind = "autonomy"
)

func (k ConstraintKind) valid() bool {
	switch k {
	case KindContract, KindInvariant, KindRule, KindBudget, KindAutonomy:
		return true
	}
	return false
}

// ConstraintState is the lifecycle state (spec §3.1).
type ConstraintState string

const (
	StateNone      ConstraintState = ""
	StateGenerated ConstraintState = "generated"
	StateVerified  ConstraintState = "verified"
	StateReview    ConstraintState = "review"
	StateStale     ConstraintState = "stale"
	StateRetired   ConstraintState = "retired"
)

// Constraint is the harness unit: contract, invariant, rule, budget or an
// autonomy declaration for a scope. It is compiled from judgments
// (promotion) or authored; promotion always carries its source judgments and
// a human-readable explanation (ADR 0003).
type Constraint struct {
	ID        core.ID        `json:"id"`
	Kind      ConstraintKind `json:"kind"`
	Scope     core.ScopeKey  `json:"scope"`
	Body      string         `json:"body"`
	Checkable string         `json:"checkable,omitempty"` // machine-checkable spec, if any
	// Origin lists the judgments this constraint was promoted from; Authored
	// marks a hand-written one. At least one must hold.
	Origin      []core.ID `json:"origin,omitempty"`
	Authored    bool      `json:"authored,omitempty"`
	Explanation string    `json:"explanation,omitempty"`
	// Level applies to KindAutonomy only.
	Level        core.Level      `json:"level,omitempty"`
	State        ConstraintState `json:"state"`
	Supersedes   core.ID         `json:"supersedes,omitempty"`
	SupersededBy core.ID         `json:"superseded_by,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// ConstraintStream is the ledger stream for a constraint.
func ConstraintStream(id core.ID) string { return "constraint/" + string(id) }

// ---- commands ----

// ConstraintCmd is the identity part of every constraint command (embed it).
type ConstraintCmd struct {
	ID   core.ID
	Idem string
}

func (c ConstraintCmd) Stream() string         { return ConstraintStream(c.ID) }
func (c ConstraintCmd) IdempotencyKey() string { return c.Idem }

// DraftConstraint creates a constraint in state generated.
type DraftConstraint struct {
	ID             core.ID
	Idem           string
	ConstraintKind ConstraintKind
	Scope          core.ScopeKey
	Body           string
	Checkable      string
	Origin         []core.ID
	Authored       bool
	Explanation    string
	Level          core.Level
	Supersedes     core.ID
}

func (DraftConstraint) Kind() string                 { return "DraftConstraint" }
func (c DraftConstraint) Stream() string             { return ConstraintStream(c.ID) }
func (c DraftConstraint) IdempotencyKey() string     { return c.Idem }
func (c DraftConstraint) TargetScope() core.ScopeKey { return c.Scope }

// VerifyConstraint marks a generated/review constraint verified (human act).
type VerifyConstraint struct{ ConstraintCmd }

func (VerifyConstraint) Kind() string { return "VerifyConstraint" }

// PromoteConstraint widens a verified constraint to a wider scope. Requires
// source judgments and an explanation; a human decides (harness change = L0).
type PromoteConstraint struct {
	ConstraintCmd
	ToScope     core.ScopeKey
	Origin      []core.ID
	Explanation string
}

func (PromoteConstraint) Kind() string { return "PromoteConstraint" }

// DemoteConstraint narrows a constraint's scope with a reason.
type DemoteConstraint struct {
	ConstraintCmd
	ToScope core.ScopeKey
	Reason  string
}

func (DemoteConstraint) Kind() string { return "DemoteConstraint" }

// SupersedeConstraint retires this constraint in favour of By.
type SupersedeConstraint struct {
	ConstraintCmd
	By core.ID
}

func (SupersedeConstraint) Kind() string { return "SupersedeConstraint" }

// RetireConstraint retires a constraint.
type RetireConstraint struct {
	ConstraintCmd
	Reason string
}

func (RetireConstraint) Kind() string { return "RetireConstraint" }

// NewConstraintCmd builds the shared identity part of a constraint command.
func NewConstraintCmd(id core.ID, idem string) ConstraintCmd {
	return ConstraintCmd{ID: id, Idem: idem}
}

// ---- events ----

// ConstraintDrafted is the creation event.
type ConstraintDrafted struct {
	ID             core.ID        `json:"id"`
	ConstraintKind ConstraintKind `json:"kind"`
	Scope          core.ScopeKey  `json:"scope"`
	Body           string         `json:"body"`
	Checkable      string         `json:"checkable,omitempty"`
	Origin         []core.ID      `json:"origin,omitempty"`
	Authored       bool           `json:"authored,omitempty"`
	Explanation    string         `json:"explanation,omitempty"`
	Level          core.Level     `json:"level,omitempty"`
	Supersedes     core.ID        `json:"supersedes,omitempty"`
	At             time.Time      `json:"at"`
}

func (ConstraintDrafted) Kind() string { return "ConstraintDrafted" }
func (ConstraintDrafted) Version() int { return 1 }

// ConstraintVerified marks verification.
type ConstraintVerified struct {
	ID core.ID       `json:"id"`
	By core.ActorRef `json:"by"`
	At time.Time     `json:"at"`
}

func (ConstraintVerified) Kind() string { return "ConstraintVerified" }
func (ConstraintVerified) Version() int { return 1 }

// ConstraintPromoted widens scope; carries the source judgments and explanation.
type ConstraintPromoted struct {
	ID          core.ID       `json:"id"`
	From        core.ScopeKey `json:"from"`
	To          core.ScopeKey `json:"to"`
	Origin      []core.ID     `json:"origin"`
	Explanation string        `json:"explanation"`
	By          core.ActorRef `json:"by"`
	At          time.Time     `json:"at"`
}

func (ConstraintPromoted) Kind() string { return "ConstraintPromoted" }
func (ConstraintPromoted) Version() int { return 1 }

// ConstraintDemoted narrows scope.
type ConstraintDemoted struct {
	ID     core.ID       `json:"id"`
	From   core.ScopeKey `json:"from"`
	To     core.ScopeKey `json:"to"`
	Reason string        `json:"reason"`
	By     core.ActorRef `json:"by"`
	At     time.Time     `json:"at"`
}

func (ConstraintDemoted) Kind() string { return "ConstraintDemoted" }
func (ConstraintDemoted) Version() int { return 1 }

// ConstraintSuperseded retires in favour of another.
type ConstraintSuperseded struct {
	ID    core.ID       `json:"id"`
	By    core.ID       `json:"by"`
	At    time.Time     `json:"at"`
	Actor core.ActorRef `json:"actor"`
}

func (ConstraintSuperseded) Kind() string { return "ConstraintSuperseded" }
func (ConstraintSuperseded) Version() int { return 1 }

// ConstraintRetired retires.
type ConstraintRetired struct {
	ID     core.ID       `json:"id"`
	Reason string        `json:"reason,omitempty"`
	By     core.ActorRef `json:"by"`
	At     time.Time     `json:"at"`
}

func (ConstraintRetired) Kind() string { return "ConstraintRetired" }
func (ConstraintRetired) Version() int { return 1 }

// ---- aggregate ----

// Decide applies the constraint rules (spec §3.6b).
func (c Constraint) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case DraftConstraint:
		return c.draft(m, ctx)
	case VerifyConstraint:
		return c.verify(ctx)
	case PromoteConstraint:
		return c.promote(m, ctx)
	case DemoteConstraint:
		return c.demote(m, ctx)
	case SupersedeConstraint:
		return c.supersede(m, ctx)
	case RetireConstraint:
		return c.retire(m, ctx)
	}
	return nil, core.Reject("unknown-command", "constraint: %s", cmd.Kind())
}

func (c Constraint) exists() bool { return c.State != StateNone }

func (c Constraint) draft(m DraftConstraint, ctx core.DecideContext) ([]core.Event, error) {
	if c.exists() {
		return nil, core.Reject("exists", "constraint %s already exists", m.ID)
	}
	if m.ID.IsZero() {
		return nil, core.Reject("invalid", "constraint id required")
	}
	if !m.ConstraintKind.valid() {
		return nil, core.Reject("invalid", "unknown constraint kind %q", m.ConstraintKind)
	}
	if err := m.Scope.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if m.Body == "" {
		return nil, core.Reject("invalid", "constraint body required")
	}
	if !m.Authored && len(m.Origin) == 0 {
		return nil, core.Reject("no-origin", "a constraint is either authored or promoted from judgments")
	}
	if m.ConstraintKind == KindAutonomy {
		if !m.Level.Valid() {
			return nil, core.Reject("invalid", "autonomy constraint needs a valid level")
		}
		if m.Level > ctx.Cap {
			return nil, core.Reject("over-cap", "level %s exceeds cap %s", m.Level, ctx.Cap)
		}
	}
	return []core.Event{ConstraintDrafted{
		ID: m.ID, ConstraintKind: m.ConstraintKind, Scope: m.Scope, Body: m.Body, Checkable: m.Checkable,
		Origin: m.Origin, Authored: m.Authored, Explanation: m.Explanation, Level: m.Level,
		Supersedes: m.Supersedes, At: ctx.Now,
	}}, nil
}

func (c Constraint) verify(ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "constraint does not exist")
	}
	if !ctx.Actor.IsHuman() {
		return nil, core.Reject("human-only", "verification is a human act")
	}
	switch c.State {
	case StateGenerated, StateReview, StateStale:
	default:
		return nil, core.Reject("bad-state", "cannot verify a %s constraint", c.State)
	}
	return []core.Event{ConstraintVerified{ID: c.ID, By: ctx.Actor, At: ctx.Now}}, nil
}

func (c Constraint) promote(m PromoteConstraint, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "constraint does not exist")
	}
	if !ctx.Actor.IsHuman() {
		return nil, core.Reject("human-only", "promotion is a harness change: a human approves it")
	}
	if c.State != StateVerified {
		return nil, core.Reject("bad-state", "only a verified constraint can be promoted (is %s)", c.State)
	}
	if err := m.ToScope.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !m.ToScope.Contains(c.Scope) || m.ToScope == c.Scope {
		return nil, core.Reject("not-wider", "promotion target %s must be wider than %s", m.ToScope, c.Scope)
	}
	origin := append(append([]core.ID(nil), c.Origin...), m.Origin...)
	if len(origin) == 0 {
		return nil, core.Reject("no-origin", "promotion requires source judgments")
	}
	if m.Explanation == "" {
		return nil, core.Reject("no-explanation", "promotion requires a human-readable explanation")
	}
	if c.Kind == KindAutonomy && c.Level > ctx.Cap {
		return nil, core.Reject("over-cap", "level %s exceeds cap %s at the target scope", c.Level, ctx.Cap)
	}
	return []core.Event{ConstraintPromoted{
		ID: c.ID, From: c.Scope, To: m.ToScope, Origin: origin, Explanation: m.Explanation, By: ctx.Actor, At: ctx.Now,
	}}, nil
}

func (c Constraint) demote(m DemoteConstraint, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "constraint does not exist")
	}
	if c.State == StateRetired {
		return nil, core.Reject("bad-state", "constraint is retired")
	}
	if err := m.ToScope.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !c.Scope.Contains(m.ToScope) || m.ToScope == c.Scope {
		return nil, core.Reject("not-narrower", "demotion target %s must be narrower than %s", m.ToScope, c.Scope)
	}
	if m.Reason == "" {
		return nil, core.Reject("no-reason", "demotion requires a reason")
	}
	return []core.Event{ConstraintDemoted{ID: c.ID, From: c.Scope, To: m.ToScope, Reason: m.Reason, By: ctx.Actor, At: ctx.Now}}, nil
}

func (c Constraint) supersede(m SupersedeConstraint, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "constraint does not exist")
	}
	if c.State == StateRetired {
		return nil, core.Reject("bad-state", "constraint is retired")
	}
	if m.By.IsZero() || m.By == c.ID {
		return nil, core.Reject("invalid", "supersede needs the successor id")
	}
	return []core.Event{ConstraintSuperseded{ID: c.ID, By: m.By, At: ctx.Now, Actor: ctx.Actor}}, nil
}

func (c Constraint) retire(m RetireConstraint, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "constraint does not exist")
	}
	if c.State == StateRetired {
		return nil, core.Reject("bad-state", "constraint is already retired")
	}
	return []core.Event{ConstraintRetired{ID: c.ID, Reason: m.Reason, By: ctx.Actor, At: ctx.Now}}, nil
}

// Apply folds an event. Unknown events leave the state unchanged.
func (c Constraint) Apply(e core.Event) Constraint {
	switch v := e.(type) {
	case ConstraintDrafted:
		c = Constraint{
			ID: v.ID, Kind: v.ConstraintKind, Scope: v.Scope, Body: v.Body, Checkable: v.Checkable,
			Origin: v.Origin, Authored: v.Authored, Explanation: v.Explanation, Level: v.Level,
			Supersedes: v.Supersedes, State: StateGenerated, UpdatedAt: v.At,
		}
	case ConstraintVerified:
		c.State = StateVerified
		c.UpdatedAt = v.At
	case ConstraintPromoted:
		c.Scope = v.To
		c.Origin = v.Origin
		c.Explanation = v.Explanation
		c.UpdatedAt = v.At
	case ConstraintDemoted:
		c.Scope = v.To
		c.UpdatedAt = v.At
	case ConstraintSuperseded:
		c.State = StateRetired
		c.SupersededBy = v.By
		c.UpdatedAt = v.At
	case ConstraintRetired:
		c.State = StateRetired
		c.UpdatedAt = v.At
	}
	return c
}

// RegisterEvents registers this context's events with the codec.
func RegisterEvents(c *core.Codec) {
	c.Register(ConstraintDrafted{})
	c.Register(ConstraintVerified{})
	c.Register(ConstraintPromoted{})
	c.Register(ConstraintDemoted{})
	c.Register(ConstraintSuperseded{})
	c.Register(ConstraintRetired{})
	c.Register(AutonomyChanged{})
	c.Register(CapChanged{})
	c.Register(ExceptionRaised{})
}
