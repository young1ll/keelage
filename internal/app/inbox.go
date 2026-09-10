package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/port"
)

// InboxItem is one row of `keelage inbox` (patterns §13.4: kind · scope ·
// anchor · state · cause). The inbox is the home: what needs a human.
type InboxItem struct {
	Kind   string    `json:"kind"` // change | anchor | constraint | session
	ID     string    `json:"id"`
	Scope  string    `json:"scope,omitempty"`
	Anchor string    `json:"anchor,omitempty"`
	State  string    `json:"state"`
	Cause  string    `json:"cause"`
	At     time.Time `json:"at"`
}

// Inbox lists everything waiting on a person, oldest first.
func Inbox(changes *ChangeIndex, anchors *AnchorIndex, constraints *ConstraintIndex, sessions *SessionIndex) []InboxItem {
	var items []InboxItem
	for _, c := range changes.All() {
		if c.State == accountability.StateSettled || len(c.Constraints) == 0 {
			continue
		}
		cause := fmt.Sprintf("references %d constraint(s)", len(c.Constraints))
		if c.State == accountability.StateProposed {
			cause += "; needs a judgment"
		}
		anchor := ""
		if len(c.Anchors) > 0 {
			anchor = c.Anchors[0]
			if len(c.Anchors) > 1 {
				anchor += fmt.Sprintf(" +%d", len(c.Anchors)-1)
			}
		}
		items = append(items, InboxItem{Kind: "change", ID: string(c.ID), Scope: c.Scope.String(), Anchor: anchor, State: string(c.State), Cause: cause, At: c.UpdatedAt})
	}
	for _, a := range anchors.All() {
		switch a.State {
		case realization.StateStale, realization.StateReview, realization.StateUnrealized:
			cause := "signature changed since verification"
			if a.State == realization.StateReview {
				cause = "body changed since verification"
			}
			if a.State == realization.StateUnrealized {
				cause = "no longer in the working tree"
				if a.MovedTo != "" {
					cause += "; moved to " + a.MovedTo
				}
			}
			items = append(items, InboxItem{Kind: "anchor", ID: a.Key, Anchor: a.Key, State: string(a.State), Cause: cause, At: a.UpdatedAt})
		}
	}
	for _, c := range constraints.All() {
		switch c.State {
		case harness.StateGenerated:
			items = append(items, InboxItem{Kind: "constraint", ID: string(c.ID), Scope: c.Scope.String(), Anchor: strings.Join(c.Anchors, ","), State: string(c.State), Cause: "drafted; not yet verified", At: time.Time{}})
		case harness.StateReview:
			items = append(items, InboxItem{Kind: "constraint", ID: string(c.ID), Scope: c.Scope.String(), Anchor: strings.Join(c.Anchors, ","), State: string(c.State), Cause: "under review", At: time.Time{}})
		}
	}
	if sessions != nil {
		for _, s := range sessions.All() {
			if len(s.Judgments) == 0 || s.State == accountability.SessionStateShared {
				continue
			}
			files := map[string]bool{}
			for _, j := range s.Judgments {
				for _, f := range j.Files {
					files[f] = true
				}
			}
			items = append(items, InboxItem{Kind: "session", ID: s.Stream(), Scope: s.Repo, Anchor: joinKeys(files), State: string(s.State), Cause: fmt.Sprintf("%d judgment candidate(s) from %d turns", len(s.Judgments), s.Turns), At: s.EndedAt})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func joinKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// HistoryEntry is one ledger record that concerns an anchor.
type HistoryEntry struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Stream  string    `json:"stream"`
	Actor   string    `json:"actor"`
	Summary string    `json:"summary"`
}

// History scans the ledger for records on the anchor's own stream or that
// reference it (constraints bound to it, changes that touched it).
func History(ctx context.Context, ledger port.Ledger, codec *core.Codec, anchor string) ([]HistoryEntry, error) {
	a, err := core.ParseAnchor(anchor)
	if err != nil {
		return nil, err
	}
	key := a.String()
	stream := realization.AnchorStream(key)
	var out []HistoryEntry
	var seq int64
	for {
		batch, err := ledger.ReadAll(ctx, seq+1, 500)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return out, nil
		}
		for _, e := range batch {
			seq = e.Seq
			if e.Stream != stream && !contains(e.Meta.Refs, key) {
				ev, err := codec.Decode(e.Kind, e.V, e.Body)
				if err != nil || !mentions(ev, key) {
					continue
				}
				out = append(out, entry(e, ev))
				continue
			}
			ev, err := codec.Decode(e.Kind, e.V, e.Body)
			if err != nil {
				return nil, err
			}
			out = append(out, entry(e, ev))
		}
		if len(batch) < 500 {
			return out, nil
		}
	}
}

func mentions(ev core.Event, key string) bool {
	switch v := ev.(type) {
	case harness.ConstraintDrafted:
		return contains(v.Anchors, key)
	case harness.ConstraintRebound:
		return contains(v.Anchors, key)
	case accountability.ChangeOpened:
		return contains(v.Impact.Anchors, key)
	case accountability.GateOpened:
		return contains(v.Anchors, key)
	case accountability.GateDecided:
		return contains(v.Anchors, key)
	}
	return false
}

func entry(e core.Envelope, ev core.Event) HistoryEntry {
	h := HistoryEntry{Seq: e.Seq, At: e.TS, Kind: e.Kind, Stream: e.Stream, Actor: string(e.Actor.ID)}
	switch v := ev.(type) {
	case realization.AnchorRecorded:
		h.Summary = fmt.Sprintf("baseline sig=%.12s at %.8s", v.Hash.Signature, v.Ref)
	case realization.AnchorVerified:
		h.Summary = fmt.Sprintf("verified (%s) at %.8s", v.Change, v.Ref)
	case realization.AnchorStaled:
		h.Summary = fmt.Sprintf("%s: %s changed at %.8s", v.State, v.Cause, v.Ref)
	case realization.AnchorMoved:
		h.Summary = "moved to " + v.To
	case harness.ConstraintDrafted:
		h.Summary = fmt.Sprintf("constraint %s drafted: %s", v.ID, v.Body)
	case harness.ConstraintRebound:
		h.Summary = fmt.Sprintf("constraint %s rebound", v.ID)
	case accountability.ChangeOpened:
		h.Summary = fmt.Sprintf("change %s opened (%s, %d constraint(s))", v.ID, v.Mode, len(v.Impact.Constraints))
	case accountability.GateOpened:
		h.Summary = fmt.Sprintf("gate %s asked (%s)", v.ID, v.PolicyRef)
	case accountability.GateDecided:
		h.Summary = fmt.Sprintf("gate %s %s (%s)", v.ID, v.Decision, v.PolicyRef)
	default:
		h.Summary = e.Kind
	}
	return h
}
