package accountability

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// ChangeKind: a realization change (code, infra, docs) or a harness change
// (constraints, autonomy, decisions). Harness changes are always L0.
type ChangeKind string

const (
	KindRealization ChangeKind = "realization"
	KindHarness     ChangeKind = "harness"
)

// ChangeMode is how the change was produced.
type ChangeMode string

const (
	ModeManual     ChangeMode = "manual"
	ModeSession    ChangeMode = "session"
	ModeDelegated  ChangeMode = "delegated"
	ModeAutonomous ChangeMode = "autonomous"
)

// ChangeState is the lifecycle: proposed → judged → deployed → observed → settled.
type ChangeState string

const (
	StateNone     ChangeState = ""
	StateProposed ChangeState = "proposed"
	StateJudged   ChangeState = "judged"
	StateDeployed ChangeState = "deployed"
	StateObserved ChangeState = "observed"
	StateSettled  ChangeState = "settled"
)

// Proposal is one proposed diff (or constraint diff) by an actor.
type Proposal struct {
	Ref   string        `json:"ref"`
	Actor core.ActorRef `json:"actor"`
	At    time.Time     `json:"at"`
	Sig   string        `json:"sig,omitempty"`
}

// ImpactRelation is how a change relates to a constraint.
type ImpactRelation string

const (
	RelReferences ImpactRelation = "references"
	RelViolates   ImpactRelation = "violates"
	RelConflicts  ImpactRelation = "conflicts"
)

// ImpactRef links a change to one constraint.
type ImpactRef struct {
	Constraint core.ID        `json:"constraint"`
	Relation   ImpactRelation `json:"relation"`
}

// Impact is what the change touches. Empty constraints and decisions mean
// "outside the harness": no judgment is required.
type Impact struct {
	Constraints []ImpactRef `json:"constraints,omitempty"`
	Decisions   []core.ID   `json:"decisions,omitempty"`
	Anchors     []string    `json:"anchors,omitempty"`
}

// Empty reports whether the change touches no constraint or decision.
func (i Impact) Empty() bool { return len(i.Constraints) == 0 && len(i.Decisions) == 0 }

// JudgmentDecision is accept | modify | reject.
type JudgmentDecision string

const (
	DecisionAccept JudgmentDecision = "accept"
	DecisionModify JudgmentDecision = "modify"
	DecisionReject JudgmentDecision = "reject"
)

// JudgmentSource is where a judgment came from.
type JudgmentSource string

const (
	SourceSession JudgmentSource = "session"
	SourceReview  JudgmentSource = "review"
	SourceHarness JudgmentSource = "harness"
)

// Judgment is a signed decision about a change (spec §3.1).
type Judgment struct {
	ID       core.ID          `json:"id"`
	Decision JudgmentDecision `json:"decision"`
	Reason   string           `json:"reason,omitempty"`
	By       core.ActorRef    `json:"by"`
	At       time.Time        `json:"at"`
	Source   JudgmentSource   `json:"source"`
	Sig      string           `json:"sig,omitempty"`
}

// OutcomeVerdict is confirmed | regressed | unknown.
type OutcomeVerdict string

const (
	Confirmed OutcomeVerdict = "confirmed"
	Regressed OutcomeVerdict = "regressed"
	Unknown   OutcomeVerdict = "unknown"
)

// Outcome is what happened after deployment.
type Outcome struct {
	Window  time.Duration  `json:"window"`
	Signals []string       `json:"signals,omitempty"`
	Verdict OutcomeVerdict `json:"verdict"`
	At      time.Time      `json:"at"`
}

// Deployment records when and where the change went live.
type Deployment struct {
	Ref string    `json:"ref"`
	At  time.Time `json:"at"`
}

// Change is the unit of the product (ADR 0001): a change to the harness or
// to a realization, with its proposals, impact, judgments, deployment and
// outcome.
type Change struct {
	ID              core.ID     `json:"id"`
	Kind            ChangeKind  `json:"kind"`
	Mode            ChangeMode  `json:"mode"`
	IntentRef       core.ID     `json:"intent_ref,omitempty"`
	NoIntent        bool        `json:"no_intent,omitempty"`
	Proposals       []Proposal  `json:"proposals,omitempty"`
	Impact          Impact      `json:"impact"`
	Judgments       []Judgment  `json:"judgments,omitempty"`
	Deployment      *Deployment `json:"deployment,omitempty"`
	Outcome         *Outcome    `json:"outcome,omitempty"`
	Derived         []core.ID   `json:"derived,omitempty"`
	AutonomyApplied core.Level  `json:"autonomy_applied"`
	State           ChangeState `json:"state"`
	OpenedAt        time.Time   `json:"opened_at"`
	SettledAt       time.Time   `json:"settled_at,omitempty"`
}

