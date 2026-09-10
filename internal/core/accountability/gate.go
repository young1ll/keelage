package accountability

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// GateDecision is the broker's answer.
type GateDecision string

const (
	GateAllow GateDecision = "allow"
	GateDeny  GateDecision = "deny"
	GateAsk   GateDecision = "ask"
)

// GateState is the lifecycle.
type GateState string

const (
	GateStateNone     GateState = ""
	GateStateDecided  GateState = "decided" // allow or deny, immediately
	GateStateOpen     GateState = "open"    // ask: waiting for a human
	GateStateResolved GateState = "resolved"
	GateStateExpired  GateState = "expired"
)

// Resolution is a human's answer to an open gate, from any channel.
type Resolution struct {
	Decision GateDecision  `json:"decision"` // allow | deny
	Reason   string        `json:"reason"`
	By       core.ActorRef `json:"by"`
	At       time.Time     `json:"at"`
	Sig      string        `json:"sig,omitempty"`
}

// Gate is the approval broker record (spec §3.6a, ADR 0005): an agent asks
// before acting; policy answers allow/deny/ask from the autonomy level, cap
// and ownership; a human resolves "ask" in their own channel; the answer is
// recorded as a judgment and feeds promotion.
type Gate struct {
	ID             core.ID       `json:"id"`
	Actor          core.ActorRef `json:"actor"`
	Scope          core.ScopeKey `json:"scope"`
	Anchors        []string      `json:"anchors,omitempty"`
	Action         string        `json:"action,omitempty"`
	ProposalRef    string        `json:"proposal_ref"`
	RequestedLevel core.Level    `json:"requested_level"`
	Decision       GateDecision  `json:"decision"`
	PolicyRef      string        `json:"policy_ref"`
	State          GateState     `json:"state"`
	OpenedAt       time.Time     `json:"opened_at"`
	ExpiresAt      time.Time     `json:"expires_at,omitempty"`
	Resolution     *Resolution   `json:"resolution,omitempty"`
}

// GateStream is the ledger stream for a gate.
func GateStream(id core.ID) string { return "gate/" + string(id) }

// GateIDFor derives the idempotent gate id for (actor, proposal_ref): asking
// twice for the same proposal returns the same gate.
func GateIDFor(actor core.ActorRef, proposalRef string) core.ID {
	h := sha256.Sum256([]byte(string(actor.ID) + "\x00" + proposalRef))
	return core.ID("gate-" + hex.EncodeToString(h[:13]))
}

// GateCmd is the identity part of every gate command (embed it).
type GateCmd struct {
	ID   core.ID
	Idem string
}

func (c GateCmd) Stream() string         { return GateStream(c.ID) }
func (c GateCmd) IdempotencyKey() string { return c.Idem }

// NewGateCmd builds the identity part of a gate command.
func NewGateCmd(id core.ID, idem string) GateCmd {
	return GateCmd{ID: id, Idem: idem}
}

// RequestGate is POST /gates.
type RequestGate struct {
	GateCmd
	Actor          core.ActorRef
	Scope          core.ScopeKey
	Anchors        []string
	Action         string
	ProposalRef    string
	RequestedLevel core.Level
}

func (RequestGate) Kind() string                 { return "RequestGate" }
func (c RequestGate) TargetScope() core.ScopeKey { return c.Scope }
func (c RequestGate) TouchedAnchors() []string   { return c.Anchors }

// ResolveGate is POST /gates/{id}/resolve.
type ResolveGate struct {
	GateCmd
	Resolution Resolution
}

func (ResolveGate) Kind() string { return "ResolveGate" }

// ExpireGate is issued by the expiry worker.
type ExpireGate struct{ GateCmd }

func (ExpireGate) Kind() string { return "ExpireGate" }

