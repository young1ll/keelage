package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/port"
)

// Shareable reports whether a stream may leave the daemon: the harness and
// accountability objects, never a session transcript. Sessions are pushed
// only once the person shared them (see Syncer).
func Shareable(stream string) bool {
	for _, p := range []string{"constraint/", "decision/", "scope/", "anchor/", "change/", "gate/"} {
		if strings.HasPrefix(stream, p) {
			return true
		}
	}
	return false
}

// TeamLayer reports whether a stream is part of the pull copy every daemon
// receives: constraints, decisions, autonomy scopes and anchor history.
func TeamLayer(stream string) bool {
	for _, p := range []string{"constraint/", "decision/", "scope/", "anchor/"} {
		if strings.HasPrefix(stream, p) {
			return true
		}
	}
	return false
}

// GateIndex projects gate events into current state (server side).
type GateIndex struct {
	mu      sync.RWMutex
	entries map[core.ID]accountability.Gate
}

// NewGateIndex returns an empty index.
func NewGateIndex() *GateIndex { return &GateIndex{entries: map[core.ID]accountability.Gate{}} }

// Name implements port.Projector.
func (g *GateIndex) Name() string { return "gate_current" }

// Handle implements port.Projector.
func (g *GateIndex) Handle(_ context.Context, e core.Envelope, ev core.Event) error {
	if !strings.HasPrefix(e.Stream, "gate/") {
		return nil
	}
	id := core.ID(strings.TrimPrefix(e.Stream, "gate/"))
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries[id] = g.entries[id].Apply(ev)
	return nil
}

// Get returns one gate.
func (g *GateIndex) Get(id core.ID) (accountability.Gate, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, ok := g.entries[id]
	return v, ok
}

// List returns gates, optionally filtered by state, newest first.
func (g *GateIndex) List(state accountability.GateState) []accountability.Gate {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]accountability.Gate, 0, len(g.entries))
	for _, v := range g.entries {
		if state == "" || v.State == state {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenedAt.After(out[j].OpenedAt) })
	return out
}

// Org is one tenant's in-process assembly on the server: its ledger, the
// projections rebuilt from it and the command pipeline. Records arrive
// through the pipeline (gates) and through sync (proposals from daemons).
type Org struct {
	ID          string
	Ledger      port.OriginLedger
	Codec       *core.Codec
	Scopes      *ScopeIndex
	Constraints *ConstraintIndex
	Changes     *ChangeIndex
	Anchors     *AnchorIndex
	Decisions   *DecisionIndex
	Gates       *GateIndex
	Pipeline    *Pipeline

	mu     sync.Mutex
	cursor *Cursor
}

func (o *Org) projectors() []port.Projector {
	return []port.Projector{o.Scopes, o.Constraints, o.Changes, o.Anchors, o.Decisions, o.Gates, o.cursor}
}

// catchUp projects records appended outside the pipeline (sync). The
// cursor projector moves with pipeline writes too, so nothing is applied
// twice.
func (o *Org) catchUp(ctx context.Context) error {
	head, err := o.Ledger.Head(ctx)
	if err != nil {
		return err
	}
	if head.Seq <= o.cursor.Seq() {
		return nil
	}
	_, err = Replay(ctx, o.Ledger, o.Codec, o.cursor.Seq(), o.projectors()...)
	return err
}

// Orgs opens tenants lazily and keeps them for the process lifetime.
type Orgs struct {
	Open   func(ctx context.Context, org string) (port.OriginLedger, error)
	Codec  *core.Codec
	Clock  port.Clock
	Policy core.Policy

	mu   sync.Mutex
	orgs map[string]*Org
}

