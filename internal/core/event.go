package core

import "fmt"

// Event is an immutable domain fact. Kind and Version identify its schema;
// a schema change is a new version plus an upcaster — existing events are
// never rewritten (ledger principle).
type Event interface {
	Kind() string
	Version() int
}

// Command is a request to change one aggregate. Stream identifies the
// aggregate; IdempotencyKey lets hooks and sync retry safely (the ledger
// rejects a duplicate (stream, key)).
type Command interface {
	Kind() string
	Stream() string
	IdempotencyKey() string
}

// Scoped is implemented by commands whose decision depends on a scope's
// autonomy level and effective constraints.
type Scoped interface {
	TargetScope() ScopeKey
}

// Anchored is implemented by commands that touch realization anchors, so the
// pipeline can look up ownership (judges) for them.
type Anchored interface {
	TouchedAnchors() []string
}

// Root is an aggregate: Decide is deterministic and side-effect free, Apply
// never fails. T is the concrete aggregate type (value semantics).
type Root[T any] interface {
	Decide(cmd Command, ctx DecideContext) ([]Event, error)
	Apply(e Event) T
}

// Replay folds events onto zero.
func Replay[T Root[T]](zero T, evs []Event) T {
	s := zero
	for _, e := range evs {
		s = s.Apply(e)
	}
	return s
}

// Rejection is a domain rule refusing a command. The pipeline records it as
// a Rejected event (no silent failure) and returns it to the caller.
// Infrastructure errors are ordinary errors, not Rejections.
type Rejection struct {
	Code   string
	Reason string
}

func (r *Rejection) Error() string { return r.Code + ": " + r.Reason }

// Reject builds a Rejection.
func Reject(code, format string, args ...any) error {
	return &Rejection{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// AsRejection unwraps a Rejection from err.
func AsRejection(err error) (*Rejection, bool) {
	for err != nil {
		if r, ok := err.(*Rejection); ok { //nolint:errorlint // explicit walk
			return r, true
		}
		u, ok := err.(interface{ Unwrap() error }) //nolint:errorlint // explicit walk
		if !ok {
			return nil, false
		}
		err = u.Unwrap()
	}
	return nil, false
}

// RejectedStream is the ledger stream that records refused commands.
const RejectedStream = "rejected"

// Rejected records a refused command (spec §3.6c): conflicts are kept, not
// dropped. It lives on RejectedStream so aggregate versions stay untouched.
type Rejected struct {
	Command string   `json:"command"`
	Target  string   `json:"target"` // the aggregate stream the command addressed
	Code    string   `json:"code"`
	Reason  string   `json:"reason"`
	Actor   ActorRef `json:"actor"`
}

func (Rejected) Kind() string { return "Rejected" }
func (Rejected) Version() int { return 1 }