// GateDecided is an immediate allow/deny.
type GateDecided struct {
	ID             core.ID       `json:"id"`
	Actor          core.ActorRef `json:"actor"`
	Scope          core.ScopeKey `json:"scope"`
	Anchors        []string      `json:"anchors,omitempty"`
	Action         string        `json:"action,omitempty"`
	ProposalRef    string        `json:"proposal_ref"`
	RequestedLevel core.Level    `json:"requested_level"`
	Decision       GateDecision  `json:"decision"`
	PolicyRef      string        `json:"policy_ref"`
	At             time.Time     `json:"at"`
}

func (GateDecided) Kind() string { return "GateDecided" }
func (GateDecided) Version() int { return 1 }

// GateOpened is an "ask" waiting for a human.
type GateOpened struct {
	ID             core.ID       `json:"id"`
	Actor          core.ActorRef `json:"actor"`
	Scope          core.ScopeKey `json:"scope"`
	Anchors        []string      `json:"anchors,omitempty"`
	Action         string        `json:"action,omitempty"`
	ProposalRef    string        `json:"proposal_ref"`
	RequestedLevel core.Level    `json:"requested_level"`
	PolicyRef      string        `json:"policy_ref"`
	At             time.Time     `json:"at"`
	ExpiresAt      time.Time     `json:"expires_at"`
}

func (GateOpened) Kind() string { return "GateOpened" }
func (GateOpened) Version() int { return 1 }

// GateResolved is the human's answer — the gate's Judged record.
type GateResolved struct {
	ID         core.ID    `json:"id"`
	Resolution Resolution `json:"resolution"`
}

func (GateResolved) Kind() string { return "GateResolved" }
func (GateResolved) Version() int { return 1 }

// GateExpired closes an unanswered gate.
type GateExpired struct {
	ID core.ID   `json:"id"`
	At time.Time `json:"at"`
}

func (GateExpired) Kind() string { return "GateExpired" }
func (GateExpired) Version() int { return 1 }

// Decide applies the broker policy.
func (g Gate) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case RequestGate:
		return g.request(m, ctx)
	case ResolveGate:
		return g.resolve(m, ctx)
	case ExpireGate:
		return g.expire(ctx)
	}
	return nil, core.Reject("unknown-command", "gate: %s", cmd.Kind())
}

// Policy: deny above the cap; allow at or below the effective level when
// no other owner is involved; otherwise ask.
func policy(ctx core.DecideContext, requested core.Level) (GateDecision, string) {
	switch {
	case requested > ctx.Cap:
		return GateDeny, "cap:" + ctx.Cap.String() + "<requested:" + requested.String()
	case len(ctx.Judges) > 0:
		return GateAsk, "ownership:other-owner"
	case requested <= ctx.Autonomy:
		return GateAllow, "autonomy:" + ctx.Autonomy.String() + ">=requested:" + requested.String()
	}
	return GateAsk, "autonomy:" + ctx.Autonomy.String() + "<requested:" + requested.String()
}

func (g Gate) request(m RequestGate, ctx core.DecideContext) ([]core.Event, error) {
	if g.State != GateStateNone {
		return nil, nil // idempotent: same (actor, proposal_ref) returns the existing gate
	}
	if m.ID.IsZero() {
		return nil, core.Reject("invalid", "gate id required")
	}
	if err := m.Actor.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !m.Actor.IsAgent() {
		return nil, core.Reject("agent-only", "gates are asked by agents")
	}
	if err := m.Scope.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if m.ProposalRef == "" {
		return nil, core.Reject("invalid", "proposal ref required")
	}
	if !m.RequestedLevel.Valid() {
		return nil, core.Reject("invalid", "invalid requested level")
	}
	for _, a := range m.Anchors {
		if _, err := core.ParseAnchor(a); err != nil {
			return nil, core.Reject("invalid", "%v", err)
		}
	}
	decision, ref := policy(ctx, m.RequestedLevel)
	if decision == GateAsk {
		return []core.Event{GateOpened{
			ID: m.ID, Actor: m.Actor, Scope: m.Scope, Anchors: m.Anchors, Action: m.Action, ProposalRef: m.ProposalRef,
			RequestedLevel: m.RequestedLevel, PolicyRef: ref, At: ctx.Now, ExpiresAt: ctx.Now.Add(ctx.Policy.GateTTL),
		}}, nil
	}
	return []core.Event{GateDecided{
		ID: m.ID, Actor: m.Actor, Scope: m.Scope, Anchors: m.Anchors, Action: m.Action, ProposalRef: m.ProposalRef,
		RequestedLevel: m.RequestedLevel, Decision: decision, PolicyRef: ref, At: ctx.Now,
	}}, nil
}