// ChangeStream is the ledger stream for a change.
func ChangeStream(id core.ID) string { return "change/" + string(id) }

// ---- commands ----

// ChangeCmd is the identity part of every change command (embed it).
type ChangeCmd struct {
	ID   core.ID
	Idem string
}

func (c ChangeCmd) Stream() string         { return ChangeStream(c.ID) }
func (c ChangeCmd) IdempotencyKey() string { return c.Idem }

// NewChangeCmd builds the identity part of a change command.
func NewChangeCmd(id core.ID, idem string) ChangeCmd {
	return ChangeCmd{ID: id, Idem: idem}
}

// OpenChange creates a change. Scope is where autonomy is resolved.
type OpenChange struct {
	ChangeCmd
	ChangeKind ChangeKind
	Mode       ChangeMode
	Scope      core.ScopeKey
	IntentRef  core.ID
	NoIntent   bool
	Impact     Impact
	Proposal   *Proposal
}

func (OpenChange) Kind() string                 { return "OpenChange" }
func (c OpenChange) TargetScope() core.ScopeKey { return c.Scope }
func (c OpenChange) TouchedAnchors() []string   { return c.Impact.Anchors }

// AddProposal appends a proposal before deployment.
type AddProposal struct {
	ChangeCmd
	Proposal Proposal
}

func (AddProposal) Kind() string { return "AddProposal" }

// Judge records a judgment.
type Judge struct {
	ChangeCmd
	Judgment Judgment
}

func (Judge) Kind() string { return "Judge" }

// Deploy marks the change deployed.
type Deploy struct {
	ChangeCmd
	Ref string
}

func (Deploy) Kind() string { return "Deploy" }

// RecordOutcome records the observed outcome.
type RecordOutcome struct {
	ChangeCmd
	Outcome Outcome
}

func (RecordOutcome) Kind() string { return "RecordOutcome" }

// Settle closes the change; Derived lists constraints/decisions promoted from it.
type Settle struct {
	ChangeCmd
	Derived []core.ID
}

func (Settle) Kind() string { return "Settle" }

// ---- events ----

// ChangeOpened is the creation event.
type ChangeOpened struct {
	ID              core.ID       `json:"id"`
	ChangeKind      ChangeKind    `json:"kind"`
	Mode            ChangeMode    `json:"mode"`
	Scope           core.ScopeKey `json:"scope"`
	IntentRef       core.ID       `json:"intent_ref,omitempty"`
	NoIntent        bool          `json:"no_intent,omitempty"`
	Impact          Impact        `json:"impact"`
	Proposal        *Proposal     `json:"proposal,omitempty"`
	AutonomyApplied core.Level    `json:"autonomy_applied"`
	By              core.ActorRef `json:"by"`
	At              time.Time     `json:"at"`
}

func (ChangeOpened) Kind() string { return "ChangeOpened" }
func (ChangeOpened) Version() int { return 1 }

// ProposalAdded appends a proposal.
type ProposalAdded struct {
	ID       core.ID  `json:"id"`
	Proposal Proposal `json:"proposal"`
}

func (ProposalAdded) Kind() string { return "ProposalAdded" }
func (ProposalAdded) Version() int { return 1 }

// Judged records a judgment.
type Judged struct {
	ID       core.ID  `json:"id"`
	Judgment Judgment `json:"judgment"`
}

func (Judged) Kind() string { return "Judged" }
func (Judged) Version() int { return 1 }

// Deployed records deployment.
type Deployed struct {
	ID         core.ID    `json:"id"`
	Deployment Deployment `json:"deployment"`
}

func (Deployed) Kind() string { return "Deployed" }
func (Deployed) Version() int { return 1 }

// OutcomeRecorded records the outcome.
type OutcomeRecorded struct {
	ID      core.ID `json:"id"`
	Outcome Outcome `json:"outcome"`
}

func (OutcomeRecorded) Kind() string { return "OutcomeRecorded" }
func (OutcomeRecorded) Version() int { return 1 }

// Settled closes the change.
type Settled struct {
	ID      core.ID   `json:"id"`
	Derived []core.ID `json:"derived,omitempty"`
	At      time.Time `json:"at"`
}

func (Settled) Kind() string { return "Settled" }
func (Settled) Version() int { return 1 }

// ---- aggregate ----

