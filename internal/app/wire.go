package app

import (
	"strings"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/realization"
)

// NewCodec returns a codec with every bounded context's events registered.
func NewCodec() *core.Codec {
	c := core.NewCodec()
	harness.RegisterEvents(c)
	accountability.RegisterEvents(c)
	realization.RegisterEvents(c)
	return c
}

// RegisterAll wires the v0 aggregates.
func RegisterAll(p *Pipeline) {
	Register(p, func(string) harness.Constraint { return harness.Constraint{} },
		harness.DraftConstraint{}.Kind(), harness.VerifyConstraint{}.Kind(), harness.PromoteConstraint{}.Kind(),
		harness.DemoteConstraint{}.Kind(), harness.SupersedeConstraint{}.Kind(), harness.RetireConstraint{}.Kind(), harness.RebindConstraint{}.Kind())
	Register(p, func(stream string) harness.Scope {
		k, _ := core.ParseScopeKey(strings.TrimPrefix(stream, "scope/"))
		return harness.NewScope(k)
	}, harness.SetAutonomy{}.Kind(), harness.SetCap{}.Kind(), harness.RecordRegression{}.Kind())
	Register(p, func(string) accountability.Change { return accountability.Change{} },
		accountability.OpenChange{}.Kind(), accountability.AddProposal{}.Kind(), accountability.Judge{}.Kind(),
		accountability.Deploy{}.Kind(), accountability.RecordOutcome{}.Kind(), accountability.Settle{}.Kind())
	Register(p, func(string) accountability.Gate { return accountability.Gate{} },
		accountability.RequestGate{}.Kind(), accountability.ResolveGate{}.Kind(), accountability.ExpireGate{}.Kind())
	Register(p, func(string) accountability.Session { return accountability.Session{} },
		accountability.StartSession{}.Kind(), accountability.RecordTurn{}.Kind(), accountability.EndSession{}.Kind(), accountability.ShareSession{}.Kind())
	Register(p, func(string) realization.Anchor { return realization.Anchor{} },
		realization.RecordAnchor{}.Kind(), realization.VerifyAnchor{}.Kind(), realization.MoveAnchor{}.Kind())
}
