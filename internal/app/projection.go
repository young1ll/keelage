package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/port"
)

// The daemon's projections are memory plus simple tables (implementation
// plan §4.4): enough to build DecideContext and the CLI views.

// ScopeEntry is one declared scope.
type ScopeEntry struct {
	Key   core.ScopeKey
	Level core.Level
	Cap   core.Level
}

// ScopeIndex projects AutonomyChanged/CapChanged into declared scopes and
// resolves the effective level and cap for a target.
type ScopeIndex struct {
	mu      sync.RWMutex
	entries map[string]ScopeEntry
}

// NewScopeIndex returns an empty index.
func NewScopeIndex() *ScopeIndex { return &ScopeIndex{entries: map[string]ScopeEntry{}} }

// Name implements port.Projector.
func (s *ScopeIndex) Name() string { return "scope_index" }

// Handle implements port.Projector.
func (s *ScopeIndex) Handle(_ context.Context, _ core.Envelope, ev core.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch v := ev.(type) {
	case harness.AutonomyChanged:
		e := s.get(v.Key)
		e.Level = v.To
		s.entries[v.Key.String()] = e
	case harness.CapChanged:
		e := s.get(v.Key)
		e.Cap = v.To
		s.entries[v.Key.String()] = e
	}
	return nil
}

func (s *ScopeIndex) get(k core.ScopeKey) ScopeEntry {
	if e, ok := s.entries[k.String()]; ok {
		return e
	}
	return ScopeEntry{Key: k, Level: core.L0, Cap: core.MaxLevel}
}

// Effective resolves target: the level comes from the narrowest declaring
// scope (context rule: the narrower specialises), the cap is the minimum
// over all applicable scopes (constraint rule: the wider binds). Level never
// exceeds cap. Undeclared means L0 / no cap.
func (s *ScopeIndex) Effective(target core.ScopeKey) (level, cap core.Level) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]core.ScopeKey, 0, len(s.entries))
	for _, e := range s.entries {
		keys = append(keys, e.Key)
	}
	applicable := core.Resolve(keys, target, core.KindConstraint) // wide → narrow
	level, cap = core.L0, core.MaxLevel
	for _, k := range applicable {
		e := s.entries[k.String()]
		level = e.Level
		if e.Cap < cap {
			cap = e.Cap
		}
	}
	if level > cap {
		level = cap
	}
	return level, cap
}

// Entries lists declared scopes, sorted by key.
func (s *ScopeIndex) Entries() []ScopeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ScopeEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

// ConstraintSummary is the current state of one constraint.
type ConstraintSummary struct {
	ID    core.ID
	Kind  harness.ConstraintKind
	Scope core.ScopeKey
	State harness.ConstraintState
	Level core.Level
}

// ConstraintIndex projects constraint events into current state.
type ConstraintIndex struct {
	mu      sync.RWMutex
	entries map[core.ID]ConstraintSummary
}

// NewConstraintIndex returns an empty index.
func NewConstraintIndex() *ConstraintIndex {
	return &ConstraintIndex{entries: map[core.ID]ConstraintSummary{}}
}

// Name implements port.Projector.
func (c *ConstraintIndex) Name() string { return "constraint_current" }

// Handle implements port.Projector.
func (c *ConstraintIndex) Handle(_ context.Context, _ core.Envelope, ev core.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch v := ev.(type) {
	case harness.ConstraintDrafted:
		c.entries[v.ID] = ConstraintSummary{ID: v.ID, Kind: v.ConstraintKind, Scope: v.Scope, State: harness.StateGenerated, Level: v.Level}
	case harness.ConstraintVerified:
		e := c.entries[v.ID]
		e.State = harness.StateVerified
		c.entries[v.ID] = e
	case harness.ConstraintPromoted:
		e := c.entries[v.ID]
		e.Scope = v.To
		c.entries[v.ID] = e
	case harness.ConstraintDemoted:
		e := c.entries[v.ID]
		e.Scope = v.To
		c.entries[v.ID] = e
	case harness.ConstraintSuperseded:
		e := c.entries[v.ID]
		e.State = harness.StateRetired
		c.entries[v.ID] = e
	case harness.ConstraintRetired:
		e := c.entries[v.ID]
		e.State = harness.StateRetired
		c.entries[v.ID] = e
	}
	return nil
}