// Decide applies the Change rules (spec §3.3, §3.6b, §4.2).
func (c Change) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case OpenChange:
		return c.open(m, ctx)
	case AddProposal:
		return c.addProposal(m)
	case Judge:
		return c.judge(m, ctx)
	case Deploy:
		return c.deploy(m, ctx)
	case RecordOutcome:
		return c.recordOutcome(m, ctx)
	case Settle:
		return c.settle(m, ctx)
	}
	return nil, core.Reject("unknown-command", "change: %s", cmd.Kind())
}

func (c Change) exists() bool { return c.State != StateNone }

func (c Change) open(m OpenChange, ctx core.DecideContext) ([]core.Event, error) {
	if c.exists() {
		return nil, core.Reject("exists", "change %s already exists", m.ID)
	}
	if m.ID.IsZero() {
		return nil, core.Reject("invalid", "change id required")
	}
	if m.ChangeKind != KindRealization && m.ChangeKind != KindHarness {
		return nil, core.Reject("invalid", "unknown change kind %q", m.ChangeKind)
	}
	switch m.Mode {
	case ModeManual, ModeSession, ModeDelegated, ModeAutonomous:
	default:
		return nil, core.Reject("invalid", "unknown change mode %q", m.Mode)
	}
	if err := m.Scope.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if m.IntentRef.IsZero() && !m.NoIntent {
		return nil, core.Reject("no-intent", "a change carries an intent or states explicitly that it has none")
	}
	for _, a := range m.Impact.Anchors {
		if _, err := core.ParseAnchor(a); err != nil {
			return nil, core.Reject("invalid", "%v", err)
		}
	}
	level := ctx.Autonomy
	if m.ChangeKind == KindHarness {
		level = core.L0 // harness changes are always judged by a human
	}
	if m.Mode == ModeAutonomous && level < core.L2 {
		return nil, core.Reject("autonomy", "autonomous mode requires L2 or above (effective %s)", level)
	}
	return []core.Event{ChangeOpened{
		ID: m.ID, ChangeKind: m.ChangeKind, Mode: m.Mode, Scope: m.Scope, IntentRef: m.IntentRef, NoIntent: m.NoIntent,
		Impact: m.Impact, Proposal: m.Proposal, AutonomyApplied: level, By: ctx.Actor, At: ctx.Now,
	}}, nil
}

func (c Change) addProposal(m AddProposal) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "change does not exist")
	}
	if c.State != StateProposed && c.State != StateJudged {
		return nil, core.Reject("bad-state", "cannot add a proposal to a %s change", c.State)
	}
	if m.Proposal.Ref == "" {
		return nil, core.Reject("invalid", "proposal ref required")
	}
	if err := m.Proposal.Actor.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	return []core.Event{ProposalAdded{ID: c.ID, Proposal: m.Proposal}}, nil
}

func (c Change) judge(m Judge, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "change does not exist")
	}
	if c.State != StateProposed && c.State != StateJudged {
		return nil, core.Reject("bad-state", "cannot judge a %s change", c.State)
	}
	j := m.Judgment
	if j.ID.IsZero() {
		return nil, core.Reject("invalid", "judgment id required")
	}
	switch j.Decision {
	case DecisionAccept, DecisionModify, DecisionReject:
	default:
		return nil, core.Reject("invalid", "unknown decision %q", j.Decision)
	}
	if err := j.By.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if j.By.IsAgent() {
		if c.Kind == KindHarness {
			return nil, core.Reject("human-only", "a harness change is judged by a human")
		}
		if c.AutonomyApplied < core.L1 {
			return nil, core.Reject("autonomy", "at L0 an agent only proposes")
		}
	}
	if !c.Impact.Empty() && j.Reason == "" {
		return nil, core.Reject("no-reason", "a judgment with impact needs a reason")
	}
	for _, prev := range c.Judgments {
		if prev.ID == j.ID {
			return nil, core.Reject("duplicate", "judgment %s already recorded", j.ID)
		}
	}
	return []core.Event{Judged{ID: c.ID, Judgment: j}}, nil
}

func (c Change) deploy(m Deploy, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "change does not exist")
	}
	if m.Ref == "" {
		return nil, core.Reject("invalid", "deployment ref required")
	}
	switch c.State {
	case StateJudged:
		if err := c.judgmentsAllow(ctx); err != nil {
			return nil, err
		}
	case StateProposed:
		if !c.Impact.Empty() {
			return nil, core.Reject("unjudged", "a change with impact is judged before deployment")
		}
	default:
		return nil, core.Reject("bad-state", "cannot deploy a %s change", c.State)
	}
	return []core.Event{Deployed{ID: c.ID, Deployment: Deployment{Ref: m.Ref, At: ctx.Now}}}, nil
}