// Get returns the tenant's runtime, building it on first use.
func (r *Orgs) Get(ctx context.Context, id string) (*Org, error) {
	if id == "" {
		return nil, errors.New("app: org required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.orgs == nil {
		r.orgs = map[string]*Org{}
	}
	if o, ok := r.orgs[id]; ok {
		return o, nil
	}
	ledger, err := r.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	o := &Org{
		ID: id, Ledger: ledger, Codec: r.Codec,
		Scopes: NewScopeIndex(), Constraints: NewConstraintIndex(), Changes: NewChangeIndex(), Anchors: NewAnchorIndex(),
		Decisions: NewDecisionIndex(), Gates: NewGateIndex(), cursor: &Cursor{},
	}
	if _, err = Rebuild(ctx, ledger, r.Codec, o.projectors()...); err != nil {
		return nil, err
	}
	o.Pipeline = New(Options{
		Ledger: ledger, Codec: r.Codec, Clock: r.Clock, Policy: r.Policy,
		Context: ProjectionContext{Scopes: o.Scopes, Constraints: o.Constraints}, Projectors: o.projectors(),
	})
	RegisterAll(o.Pipeline)
	r.orgs[id] = o
	return o, nil
}

// TeamServer implements port.TeamService: the sync receiver and the
// approval broker over per-org runtimes.
type TeamServer struct {
	Orgs    *Orgs
	Daemons port.DaemonRegistry
	Keys    port.SignatureVerifier
	Clock   port.Clock
	// PullLimit caps one pull page (default 500).
	PullLimit int
}

// RegisterDaemon binds a daemon id to its public key under the caller.
func (t *TeamServer) RegisterDaemon(ctx context.Context, p port.Principal, id, publicKey string) error {
	if id == "" || publicKey == "" {
		return errors.New("app: daemon id and public key required")
	}
	if !t.Keys.ValidKey(publicKey) {
		return errors.New("app: public key does not parse")
	}
	return t.Daemons.RegisterDaemon(ctx, p.Org, id, publicKey, p.User)
}

// Push implements the sync receiver (architecture-patterns-v0 §9).
func (t *TeamServer) Push(ctx context.Context, p port.Principal, req port.PushRequest) (port.PushResponse, error) {
	org, err := t.Orgs.Get(ctx, p.Org)
	if err != nil {
		return port.PushResponse{}, err
	}
	pub, user, err := t.Daemons.DaemonKey(ctx, p.Org, req.DaemonID)
	if errors.Is(err, port.ErrNotFound) {
		return port.PushResponse{}, fmt.Errorf("%w: daemon %q is not registered", port.ErrNotFound, req.DaemonID)
	}
	if err != nil {
		return port.PushResponse{}, err
	}
	if user != p.User {
		return port.PushResponse{}, fmt.Errorf("%w: daemon %q belongs to another user", port.ErrForbidden, req.DaemonID)
	}
	evs := append([]core.Envelope(nil), req.Events...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Seq < evs[j].Seq })

	org.mu.Lock()
	defer org.mu.Unlock()
	res := port.PushResponse{Accepted: []port.PushAccepted{}, Rejected: []port.PushRejected{}}
	now := t.Clock.Now()
	reject := func(e core.Envelope, code, reason string, record bool) error {
		res.Rejected = append(res.Rejected, port.PushRejected{LocalSeq: e.Seq, Code: code, Reason: reason})
		if !record {
			return nil
		}
		return appendRejected(ctx, org.Ledger, org.Codec, org.projectors(), now, core.Rejected{
			Command: "sync:" + e.Kind, Target: e.Stream, Code: code, Reason: reason, Actor: e.Actor,
		})
	}
	for _, e := range evs {
		if seq, err := org.Ledger.FindOrigin(ctx, req.DaemonID, e.Seq); err == nil {
			res.Accepted = append(res.Accepted, port.PushAccepted{LocalSeq: e.Seq, Seq: seq, Duplicate: true})
			continue
		} else if !errors.Is(err, port.ErrNotFound) {
			return res, err
		}
		// the daemon's own chain must hold, and the daemon must have signed it
		if err := e.Verify(e.PrevHash); err != nil {
			if err := reject(e, "bad-hash", err.Error(), false); err != nil {
				return res, err
			}
			continue
		}
		if !t.Keys.Verify(pub, e.Hash, e.Sig) {
			if err := reject(e, "bad-signature", "signature does not verify with the daemon key", false); err != nil {
				return res, err
			}
			continue
		}
		var code, reason string
		switch {
		case !Shareable(e.Stream):
			code, reason = "not-shareable", "stream "+e.Stream+" is not shared"
		case e.Actor.IsHuman() && string(e.Actor.ID) != user, e.Actor.IsAgent() && string(e.Actor.Owner) != user:
			code, reason = "unauthorized", "record actor is not the pushing user or an agent they own"
		default:
			if _, err := org.Codec.Decode(e.Kind, e.V, e.Body); err != nil {
				code, reason = "unknown-event", err.Error()
			}
		}
		if code != "" {
			if err := reject(e, code, reason, true); err != nil {
				return res, err
			}
			continue
		}
		copyE := e
		copyE.Seq = 0
		copyE.Origin = &core.Origin{DaemonID: req.DaemonID, LocalSeq: e.Seq, PrevHash: append(core.Hash(nil), e.PrevHash...)}
		rng, err := org.Ledger.Append(ctx, e.Stream, e.Ver-1, []core.Envelope{copyE})
		switch {
		case err == nil:
			res.Accepted = append(res.Accepted, port.PushAccepted{LocalSeq: e.Seq, Seq: rng.FromSeq})
		case errors.Is(err, port.ErrDuplicateOrigin):
			seq, _ := org.Ledger.FindOrigin(ctx, req.DaemonID, e.Seq)
			res.Accepted = append(res.Accepted, port.PushAccepted{LocalSeq: e.Seq, Seq: seq, Duplicate: true})
		case errors.Is(err, port.ErrVersionConflict):
			if err := reject(e, "version-conflict", fmt.Sprintf("stream %s is not at version %d on the server", e.Stream, e.Ver-1), true); err != nil {
				return res, err
			}
		case errors.Is(err, port.ErrDuplicateIdempotencyKey):
			if err := reject(e, "duplicate-idem", "idempotency key already used on this stream", true); err != nil {
				return res, err
			}
		default:
			return res, err
		}
	}
	if err := org.catchUp(ctx); err != nil {
		return res, err
	}
	head, err := org.Ledger.Head(ctx)
	if err != nil {
		return res, err
	}
	res.Head = head.Seq
	return res, nil
}