// Get returns one constraint.
func (c *ConstraintIndex) Get(id core.ID) (ConstraintSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	return e, ok
}

// InForce lists verified or in-review constraints applying to target,
// widest first, then by ID.
func (c *ConstraintIndex) InForce(target core.ScopeKey) []ConstraintSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []ConstraintSummary{}
	for _, e := range c.entries {
		if (e.State == harness.StateVerified || e.State == harness.StateReview) && e.Scope.Contains(target) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		wi, wj := out[i].Scope.Width(), out[j].Scope.Width()
		if wi != wj {
			return wi > wj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SnapshotHash hashes the constraints in force for target (id, state,
// scope), the evidence of procedure recorded in event meta.
func (c *ConstraintIndex) SnapshotHash(target core.ScopeKey) string {
	in := c.InForce(target)
	if len(in) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range in {
		b.WriteString(string(e.ID))
		b.WriteByte(':')
		b.WriteString(string(e.State))
		b.WriteByte(':')
		b.WriteString(e.Scope.String())
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ChangeSummary is the current state of one change.
type ChangeSummary struct {
	ID              core.ID
	Kind            accountability.ChangeKind
	Scope           core.ScopeKey
	State           accountability.ChangeState
	AutonomyApplied core.Level
	Judgments       int
}

// ChangeIndex projects change events into current state.
type ChangeIndex struct {
	mu      sync.RWMutex
	entries map[core.ID]ChangeSummary
}

// NewChangeIndex returns an empty index.
func NewChangeIndex() *ChangeIndex { return &ChangeIndex{entries: map[core.ID]ChangeSummary{}} }

// Name implements port.Projector.
func (c *ChangeIndex) Name() string { return "change_current" }

// Handle implements port.Projector.
func (c *ChangeIndex) Handle(_ context.Context, _ core.Envelope, ev core.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch v := ev.(type) {
	case accountability.ChangeOpened:
		c.entries[v.ID] = ChangeSummary{ID: v.ID, Kind: v.ChangeKind, Scope: v.Scope, State: accountability.StateProposed, AutonomyApplied: v.AutonomyApplied}
	case accountability.Judged:
		e := c.entries[v.ID]
		e.State = accountability.StateJudged
		e.Judgments++
		c.entries[v.ID] = e
	case accountability.Deployed:
		e := c.entries[v.ID]
		e.State = accountability.StateDeployed
		c.entries[v.ID] = e
	case accountability.OutcomeRecorded:
		e := c.entries[v.ID]
		e.State = accountability.StateObserved
		c.entries[v.ID] = e
	case accountability.Settled:
		e := c.entries[v.ID]
		e.State = accountability.StateSettled
		c.entries[v.ID] = e
	}
	return nil
}

// Get returns one change.
func (c *ChangeIndex) Get(id core.ID) (ChangeSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	return e, ok
}

// Open lists changes that are not settled, by ID.
func (c *ChangeIndex) Open() []ChangeSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []ChangeSummary{}
	for _, e := range c.entries {
		if e.State != accountability.StateSettled {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ProjectionContext builds DecideContext from the indexes. Judges stay
// empty until the ownership map exists (week 9).
type ProjectionContext struct {
	Scopes      *ScopeIndex
	Constraints *ConstraintIndex
}

// Build implements ContextSource.
func (p ProjectionContext) Build(_ context.Context, cmd core.Command) (core.DecideContext, error) {
	var target core.ScopeKey
	if s, ok := cmd.(core.Scoped); ok {
		target = s.TargetScope()
	}
	level, cap := p.Scopes.Effective(target)
	return core.DecideContext{
		Autonomy:        level,
		Cap:             cap,
		ConstraintsHash: p.Constraints.SnapshotHash(target),
	}, nil
}

var _ port.Projector = (*ScopeIndex)(nil)
var _ port.Projector = (*ConstraintIndex)(nil)
var _ port.Projector = (*ChangeIndex)(nil)
