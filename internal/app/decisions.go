package app

import (
	"context"
	"sort"
	"sync"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
)

// DecisionSummary is the current state of one decision.
type DecisionSummary struct {
	ID           core.ID
	Title        string
	State        harness.DecisionState
	Scope        core.ScopeKey
	Anchors      []string
	Supersedes   core.ID
	SupersededBy core.ID
}

// DecisionIndex projects decision events.
type DecisionIndex struct {
	mu      sync.RWMutex
	entries map[core.ID]DecisionSummary
}

// NewDecisionIndex returns an empty index.
func NewDecisionIndex() *DecisionIndex { return &DecisionIndex{entries: map[core.ID]DecisionSummary{}} }

// Name implements port.Projector.
func (d *DecisionIndex) Name() string { return "decision_current" }

// Handle implements port.Projector.
func (d *DecisionIndex) Handle(_ context.Context, _ core.Envelope, ev core.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch v := ev.(type) {
	case harness.DecisionDrafted:
		d.entries[v.ID] = DecisionSummary{ID: v.ID, Title: v.Title, State: harness.DecisionStateGenerated, Scope: v.Scope, Anchors: v.Anchors, Supersedes: v.Supersedes}
	case harness.DecisionVerified:
		e := d.entries[v.ID]
		e.State = harness.DecisionStateVerified
		d.entries[v.ID] = e
	case harness.DecisionRevised:
		e := d.entries[v.ID]
		e.Title, e.State = v.Title, harness.DecisionStateGenerated
		d.entries[v.ID] = e
	case harness.DecisionSuperseded:
		e := d.entries[v.ID]
		e.State, e.SupersededBy = harness.DecisionStateRetired, v.By
		d.entries[v.ID] = e
	case harness.DecisionRetired:
		e := d.entries[v.ID]
		e.State = harness.DecisionStateRetired
		d.entries[v.ID] = e
	}
	return nil
}

// Get returns one decision.
func (d *DecisionIndex) Get(id core.ID) (DecisionSummary, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.entries[id]
	return e, ok
}

// All lists decisions by ID.
func (d *DecisionIndex) All() []DecisionSummary {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]DecisionSummary, 0, len(d.entries))
	for _, e := range d.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