func (g Gate) resolve(m ResolveGate, ctx core.DecideContext) ([]core.Event, error) {
	if g.State == GateStateNone {
		return nil, core.Reject("not-found", "gate does not exist")
	}
	if g.State != GateStateOpen {
		return nil, core.Reject("bad-state", "gate is %s", g.State)
	}
	r := m.Resolution
	if r.Decision != GateAllow && r.Decision != GateDeny {
		return nil, core.Reject("invalid", "resolution is allow or deny")
	}
	if err := r.By.Validate(); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if !r.By.IsHuman() {
		return nil, core.Reject("human-only", "a gate is resolved by a human")
	}
	if len(ctx.Judges) > 0 && !ctx.RequiresJudge(r.By.ID) {
		return nil, core.Reject("not-a-judge", "only a judge of the scope resolves this gate")
	}
	if r.Reason == "" {
		return nil, core.Reject("no-reason", "a resolution carries a reason")
	}
	if !ctx.Now.Before(g.ExpiresAt) {
		return nil, core.Reject("expired", "gate expired at %s", g.ExpiresAt.Format(time.RFC3339))
	}
	if r.At.IsZero() {
		r.At = ctx.Now
	}
	return []core.Event{GateResolved{ID: g.ID, Resolution: r}}, nil
}

func (g Gate) expire(ctx core.DecideContext) ([]core.Event, error) {
	if g.State == GateStateNone {
		return nil, core.Reject("not-found", "gate does not exist")
	}
	if g.State != GateStateOpen {
		return nil, nil
	}
	if ctx.Now.Before(g.ExpiresAt) {
		return nil, core.Reject("not-yet", "gate expires at %s", g.ExpiresAt.Format(time.RFC3339))
	}
	return []core.Event{GateExpired{ID: g.ID, At: ctx.Now}}, nil
}

// Apply folds an event.
func (g Gate) Apply(e core.Event) Gate {
	switch v := e.(type) {
	case GateDecided:
		g = Gate{
			ID: v.ID, Actor: v.Actor, Scope: v.Scope, Anchors: v.Anchors, Action: v.Action, ProposalRef: v.ProposalRef,
			RequestedLevel: v.RequestedLevel, Decision: v.Decision, PolicyRef: v.PolicyRef, State: GateStateDecided, OpenedAt: v.At,
		}
	case GateOpened:
		g = Gate{
			ID: v.ID, Actor: v.Actor, Scope: v.Scope, Anchors: v.Anchors, Action: v.Action, ProposalRef: v.ProposalRef,
			RequestedLevel: v.RequestedLevel, Decision: GateAsk, PolicyRef: v.PolicyRef, State: GateStateOpen, OpenedAt: v.At, ExpiresAt: v.ExpiresAt,
		}
	case GateResolved:
		r := v.Resolution
		g.Resolution = &r
		g.Decision = r.Decision
		g.State = GateStateResolved
	case GateExpired:
		g.State = GateStateExpired
	}
	return g
}

// RegisterEvents registers this context's events with the codec.
func RegisterEvents(c *core.Codec) {
	c.Register(ChangeOpened{})
	c.Register(ProposalAdded{})
	c.Register(Judged{})
	c.Register(Deployed{})
	c.Register(OutcomeRecorded{})
	c.Register(Settled{})
	c.Register(GateDecided{})
	c.Register(GateOpened{})
	c.Register(GateResolved{})
	c.Register(GateExpired{})
}
