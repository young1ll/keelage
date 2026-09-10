package core

import "time"

// DecideContext carries the external facts a Decide needs, as values: the
// aggregate never calls a repository. The pipeline builds it from projections
// and the clock.
type DecideContext struct {
	Now   time.Time
	Actor ActorRef
	// Autonomy is the effective level for the command's scope; Cap the
	// effective upper bound (widest wins).
	Autonomy Level
	Cap      Level
	Policy   Policy
	// Judges are the actors whose judgment is required because the command
	// touches anchors they own (ownership map). Empty means no owner gate.
	Judges []ID
	// ConstraintsHash is the snapshot hash of the constraints in force, kept
	// in event meta as evidence of procedure (spec §3.7).
	ConstraintsHash string
}

// RequiresJudge reports whether id is one of the required judges.
func (c DecideContext) RequiresJudge(id ID) bool {
	for _, j := range c.Judges {
		if j == id {
			return true
		}
	}
	return false
}

// Policy holds tunable thresholds. Values are configuration, not rules: the
// rules that use them live in the aggregates.
type Policy struct {
	// PromotionMinOutcomes is the minimum number of scored outcomes without a
	// regression since the last demotion before a level can be raised.
	PromotionMinOutcomes int
	// OutcomeWindow is how long a deployed Change is observed before it may
	// settle without an outcome.
	OutcomeWindow time.Duration
	// GateTTL is how long an "ask" gate stays open.
	GateTTL time.Duration
}

// DefaultPolicy is the v0 default.
func DefaultPolicy() Policy {
	return Policy{
		PromotionMinOutcomes: 10,
		OutcomeWindow:        24 * time.Hour,
		GateTTL:              1 * time.Hour,
	}
}
