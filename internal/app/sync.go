package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// SyncState is the daemon's cursor pair: how far the personal ledger has
// been offered to the server and how far the server's team layer has been
// copied into the cache. cmd persists it in ~/.keelage/sync.json.
type SyncState struct {
	DaemonID   string    `json:"daemon_id"`
	PushCursor int64     `json:"push_cursor"`
	PullCursor int64     `json:"pull_cursor"`
	PushedAt   time.Time `json:"pushed_at,omitempty"`
	PulledAt   time.Time `json:"pulled_at,omitempty"`
}

// PushReport says what one push did.
type PushReport struct {
	Scanned  int64 `json:"scanned"`
	Offered  int   `json:"offered"`
	Accepted int   `json:"accepted"`
	// Rejected lists refusals; they are logged, not retried (spec §4.4 v0:
	// the next pull overwrites local state with the server copy).
	Rejected []port.PushRejected `json:"rejected"`
	Head     int64               `json:"server_head"`
}

// PullReport says what one pull did.
type PullReport struct {
	Received int   `json:"received"`
	Cached   int   `json:"cached"`
	Skipped  int   `json:"skipped"` // this daemon's own records, already local
	Cursor   int64 `json:"cursor"`
}

// Syncer is the daemon side of the protocol (spec §4.4, v0: push at commit
// or share, pull the team layer into a cache).
type Syncer struct {
	Local  port.Ledger
	Cache  port.Ledger
	Codec  *core.Codec
	Client port.TeamClient
	// DaemonID is the id registered with the server (the key fingerprint).
	DaemonID string
	// Constraints and Sessions decide what stays personal.
	Constraints *ConstraintIndex
	Sessions    *SessionIndex
	// Projectors receive pulled records (nil: cache only).
	Projectors []port.Projector
	Clock      port.Clock
	// Batch is the push page size (default 200).
	Batch int
}

// shareable applies the personal filters on top of Shareable: constraints
// and scopes on the person axis stay local; sessions travel only after
// the person shared them.
func (s *Syncer) shareable(e core.Envelope) bool {
	switch {
	case strings.HasPrefix(e.Stream, "constraint/"):
		if s.Constraints != nil {
			if c, ok := s.Constraints.Get(core.ID(strings.TrimPrefix(e.Stream, "constraint/"))); ok && c.Scope.Person != "" {
				return false
			}
		}
		return true
	case strings.HasPrefix(e.Stream, "scope/"):
		k, err := core.ParseScopeKey(strings.TrimPrefix(e.Stream, "scope/"))
		return err == nil && k.Person == ""
	case strings.HasPrefix(e.Stream, "session/"):
		if s.Sessions == nil {
			return false
		}
		sum, ok := s.Sessions.Get(e.Stream)
		return ok && len(sum.Shared) > 0
	}
	return Shareable(e.Stream)
}

// Push offers every shareable local record after the cursor. The cursor
// advances past what the server answered for (accepted or rejected); a
// transport failure leaves it where it was, so the next push retries.
func (s *Syncer) Push(ctx context.Context, st *SyncState) (PushReport, error) {
	batch := s.Batch
	if batch <= 0 {
		batch = 200
	}
	var rep PushReport
	for {
		page, err := s.Local.ReadAll(ctx, st.PushCursor+1, batch)
		if err != nil {
			return rep, err
		}
		if len(page) == 0 {
			return rep, nil
		}
		var offer []core.Envelope
		for _, e := range page {
			if e.Stream == core.RejectedStream || !s.shareable(e) {
				continue
			}
			offer = append(offer, e)
		}
		if len(offer) > 0 {
			res, err := s.Client.Push(ctx, port.PushRequest{DaemonID: s.DaemonID, Events: offer})
			if err != nil {
				return rep, fmt.Errorf("sync push: %w", err)
			}
			rep.Offered += len(offer)
			rep.Accepted += len(res.Accepted)
			rep.Rejected = append(rep.Rejected, res.Rejected...)
			rep.Head = res.Head
		}
		last := page[len(page)-1].Seq
		rep.Scanned += last - st.PushCursor
		st.PushCursor = last
		st.PushedAt = s.Clock.Now()
		if len(page) < batch {
			return rep, nil
		}
	}
}

// Pull copies the team layer into the cache and the projections, skipping
// this daemon's own records (they are already in the personal ledger).
func (s *Syncer) Pull(ctx context.Context, st *SyncState) (PullReport, error) {
	var rep PullReport
	for {
		res, err := s.Client.Pull(ctx, st.PullCursor, 0)
		if err != nil {
			return rep, fmt.Errorf("sync pull: %w", err)
		}
		for _, e := range res.Events {
			rep.Received++
			if e.Origin != nil && e.Origin.DaemonID == s.DaemonID {
				rep.Skipped++
				continue
			}
			ev, err := s.Codec.Decode(e.Kind, e.V, e.Body)
			if err != nil {
				return rep, fmt.Errorf("sync pull: seq %d: %w", e.Seq, err)
			}
			c := e
			c.Seq = 0
			if c.Origin == nil {
				c.Origin = &core.Origin{DaemonID: "server", LocalSeq: e.Seq}
			}
			rng, err := s.Cache.Append(ctx, e.Stream, port.AnyVersion, []core.Envelope{c})
			if err != nil {
				return rep, fmt.Errorf("sync pull: cache seq %d: %w", e.Seq, err)
			}
			rep.Cached++
			written, err := s.Cache.Read(ctx, e.Stream, rng.FromVer)
			if err != nil || len(written) == 0 {
				continue
			}
			for _, p := range s.Projectors {
				if err := p.Handle(ctx, written[0], ev); err != nil {
					return rep, fmt.Errorf("sync pull: project seq %d: %w", e.Seq, err)
				}
			}
		}
		st.PullCursor = res.Cursor
		st.PulledAt = s.Clock.Now()
		rep.Cursor = res.Cursor
		if !res.More {
			return rep, nil
		}
	}
}
