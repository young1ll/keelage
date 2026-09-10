package core

import "errors"

// ID is a ULID (sortable, 26 chars). Generated through port.IDGen so tests are deterministic.
type ID string

func (id ID) String() string { return string(id) }

// IsZero reports whether the ID is unset.
func (id ID) IsZero() bool { return id == "" }

// ActorKind distinguishes people from agent instances. Both are first-class
// actors with their own keys (spec §3.6).
type ActorKind string

const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"
)

// ActorRef identifies who acted. Signature verification is an adapter
// concern; core only compares references.
type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   ID        `json:"id"`
	// Key is the signing key fingerprint.
	Key string `json:"key,omitempty"`
	// Owner is the responsible human for an agent instance. Required for agents.
	Owner ID `json:"owner,omitempty"`
}

// IsHuman reports whether the actor is a person.
func (a ActorRef) IsHuman() bool { return a.Kind == ActorHuman }

// IsAgent reports whether the actor is an agent instance.
func (a ActorRef) IsAgent() bool { return a.Kind == ActorAgent }

// Validate checks the reference is well formed: known kind, non-empty ID,
// and an owner for agents.
func (a ActorRef) Validate() error {
	switch a.Kind {
	case ActorHuman:
	case ActorAgent:
		if a.Owner.IsZero() {
			return errors.New("actor: agent requires an owner")
		}
	default:
		return errors.New("actor: unknown kind")
	}
	if a.ID.IsZero() {
		return errors.New("actor: empty id")
	}
	return nil
}
