package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
)

// SessionSummary is the current state of one recorded session.
type SessionSummary struct {
	ID        string
	Tool      string
	Repo      string
	Owner     core.ID
	State     accountability.SessionState
	StartedAt time.Time
	EndedAt   time.Time
	Turns     int
	Touched   []string
	Judgments []accountability.SessionJudgment
	Summary   string
	Shared    []string
}

// Stream is the session's ledger stream.
func (s SessionSummary) Stream() string { return accountability.SessionStream(s.Tool, s.ID) }

// SessionIndex projects session events.
type SessionIndex struct {
	mu      sync.RWMutex
	entries map[string]SessionSummary // keyed by stream
}

// NewSessionIndex returns an empty index.
func NewSessionIndex() *SessionIndex { return &SessionIndex{entries: map[string]SessionSummary{}} }

// Name implements port.Projector.
func (s *SessionIndex) Name() string { return "session_current" }

// Handle implements port.Projector.
func (s *SessionIndex) Handle(_ context.Context, env core.Envelope, ev core.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch v := ev.(type) {
	case accountability.SessionStarted:
		s.entries[env.Stream] = SessionSummary{ID: v.ID, Tool: v.Tool, Repo: v.Repo, Owner: v.Agent.Owner, State: accountability.SessionStateOpen, StartedAt: v.At}
	case accountability.TurnRecorded:
		e := s.entries[env.Stream]
		e.Turns++
		for _, f := range v.Turn.Files {
			if !contains(e.Touched, f) {
				e.Touched = append(e.Touched, f)
			}
		}
		if v.Turn.Class != accountability.TurnAccept {
			e.Judgments = append(e.Judgments, accountability.SessionJudgment{Turn: v.Turn.Seq, Class: v.Turn.Class, Files: v.Turn.Files, Signal: v.Turn.Signal, At: v.Turn.At})
		}
		s.entries[env.Stream] = e
	case accountability.SessionEnded:
		e := s.entries[env.Stream]
		e.State, e.EndedAt, e.Summary = accountability.SessionStateEnded, v.At, v.Summary
		s.entries[env.Stream] = e
	case accountability.SessionShared:
		e := s.entries[env.Stream]
		e.State, e.Shared = accountability.SessionStateShared, v.Parts
		s.entries[env.Stream] = e
	}
	return nil
}

// All lists sessions, newest first.
func (s *SessionIndex) All() []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionSummary, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Get returns one session by stream.
func (s *SessionIndex) Get(stream string) (SessionSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[stream]
	return e, ok
}

// Touching lists streams of sessions in repo that touched any of files and
// were active after since. Used to bundle sessions into a derived Change.
func (s *SessionIndex) Touching(repo string, files []string, since time.Time) []string {
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	var out []string
	for _, e := range s.All() {
		if e.Repo != repo || e.StartedAt.Before(since) && (e.EndedAt.IsZero() || e.EndedAt.Before(since)) {
			continue
		}
		for _, f := range e.Touched {
			if set[f] {
				out = append(out, e.Stream())
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