// Pull returns the team layer after the cursor.
func (t *TeamServer) Pull(ctx context.Context, p port.Principal, since int64, limit int) (port.PullResponse, error) {
	org, err := t.Orgs.Get(ctx, p.Org)
	if err != nil {
		return port.PullResponse{}, err
	}
	max := t.PullLimit
	if max <= 0 {
		max = 500
	}
	if limit <= 0 || limit > max {
		limit = max
	}
	batch, err := org.Ledger.ReadAll(ctx, since+1, limit)
	if err != nil {
		return port.PullResponse{}, err
	}
	res := port.PullResponse{Cursor: since, Events: []core.Envelope{}}
	for _, e := range batch {
		res.Cursor = e.Seq
		if TeamLayer(e.Stream) {
			res.Events = append(res.Events, e)
		}
	}
	res.More = len(batch) == limit
	return res, nil
}

// RequestGate is the broker: an agent asks before acting (spec §3.6a).
func (t *TeamServer) RequestGate(ctx context.Context, p port.Principal, req port.GateRequest) (accountability.Gate, error) {
	org, err := t.Orgs.Get(ctx, p.Org)
	if err != nil {
		return accountability.Gate{}, err
	}
	actor := p.Actor()
	id := accountability.GateIDFor(actor, req.ProposalRef)
	cmd := accountability.RequestGate{
		GateCmd: accountability.NewGateCmd(id, "gate:"+string(id)), Actor: actor, Scope: req.Scope, Anchors: req.Anchors,
		Action: req.Action, ProposalRef: req.ProposalRef, RequestedLevel: req.RequestedLevel,
	}
	org.mu.Lock()
	defer org.mu.Unlock()
	if err := org.catchUp(ctx); err != nil {
		return accountability.Gate{}, err
	}
	if _, err := org.Pipeline.Handle(ctx, actor, cmd); err != nil {
		return accountability.Gate{}, err
	}
	g, ok := org.Gates.Get(id)
	if !ok {
		return accountability.Gate{}, port.ErrNotFound
	}
	return g, nil
}

// ResolveGate records a human's answer to an open gate.
func (t *TeamServer) ResolveGate(ctx context.Context, p port.Principal, id core.ID, res port.GateResolution) (accountability.Gate, error) {
	org, err := t.Orgs.Get(ctx, p.Org)
	if err != nil {
		return accountability.Gate{}, err
	}
	org.mu.Lock()
	defer org.mu.Unlock()
	if err := org.catchUp(ctx); err != nil {
		return accountability.Gate{}, err
	}
	if _, ok := org.Gates.Get(id); !ok {
		return accountability.Gate{}, port.ErrNotFound
	}
	actor := p.Actor()
	cmd := accountability.ResolveGate{
		GateCmd:    accountability.NewGateCmd(id, ""),
		Resolution: accountability.Resolution{Decision: res.Decision, Reason: res.Reason, By: actor, At: t.Clock.Now()},
	}
	if _, err := org.Pipeline.Handle(ctx, actor, cmd); err != nil {
		return accountability.Gate{}, err
	}
	g, _ := org.Gates.Get(id)
	return g, nil
}

// ListGates lists the org's gates.
func (t *TeamServer) ListGates(ctx context.Context, p port.Principal, state accountability.GateState) ([]accountability.Gate, error) {
	org, err := t.Orgs.Get(ctx, p.Org)
	if err != nil {
		return nil, err
	}
	org.mu.Lock()
	defer org.mu.Unlock()
	if err := org.catchUp(ctx); err != nil {
		return nil, err
	}
	return org.Gates.List(state), nil
}

var (
	_ port.TeamService = (*TeamServer)(nil)
	_ port.Projector   = (*GateIndex)(nil)
)