func (c Change) recordOutcome(m RecordOutcome, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "change does not exist")
	}
	if c.State != StateDeployed {
		return nil, core.Reject("bad-state", "outcome is recorded on a deployed change (is %s)", c.State)
	}
	switch m.Outcome.Verdict {
	case Confirmed, Regressed, Unknown:
	default:
		return nil, core.Reject("invalid", "unknown verdict %q", m.Outcome.Verdict)
	}
	o := m.Outcome
	if o.At.IsZero() {
		o.At = ctx.Now
	}
	return []core.Event{OutcomeRecorded{ID: c.ID, Outcome: o}}, nil
}

func (c Change) settle(m Settle, ctx core.DecideContext) ([]core.Event, error) {
	if !c.exists() {
		return nil, core.Reject("not-found", "change does not exist")
	}
	if c.State == StateSettled {
		return nil, core.Reject("bad-state", "already settled")
	}
	if c.Impact.Empty() {
		// Outside the harness: settles without judgment (spec §1 "Change 도출").
		if len(m.Derived) > 0 {
			return nil, core.Reject("no-derivation", "nothing is derived from a change without impact")
		}
		return []core.Event{Settled{ID: c.ID, At: ctx.Now}}, nil
	}
	switch c.State {
	case StateObserved:
	case StateDeployed:
		if c.Deployment == nil || ctx.Now.Before(c.Deployment.At.Add(ctx.Policy.OutcomeWindow)) {
			return nil, core.Reject("observing", "outcome window is still open")
		}
	default:
		return nil, core.Reject("bad-state", "a change with impact settles after deployment and observation (is %s)", c.State)
	}
	if err := c.judgmentsAllow(ctx); err != nil {
		return nil, err
	}
	if ctx.Actor.IsAgent() && c.AutonomyApplied < core.L2 {
		return nil, core.Reject("autonomy", "below L2 a human settles")
	}
	return []core.Event{Settled{ID: c.ID, Derived: m.Derived, At: ctx.Now}}, nil
}

// judgmentsAllow checks that the recorded judgments permit proceeding:
// no standing reject, an accepting judgment, a human one at L0/L1 or for a
// harness change, and one from each required owner (other-team anchors).
func (c Change) judgmentsAllow(ctx core.DecideContext) error {
	if len(c.Judgments) == 0 {
		return core.Reject("unjudged", "no judgment recorded")
	}
	last := c.Judgments[len(c.Judgments)-1]
	if last.Decision == DecisionReject {
		return core.Reject("rejected", "the latest judgment rejected the change")
	}
	humanOK, anyOK := false, false
	judged := map[core.ID]bool{}
	for _, j := range c.Judgments {
		if j.Decision == DecisionReject {
			continue
		}
		anyOK = true
		if j.By.IsHuman() {
			humanOK = true
		}
		judged[j.By.ID] = true
	}
	if !anyOK {
		return core.Reject("unjudged", "no accepting judgment")
	}
	if (c.Kind == KindHarness || c.AutonomyApplied <= core.L1) && !humanOK {
		return core.Reject("human-required", "a human confirms at %s", c.AutonomyApplied)
	}
	for _, owner := range ctx.Judges {
		if !judged[owner] {
			return core.Reject("owner-required", "judgment from owner %s is required", owner)
		}
	}
	return nil
}

// Apply folds an event.
func (c Change) Apply(e core.Event) Change {
	switch v := e.(type) {
	case ChangeOpened:
		c = Change{
			ID: v.ID, Kind: v.ChangeKind, Mode: v.Mode, IntentRef: v.IntentRef, NoIntent: v.NoIntent,
			Impact: v.Impact, AutonomyApplied: v.AutonomyApplied, State: StateProposed, OpenedAt: v.At,
		}
		if v.Proposal != nil {
			c.Proposals = []Proposal{*v.Proposal}
		}
	case ProposalAdded:
		c.Proposals = append(c.Proposals, v.Proposal)
	case Judged:
		c.Judgments = append(c.Judgments, v.Judgment)
		c.State = StateJudged
	case Deployed:
		d := v.Deployment
		c.Deployment = &d
		c.State = StateDeployed
	case OutcomeRecorded:
		o := v.Outcome
		c.Outcome = &o
		c.State = StateObserved
	case Settled:
		c.Derived = v.Derived
		c.State = StateSettled
		c.SettledAt = v.At
	}
	return c
}
