package harness

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// DecisionState is generated (imported/drafted) | verified | retired.
type DecisionState string

const (
	DecisionStateNone      DecisionState = ""
	DecisionStateGenerated DecisionState = "generated"
	DecisionStateVerified  DecisionState = "verified"
	DecisionStateRetired   DecisionState = "retired"
)

// Decision is an ADR-shaped persistent object (spec §3.1): compiled from
// judgments or imported from the repository's docs/adr. v0 imports and
// verifies; promotion from judgments is a later week.
type Decision struct {
	ID           core.ID       `json:"id"`
	DecisionKind string        `json:"kind"` // adr
	Title        string        `json:"title"`
	Body         string        `json:"body"` // MADR text
	Scope        core.ScopeKey `json:"scope"`
	From         []core.ID     `json:"from,omitempty"` // source judgments
	Anchors      []string      `json:"anchors,omitempty"`
	Supersedes   core.ID       `json:"supersedes,omitempty"`
	SupersededBy core.ID       `json:"superseded_by,omitempty"`
	State        DecisionState `json:"state"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// DecisionStream is the ledger stream for a decision.
func DecisionStream(id core.ID) string { return "decision/" + string(id) }

// DecisionCmd is the identity part of every decision command (embed it).
type DecisionCmd struct {
	ID   core.ID
	Idem string
}

func (c DecisionCmd) Stream() string         { return DecisionStream(c.ID) }
func (c DecisionCmd) IdempotencyKey() string { return c.Idem }

// DraftDecision creates a decision (imported or authored).
type DraftDecision struct {
	DecisionCmd
	Title      string
	Body       string
	Scope      core.ScopeKey
	From       []core.ID
	Anchors    []string
	Supersedes core.ID
}

func (DraftDecision) Kind() string { return "DraftDecision" }

// VerifyDecision marks it verified (a human act).
type VerifyDecision struct{ DecisionCmd }

func (VerifyDecision) Kind() string { return "VerifyDecision" }

// ReviseDecision replaces title/body (re-import after the file changed).
type ReviseDecision struct {
	DecisionCmd
	Title string
	Body  string
}

func (ReviseDecision) Kind() string { return "ReviseDecision" }

// SupersedeDecision retires this decision in favour of By.
type SupersedeDecision struct {
	DecisionCmd
	By core.ID
}

func (SupersedeDecision) Kind() string { return "SupersedeDecision" }

// RetireDecision retires.
type RetireDecision struct{ DecisionCmd }

func (RetireDecision) Kind() string { return "RetireDecision" }

// DecisionDrafted is the creation event.
type DecisionDrafted struct {
	ID         core.ID       `json:"id"`
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Scope      core.ScopeKey `json:"scope"`
	From       []core.ID     `json:"from,omitempty"`
	Anchors    []string      `json:"anchors,omitempty"`
	Supersedes core.ID       `json:"supersedes,omitempty"`
	By         core.ActorRef `json:"by"`
	At         time.Time     `json:"at"`
}

func (DecisionDrafted) Kind() string { return "DecisionDrafted" }
func (DecisionDrafted) Version() int { return 1 }

// DecisionVerified marks verification.
type DecisionVerified struct {
	ID core.ID       `json:"id"`
	By core.ActorRef `json:"by"`
	At time.Time     `json:"at"`
}

func (DecisionVerified) Kind() string { return "DecisionVerified" }
func (DecisionVerified) Version() int { return 1 }

// DecisionRevised replaces the text; state falls back to generated until
// re-verified.
type DecisionRevised struct {
	ID    core.ID       `json:"id"`
	Title string        `json:"title"`
	Body  string        `json:"body"`
	By    core.ActorRef `json:"by"`
	At    time.Time     `json:"at"`
}

func (DecisionRevised) Kind() string { return "DecisionRevised" }
func (DecisionRevised) Version() int { return 1 }

// DecisionSuperseded retires in favour of another.
type DecisionSuperseded struct {
	ID core.ID   `json:"id"`
	By core.ID   `json:"by"`
	At time.Time `json:"at"`
}

func (DecisionSuperseded) Kind() string { return "DecisionSuperseded" }
func (DecisionSuperseded) Version() int { return 1 }

// DecisionRetired retires.
type DecisionRetired struct {
	ID core.ID   `json:"id"`
	At time.Time `json:"at"`
}

func (DecisionRetired) Kind() string { return "DecisionRetired" }
func (DecisionRetired) Version() int { return 1 }

// Decide applies the decision rules.
func (d Decision) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case DraftDecision:
		if d.State != DecisionStateNone {
			return nil, core.Reject("exists", "decision %s already exists", m.ID)
		}
		if m.ID.IsZero() || m.Title == "" || m.Body == "" {
			return nil, core.Reject("invalid", "id, title and body required")
		}
		if err := m.Scope.Validate(); err != nil {
			return nil, core.Reject("invalid", "%v", err)
		}
		for _, a := range m.Anchors {
			if _, err := core.ParseAnchor(a); err != nil {
				return nil, core.Reject("invalid", "%v", err)
			}
		}
		return []core.Event{DecisionDrafted{ID: m.ID, Title: m.Title, Body: m.Body, Scope: m.Scope, From: m.From, Anchors: m.Anchors, Supersedes: m.Supersedes, By: ctx.Actor, At: ctx.Now}}, nil
	case VerifyDecision:
		if d.State == DecisionStateNone {
			return nil, core.Reject("not-found", "decision does not exist")
		}
		if !ctx.Actor.IsHuman() {
			return nil, core.Reject("human-only", "verification is a human act")
		}
		if d.State != DecisionStateGenerated {
			return nil, core.Reject("bad-state", "cannot verify a %s decision", d.State)
		}
		return []core.Event{DecisionVerified{ID: d.ID, By: ctx.Actor, At: ctx.Now}}, nil
	case ReviseDecision:
		if d.State == DecisionStateNone {
			return nil, core.Reject("not-found", "decision does not exist")
		}
		if d.State == DecisionStateRetired {
			return nil, core.Reject("bad-state", "decision is retired")
		}
		if m.Title == d.Title && m.Body == d.Body {
			return nil, nil
		}
		if m.Title == "" || m.Body == "" {
			return nil, core.Reject("invalid", "title and body required")
		}
		return []core.Event{DecisionRevised{ID: d.ID, Title: m.Title, Body: m.Body, By: ctx.Actor, At: ctx.Now}}, nil
	case SupersedeDecision:
		if d.State == DecisionStateNone {
			return nil, core.Reject("not-found", "decision does not exist")
		}
		if d.State == DecisionStateRetired {
			return nil, core.Reject("bad-state", "decision is retired")
		}
		if m.By.IsZero() || m.By == d.ID {
			return nil, core.Reject("invalid", "supersede needs the successor id")
		}
		return []core.Event{DecisionSuperseded{ID: d.ID, By: m.By, At: ctx.Now}}, nil
	case RetireDecision:
		if d.State == DecisionStateNone {
			return nil, core.Reject("not-found", "decision does not exist")
		}
		if d.State == DecisionStateRetired {
			return nil, core.Reject("bad-state", "already retired")
		}
		return []core.Event{DecisionRetired{ID: d.ID, At: ctx.Now}}, nil
	}
	return nil, core.Reject("unknown-command", "decision: %s", cmd.Kind())
}

// Apply folds an event.
func (d Decision) Apply(e core.Event) Decision {
	switch v := e.(type) {
	case DecisionDrafted:
		d = Decision{ID: v.ID, DecisionKind: "adr", Title: v.Title, Body: v.Body, Scope: v.Scope, From: v.From, Anchors: v.Anchors, Supersedes: v.Supersedes, State: DecisionStateGenerated, UpdatedAt: v.At}
	case DecisionVerified:
		d.State, d.UpdatedAt = DecisionStateVerified, v.At
	case DecisionRevised:
		d.Title, d.Body, d.State, d.UpdatedAt = v.Title, v.Body, DecisionStateGenerated, v.At
	case DecisionSuperseded:
		d.State, d.SupersededBy, d.UpdatedAt = DecisionStateRetired, v.By, v.At
	case DecisionRetired:
		d.State, d.UpdatedAt = DecisionStateRetired, v.At
	}
	return d
}
