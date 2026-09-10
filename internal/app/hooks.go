package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/supply"
	"github.com/young1ll/keelage/internal/port"
)

// RepoID names a repository for scope keys: the root directory's base name
// (v0; remote-derived ids come with the server). CLI and daemon agree on it.
func RepoID(root string) string { return filepath.Base(filepath.Clean(root)) }

// FindRepoRoot walks up from dir to the directory holding .git.
func FindRepoRoot(dir string) (string, bool) {
	d := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// Session is what the daemon keeps about one live tool session: memory only,
// never the prompt text (spec §8).
type Session struct {
	ID        string
	Tool      string
	CWD       string
	Root      string
	StartedAt time.Time
	LastAt    time.Time
	Prompts   int
	Touched   []string // repo-relative files edited, in first-touch order
	Ended     bool
	EndReason string
}

// Hooks implements port.HookService and port.ContextQuery over the daemon's
// projections. pre_edit answers come from indexes only (no parsing), so the
// 50 ms budget holds; the caller's deadline is honoured between steps.
type Hooks struct {
	anchors     *AnchorIndex
	constraints *ConstraintIndex
	clock       port.Clock

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewHooks builds the service.
func NewHooks(anchors *AnchorIndex, constraints *ConstraintIndex, clock port.Clock) *Hooks {
	return &Hooks{anchors: anchors, constraints: constraints, clock: clock, sessions: map[string]*Session{}}
}

func (h *Hooks) session(ev supply.Event) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[ev.SessionID]
	if !ok {
		s = &Session{ID: ev.SessionID, Tool: ev.Tool, CWD: ev.CWD, StartedAt: h.clock.Now()}
		if root, ok := FindRepoRoot(ev.CWD); ok {
			s.Root = root
		}
		h.sessions[ev.SessionID] = s
	}
	if s.Root == "" && ev.CWD != "" {
		if root, ok := FindRepoRoot(ev.CWD); ok {
			s.Root = root
		}
	}
	s.LastAt = h.clock.Now()
	return s
}

// Sessions lists live and ended sessions, newest first.
func (h *Hooks) Sessions() []Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Hook implements port.HookService.
func (h *Hooks) Hook(ctx context.Context, ev supply.Event) (supply.Response, error) {
	if ev.SessionID == "" {
		return supply.Response{}, errors.New("hook: session id required")
	}
	s := h.session(ev)
	switch ev.Kind {
	case supply.SessionStart:
		return supply.Response{}, nil
	case supply.Prompt:
		h.mu.Lock()
		s.Prompts++ // the text itself is dropped here
		h.mu.Unlock()
		return supply.Response{}, nil
	case supply.PreEdit:
		return h.preEdit(ctx, s, ev)
	case supply.PostEdit:
		h.mu.Lock()
		for _, f := range ev.Files {
			if rel, ok := relPath(s.Root, f); ok && !contains(s.Touched, rel) {
				s.Touched = append(s.Touched, rel)
			}
		}
		h.mu.Unlock()
		return supply.Response{}, nil
	case supply.ToolResult:
		return supply.Response{}, nil
	case supply.SessionEnd:
		h.mu.Lock()
		s.Ended, s.EndReason = true, ev.Reason
		h.mu.Unlock()
		return supply.Response{}, nil
	}
	return supply.Response{}, errors.New("hook: unknown event kind " + string(ev.Kind))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func relPath(root, abs string) (string, bool) {
	if root == "" || abs == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (h *Hooks) preEdit(ctx context.Context, s *Session, ev supply.Event) (supply.Response, error) {
	var facts []supply.Facts
	for _, f := range ev.Files {
		if err := ctx.Err(); err != nil {
			return supply.Response{}, err
		}
		rel, ok := relPath(s.Root, f)
		if !ok {
			continue
		}
		facts = append(facts, h.factsFor(RepoID(s.Root), rel, core.ActorAgent))
	}
	return supply.Compose(facts), nil
}

// factsFor gathers anchors recorded in the file and constraints that apply
// to it: bound to the file or one of its anchors, or in force for its scope.
func (h *Hooks) factsFor(repo, rel string, actor core.ActorKind) supply.Facts {
	f := supply.Facts{File: rel, Repo: repo, Actor: actor}
	fileAnchor := core.Anchor{Scheme: core.SchemeCode, Path: rel}.String()
	seen := map[core.ID]bool{}
	addC := func(c ConstraintSummary) {
		if seen[c.ID] || (c.State != harness.StateVerified && c.State != harness.StateReview) {
			return
		}
		seen[c.ID] = true
		f.Constraints = append(f.Constraints, constraintFact(c))
	}
	for _, a := range h.anchors.All() {
		if a.Anchor.Scheme != core.SchemeCode || a.Anchor.Path != rel {
			continue
		}
		f.Anchors = append(f.Anchors, supply.AnchorFact{Key: a.Key, State: string(a.State), MovedTo: a.MovedTo})
		for _, c := range h.constraints.BoundTo(a.Key) {
			addC(c)
		}
	}
	for _, c := range h.constraints.BoundTo(fileAnchor) {
		addC(c)
	}
	for _, c := range h.constraints.InForce(core.ScopeKey{Repo: repo, PathGlob: rel}) {
		addC(c)
	}
	return f
}

func constraintFact(c ConstraintSummary) supply.ConstraintFact {
	return supply.ConstraintFact{
		ID: c.ID, Kind: string(c.Kind), State: string(c.State), Scope: c.Scope, Body: c.Body, Explanation: c.Explanation,
		Anchors: c.Anchors, Level: c.Level, DenyEdit: c.Kind == harness.KindAutonomy && c.Checkable == supply.DenyEditMarker,
	}
}

// WhatTouches implements port.ContextQuery.
func (h *Hooks) WhatTouches(_ context.Context, anchor, repo string) (supply.Touches, error) {
	a, err := core.ParseAnchor(anchor)
	if err != nil {
		return supply.Touches{}, err
	}
	t := supply.Touches{Anchor: a.String(), Constraints: []supply.ConstraintFact{}}
	if rec, ok := h.anchors.Get(a.String()); ok {
		t.State, t.MovedTo = string(rec.State), rec.MovedTo
	}
	seen := map[core.ID]bool{}
	for _, c := range h.constraints.BoundTo(a.String()) {
		if !seen[c.ID] {
			seen[c.ID] = true
			t.Constraints = append(t.Constraints, constraintFact(c))
		}
	}
	if a.Scheme == core.SchemeCode && repo != "" {
		for _, c := range h.constraints.InForce(core.ScopeKey{Repo: repo, PathGlob: a.Path}) {
			if !seen[c.ID] {
				seen[c.ID] = true
				t.Constraints = append(t.Constraints, constraintFact(c))
			}
		}
	}
	return t, nil
}

// Related implements port.ContextQuery: a constraint's anchors and lineage,
// or an anchor's constraints and relocation.
func (h *Hooks) Related(_ context.Context, id string) (supply.Related, error) {
	if c, ok := h.constraints.Get(core.ID(id)); ok {
		return supply.Related{ID: id, Kind: "constraint", Anchors: c.Anchors, State: string(c.State), Supersedes: string(c.Supersedes), SupersededBy: string(c.SupersededBy)}, nil
	}
	if a, err := core.ParseAnchor(id); err == nil {
		r := supply.Related{ID: a.String(), Kind: "anchor"}
		if rec, ok := h.anchors.Get(a.String()); ok {
			r.State, r.MovedTo = string(rec.State), rec.MovedTo
		}
		for _, c := range h.constraints.BoundTo(a.String()) {
			r.Constraints = append(r.Constraints, string(c.ID))
		}
		return r, nil
	}
	return supply.Related{}, errors.New("related: unknown id " + id)
}

var (
	_ port.HookService  = (*Hooks)(nil)
	_ port.ContextQuery = (*Hooks)(nil)
)
