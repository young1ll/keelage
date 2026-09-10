package realization

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// AnchorState is the staleness state of a recorded anchor (spec §1 "낡음
// 판정": three hash layers, two stages review/stale, a signature change is
// always stale).
type AnchorState string

const (
	StateNone       AnchorState = ""
	StateRecorded   AnchorState = "recorded"   // hash captured, not yet re-verified
	StateVerified   AnchorState = "verified"   // last verify matched
	StateReview     AnchorState = "review"     // body changed: someone should look
	StateStale      AnchorState = "stale"      // signature changed: bindings are stale
	StateUnrealized AnchorState = "unrealized" // the symbol/file no longer exists
)

// Anchor is the aggregate that tracks one realization anchor's hash and
// state over time. Persistent objects point at it; it never points back.
type Anchor struct {
	Key       string      `json:"key"` // canonical anchor string
	Hash      core.Hash3  `json:"hash"`
	State     AnchorState `json:"state"`
	Ref       string      `json:"ref,omitempty"` // commit the hash was taken at
	MovedTo   string      `json:"moved_to,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
	Verifies  int         `json:"verifies"`
	Staled    int         `json:"staled"`
}

// AnchorStream is the ledger stream for an anchor.
func AnchorStream(key string) string { return "anchor/" + key }

// AnchorCmd is the identity part of every anchor command (embed it).
type AnchorCmd struct {
	Key  string
	Idem string
}

func (c AnchorCmd) Stream() string         { return AnchorStream(c.Key) }
func (c AnchorCmd) IdempotencyKey() string { return c.Idem }

// RecordAnchor captures the current hash (first time or re-baseline after a
// human accepted the change).
type RecordAnchor struct {
	AnchorCmd
	Hash core.Hash3
	Ref  string
}

func (RecordAnchor) Kind() string { return "RecordAnchor" }

// VerifyAnchor compares the current hash with the recorded one. Exists is
// false when the resolver could not find the symbol or file.
type VerifyAnchor struct {
	AnchorCmd
	Current core.Hash3
	Exists  bool
	Ref     string
}

func (VerifyAnchor) Kind() string { return "VerifyAnchor" }

// MoveAnchor records an unambiguous relocation of the same signature.
type MoveAnchor struct {
	AnchorCmd
	To   string
	Hash core.Hash3
	Ref  string
}

func (MoveAnchor) Kind() string { return "MoveAnchor" }

// AnchorRecorded is the baseline event.
type AnchorRecorded struct {
	Key  string        `json:"key"`
	Hash core.Hash3    `json:"hash"`
	Ref  string        `json:"ref,omitempty"`
	By   core.ActorRef `json:"by"`
	At   time.Time     `json:"at"`
}

func (AnchorRecorded) Kind() string { return "AnchorRecorded" }
func (AnchorRecorded) Version() int { return 1 }

// AnchorVerified: the hash still matches (or only the file layer moved).
type AnchorVerified struct {
	Key    string          `json:"key"`
	Hash   core.Hash3      `json:"hash"`
	Change core.HashChange `json:"change"`
	Ref    string          `json:"ref,omitempty"`
	At     time.Time       `json:"at"`
}

func (AnchorVerified) Kind() string { return "AnchorVerified" }
func (AnchorVerified) Version() int { return 1 }

// AnchorStaled: the body (review) or the signature (stale) changed, or the
// anchor is gone (unrealized).
type AnchorStaled struct {
	Key   string          `json:"key"`
	State AnchorState     `json:"state"`
	Cause core.HashChange `json:"cause"`
	Old   core.Hash3      `json:"old"`
	New   core.Hash3      `json:"new"`
	Ref   string          `json:"ref,omitempty"`
	At    time.Time       `json:"at"`
}

func (AnchorStaled) Kind() string { return "AnchorStaled" }
func (AnchorStaled) Version() int { return 1 }

// AnchorMoved: same signature, new location.
type AnchorMoved struct {
	Key  string     `json:"key"`
	To   string     `json:"to"`
	Hash core.Hash3 `json:"hash"`
	Ref  string     `json:"ref,omitempty"`
	At   time.Time  `json:"at"`
}

func (AnchorMoved) Kind() string { return "AnchorMoved" }
func (AnchorMoved) Version() int { return 1 }

// Decide applies the staleness rules.
func (a Anchor) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case RecordAnchor:
		return a.record(m, ctx)
	case VerifyAnchor:
		return a.verify(m, ctx)
	case MoveAnchor:
		return a.move(m, ctx)
	}
	return nil, core.Reject("unknown-command", "anchor: %s", cmd.Kind())
}

func (a Anchor) record(m RecordAnchor, ctx core.DecideContext) ([]core.Event, error) {
	if _, err := core.ParseAnchor(m.Key); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if m.Hash == (core.Hash3{}) {
		return nil, core.Reject("invalid", "hash required")
	}
	if a.State != StateNone && a.Hash == m.Hash && a.State != StateUnrealized {
		return nil, nil // already at this baseline
	}
	return []core.Event{AnchorRecorded{Key: m.Key, Hash: m.Hash, Ref: m.Ref, By: ctx.Actor, At: ctx.Now}}, nil
}

// Staging (spec §1): file-only change → still verified; body change →
// review; signature change → stale, always; gone → unrealized.
func (a Anchor) verify(m VerifyAnchor, ctx core.DecideContext) ([]core.Event, error) {
	if a.State == StateNone {
		return nil, core.Reject("not-found", "anchor %s was never recorded", m.Key)
	}
	if !m.Exists {
		if a.State == StateUnrealized {
			return nil, nil
		}
		return []core.Event{AnchorStaled{Key: a.Key, State: StateUnrealized, Cause: core.HashSignature, Old: a.Hash, New: m.Current, Ref: m.Ref, At: ctx.Now}}, nil
	}
	change := a.Hash.Cmp(m.Current)
	switch change {
	case core.HashNone, core.HashFile:
		return []core.Event{AnchorVerified{Key: a.Key, Hash: m.Current, Change: change, Ref: m.Ref, At: ctx.Now}}, nil
	case core.HashBody:
		if a.State == StateStale {
			return nil, nil // already worse; wait for a re-baseline
		}
		return []core.Event{AnchorStaled{Key: a.Key, State: StateReview, Cause: change, Old: a.Hash, New: m.Current, Ref: m.Ref, At: ctx.Now}}, nil
	default:
		return []core.Event{AnchorStaled{Key: a.Key, State: StateStale, Cause: change, Old: a.Hash, New: m.Current, Ref: m.Ref, At: ctx.Now}}, nil
	}
}

func (a Anchor) move(m MoveAnchor, ctx core.DecideContext) ([]core.Event, error) {
	if a.State == StateNone {
		return nil, core.Reject("not-found", "anchor %s was never recorded", m.Key)
	}
	if _, err := core.ParseAnchor(m.To); err != nil {
		return nil, core.Reject("invalid", "%v", err)
	}
	if m.To == a.Key {
		return nil, core.Reject("invalid", "move to itself")
	}
	if m.Hash.Signature != a.Hash.Signature {
		return nil, core.Reject("not-a-move", "signature differs: that is a change, not a move")
	}
	return []core.Event{AnchorMoved{Key: a.Key, To: m.To, Hash: m.Hash, Ref: m.Ref, At: ctx.Now}}, nil
}

// Apply folds an event.
func (a Anchor) Apply(e core.Event) Anchor {
	switch v := e.(type) {
	case AnchorRecorded:
		a = Anchor{Key: v.Key, Hash: v.Hash, State: StateRecorded, Ref: v.Ref, UpdatedAt: v.At, Verifies: a.Verifies, Staled: a.Staled}
	case AnchorVerified:
		a.Hash = v.Hash // the file layer may drift without consequence
		a.State = StateVerified
		a.Ref = v.Ref
		a.UpdatedAt = v.At
		a.Verifies++
	case AnchorStaled:
		a.State = v.State
		a.UpdatedAt = v.At
		a.Staled++
	case AnchorMoved:
		a.MovedTo = v.To
		a.UpdatedAt = v.At
	}
	return a
}

// RegisterEvents registers this context's events with the codec.
func RegisterEvents(c *core.Codec) {
	c.Register(AnchorRecorded{})
	c.Register(AnchorVerified{})
	c.Register(AnchorStaled{})
	c.Register(AnchorMoved{})
}
