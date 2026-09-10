package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/adapter/memory"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/port"
)

// ---- test doubles: hex ed25519 keys, an in-memory registry, a direct client ----

type testKey struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newTestKey(t *testing.T) *testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testKey{priv: priv, pub: pub}
}

func (k *testKey) Sign(h []byte) ([]byte, error) { return ed25519.Sign(k.priv, h), nil }
func (k *testKey) Fingerprint() string           { return "fp-" + hex.EncodeToString(k.pub[:4]) }
func (k *testKey) Public() string                { return hex.EncodeToString(k.pub) }

type hexVerifier struct{}

func (hexVerifier) Verify(pub string, h, sig []byte) bool {
	b, err := hex.DecodeString(pub)
	return err == nil && len(b) == ed25519.PublicKeySize && ed25519.Verify(b, h, sig)
}

func (hexVerifier) ValidKey(pub string) bool {
	b, err := hex.DecodeString(pub)
	return err == nil && len(b) == ed25519.PublicKeySize
}

type registry map[string][2]string // org/id → {pub, user}

func (r registry) RegisterDaemon(_ context.Context, org, id, pub, user string) error {
	if v, ok := r[org+"/"+id]; ok && v[0] != pub {
		return port.ErrDaemonKeyMismatch
	}
	r[org+"/"+id] = [2]string{pub, user}
	return nil
}

func (r registry) DaemonKey(_ context.Context, org, id string) (string, string, error) {
	v, ok := r[org+"/"+id]
	if !ok {
		return "", "", port.ErrNotFound
	}
	return v[0], v[1], nil
}

// direct calls the server in-process as a fixed principal.
type direct struct {
	s *TeamServer
	p port.Principal
}

func (d direct) Push(ctx context.Context, req port.PushRequest) (port.PushResponse, error) {
	return d.s.Push(ctx, d.p, req)
}
func (d direct) Pull(ctx context.Context, since int64, limit int) (port.PullResponse, error) {
	return d.s.Pull(ctx, d.p, since, limit)
}
func (d direct) RegisterDaemon(ctx context.Context, id, pub string) error {
	return d.s.RegisterDaemon(ctx, d.p, id, pub)
}
func (d direct) RequestGate(ctx context.Context, req port.GateRequest) (accountability.Gate, error) {
	return d.s.RequestGate(ctx, d.p, req)
}
func (d direct) ResolveGate(ctx context.Context, id core.ID, res port.GateResolution) (accountability.Gate, error) {
	return d.s.ResolveGate(ctx, d.p, id, res)
}
func (d direct) ListGates(ctx context.Context, st accountability.GateState) ([]accountability.Gate, error) {
	return d.s.ListGates(ctx, d.p, st)
}

// daemon is one person's local assembly.
type daemon struct {
	key         *testKey
	ledger      *memory.Ledger
	cache       *memory.Ledger
	constraints *ConstraintIndex
	scopes      *ScopeIndex
	sessions    *SessionIndex
	p           *Pipeline
	state       SyncState
}

func newDaemon(t *testing.T, srv *TeamServer, user string) (*daemon, *Syncer) {
	t.Helper()
	d := &daemon{key: newTestKey(t), cache: memory.NewLedger(nil), constraints: NewConstraintIndex(), scopes: NewScopeIndex(), sessions: NewSessionIndex()}
	d.ledger = memory.NewLedger(d.key)
	projectors := []port.Projector{d.scopes, d.constraints, d.sessions}
	d.p = New(Options{Ledger: d.ledger, Codec: NewCodec(), Clock: memory.NewClock(t0), Context: ProjectionContext{Scopes: d.scopes, Constraints: d.constraints}, Projectors: projectors})
	RegisterAll(d.p)
	client := direct{s: srv, p: port.Principal{Org: "acme", User: user, Kind: "human", Owner: user}}
	if err := client.RegisterDaemon(context.Background(), d.key.Fingerprint(), d.key.Public()); err != nil {
		t.Fatal(err)
	}
	d.state = SyncState{DaemonID: d.key.Fingerprint()}
	return d, &Syncer{
		Local: d.ledger, Cache: d.cache, Codec: NewCodec(), Client: client, DaemonID: d.key.Fingerprint(),
		Constraints: d.constraints, Sessions: d.sessions, Projectors: projectors, Clock: memory.NewClock(t0), Batch: 2,
	}
}

func newServer(t *testing.T) (*TeamServer, *memory.Ledger) {
	t.Helper()
	ledger := memory.NewLedger(nil)
	srv := &TeamServer{
		Orgs: &Orgs{
			Open:  func(_ context.Context, org string) (port.OriginLedger, error) { return ledger, nil },
			Codec: NewCodec(), Clock: memory.NewClock(t0),
		},
		Daemons: registry{}, Keys: hexVerifier{}, Clock: memory.NewClock(t0),
	}
	return srv, ledger
}

func serverRejections(t *testing.T, ledger port.Ledger) []core.Rejected {
	t.Helper()
	envs, _ := ledger.Read(context.Background(), core.RejectedStream, 0)
	codec := NewCodec()
	out := []core.Rejected{}
	for _, e := range envs {
		ev, err := codec.Decode(e.Kind, e.V, e.Body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev.(core.Rejected))
	}
	return out
}

func TestSync_PushPullBetweenDaemons(t *testing.T) {
	ctx := context.Background()
	srv, sledger := newServer(t)
	a, syncA := newDaemon(t, srv, "alice")
	b, syncB := newDaemon(t, srv, "bob")
	aliceA := core.ActorRef{Kind: core.ActorHuman, ID: "alice"}

	// alice: a team constraint (shared), a personal one (stays local), a verify
	if _, err := a.p.Handle(ctx, aliceA, harness.DraftConstraint{ID: "c1", ConstraintKind: harness.KindRule, Scope: team, Body: "no retries", Authored: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.p.Handle(ctx, aliceA, harness.DraftConstraint{ID: "p1", ConstraintKind: harness.KindRule, Scope: core.ScopeKey{Person: "alice"}, Body: "my habit", Authored: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.p.Handle(ctx, aliceA, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1"}}); err != nil {
		t.Fatal(err)
	}
	rep, err := syncA.Push(ctx, &a.state)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 3 || rep.Offered != 2 || rep.Accepted != 2 || len(rep.Rejected) != 0 || rep.Head != 2 || a.state.PushCursor != 3 {
		t.Fatalf("push report %+v state %+v", rep, a.state)
	}
	// the server copy keeps alice's daemon signature, verifiable via origin
	got, _ := sledger.Read(ctx, harness.ConstraintStream("c1"), 0)
	if len(got) != 2 || got[0].Origin == nil || got[0].Origin.DaemonID != a.key.Fingerprint() || got[0].Origin.LocalSeq != 1 {
		t.Fatalf("server copy: %+v", got)
	}
	if !ed25519.Verify(a.key.pub, got[1].OriginHash(), got[1].Sig) {
		t.Fatal("origin signature must verify from the server copy")
	}
	if got[1].Hash.Equal(got[1].OriginHash()) {
		t.Fatal("the server re-chains: server hash differs from the daemon hash")
	}
	// pushing again offers nothing new; re-offering the same records is a duplicate, not a copy
	if rep, _ := syncA.Push(ctx, &a.state); rep.Scanned != 0 || rep.Offered != 0 {
		t.Fatalf("second push: %+v", rep)
	}
	a.state.PushCursor = 0
	if rep, _ := syncA.Push(ctx, &a.state); rep.Accepted != 2 {
		t.Fatalf("replayed push: %+v", rep)
	}
	if h, _ := sledger.Head(ctx); h.Seq != 2 {
		t.Fatalf("duplicates were appended: head %d", h.Seq)
	}

	// bob pulls alice's constraint into his cache and projections
	prep, err := syncB.Pull(ctx, &b.state)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Received != 2 || prep.Cached != 2 || prep.Skipped != 0 || b.state.PullCursor != 2 {
		t.Fatalf("pull report %+v state %+v", prep, b.state)
	}
	if c, ok := b.constraints.Get("c1"); !ok || c.State != harness.StateVerified {
		t.Fatalf("bob's projection: %+v %v", c, ok)
	}
	if _, ok := b.constraints.Get("p1"); ok {
		t.Fatal("personal constraint must not travel")
	}
	// alice pulling skips her own records
	if prep, _ := syncA.Pull(ctx, &a.state); prep.Skipped != 2 || prep.Cached != 0 {
		t.Fatalf("alice pull: %+v", prep)
	}
	// bob re-pulling from zero (cursor reset) re-caches nothing
	b.state.PullCursor = 0
	if prep, err := syncB.Pull(ctx, &b.state); err != nil || prep.Cached != 0 || prep.Skipped != 2 {
		t.Fatalf("re-pull: %+v %v", prep, err)
	}
	if h, _ := b.cache.Head(ctx); h.Seq != 2 {
		t.Fatalf("cache grew on re-pull: %d", h.Seq)
	}

	// bob drafts a new constraint and, locally, the same stream as alice's:
	// the new one is accepted, the clash is a version conflict → Rejected on
	// the server, logged for bob. The accepted record must still reach the
	// server's projections even though the rejection moved the cursor.
	bobB := core.ActorRef{Kind: core.ActorHuman, ID: "bob"}
	if _, err := b.p.Handle(ctx, bobB, harness.DraftConstraint{ID: "c2", ConstraintKind: harness.KindRule, Scope: team, Body: "bob's rule", Authored: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.p.Handle(ctx, bobB, harness.DraftConstraint{ID: "c1", ConstraintKind: harness.KindRule, Scope: team, Body: "mine", Authored: true}); err != nil {
		t.Fatal(err)
	}
	rep, err = syncB.Push(ctx, &b.state)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Accepted != 1 || len(rep.Rejected) != 1 || rep.Rejected[0].Code != "version-conflict" {
		t.Fatalf("conflict push: %+v", rep)
	}
	sorg, _ := srv.Orgs.Get(ctx, "acme")
	if _, ok := sorg.Constraints.Get("c2"); !ok {
		t.Fatal("accepted record skipped by the server projections")
	}
	rej := serverRejections(t, sledger)
	if len(rej) != 1 || rej[0].Code != "version-conflict" || rej[0].Target != "constraint/c1" || rej[0].Actor.ID != "bob" {
		t.Fatalf("server rejections: %+v", rej)
	}
	// the cursor moved past the rejected record: it is not retried
	if rep, _ := syncB.Push(ctx, &b.state); rep.Offered != 0 {
		t.Fatalf("rejected record retried: %+v", rep)
	}
}

func TestSync_PushRefusesForgedOrForeignRecords(t *testing.T) {
	ctx := context.Background()
	srv, sledger := newServer(t)
	a, _ := newDaemon(t, srv, "alice")
	client := direct{s: srv, p: port.Principal{Org: "acme", User: "alice", Kind: "human", Owner: "alice"}}
	aliceA := core.ActorRef{Kind: core.ActorHuman, ID: "alice"}
	if _, err := a.p.Handle(ctx, aliceA, harness.DraftConstraint{ID: "c1", ConstraintKind: harness.KindRule, Scope: team, Body: "x", Authored: true}); err != nil {
		t.Fatal(err)
	}
	envs, _ := a.ledger.ReadAll(ctx, 1, 0)
	good := envs[0]

	tampered := good
	tampered.Body = append([]byte(nil), good.Body...)
	tampered.Body[len(tampered.Body)-2] = 'X'
	res, err := client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: []core.Envelope{tampered}})
	if err != nil || len(res.Rejected) != 1 || res.Rejected[0].Code != "bad-hash" {
		t.Fatalf("tampered body: %+v %v", res, err)
	}
	forged := good
	forged.Sig = append([]byte(nil), good.Sig...)
	forged.Sig[0] ^= 1
	res, _ = client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: []core.Envelope{forged}})
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "bad-signature" {
		t.Fatalf("forged signature: %+v", res)
	}
	if h, _ := sledger.Head(ctx); h.Seq != 0 {
		t.Fatal("authentication failures are not ledger records")
	}
	// an unknown daemon, and a daemon that belongs to someone else
	if _, err := client.Push(ctx, port.PushRequest{DaemonID: "nope", Events: []core.Envelope{good}}); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("unknown daemon: %v", err)
	}
	bob := direct{s: srv, p: port.Principal{Org: "acme", User: "bob", Kind: "human", Owner: "bob"}}
	if _, err := bob.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: []core.Envelope{good}}); !errors.Is(err, port.ErrForbidden) {
		t.Fatalf("foreign daemon: %v", err)
	}
	// a record signed by alice's daemon but claiming bob acted: refused and recorded
	foreign := memory.NewLedger(a.key)
	e := good
	e.Actor = core.ActorRef{Kind: core.ActorHuman, ID: "bob"}
	e.Seq, e.Ver, e.Sig = 0, 0, nil
	if _, err := foreign.Append(ctx, good.Stream, port.AnyVersion, []core.Envelope{e}); err != nil {
		t.Fatal(err)
	}
	fe, _ := foreign.ReadAll(ctx, 1, 0)
	res, _ = client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: fe})
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "unauthorized" {
		t.Fatalf("foreign actor: %+v", res)
	}
	// the Rejected stream never travels; a malformed actor is refused too
	sess := memory.NewLedger(a.key)
	s := good
	s.Seq, s.Ver, s.Sig = 0, 0, nil
	if _, err := sess.Append(ctx, core.RejectedStream, port.AnyVersion, []core.Envelope{s}); err != nil {
		t.Fatal(err)
	}
	se, _ := sess.ReadAll(ctx, 1, 0)
	res, _ = client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: se})
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "not-shareable" {
		t.Fatalf("rejected stream: %+v", res)
	}
	bad := memory.NewLedger(a.key)
	m := good
	m.Actor = core.ActorRef{Kind: "robot", ID: "x"}
	m.Seq, m.Ver, m.Sig = 0, 0, nil
	if _, err := bad.Append(ctx, "constraint/c9", port.AnyVersion, []core.Envelope{m}); err != nil {
		t.Fatal(err)
	}
	be, _ := bad.ReadAll(ctx, 1, 0)
	res, _ = client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: be})
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "unauthorized" {
		t.Fatalf("malformed actor: %+v", res)
	}
	// ver 0 would disable the continuity check: refused without a record
	z := good
	z.Ver = 0
	res, _ = client.Push(ctx, port.PushRequest{DaemonID: a.key.Fingerprint(), Events: []core.Envelope{z}})
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "bad-hash" {
		t.Fatalf("ver 0: %+v", res)
	}
	rej := serverRejections(t, sledger)
	if len(rej) != 3 || rej[0].Code != "unauthorized" || rej[1].Code != "not-shareable" || rej[2].Code != "unauthorized" {
		t.Fatalf("recorded rejections: %+v", rej)
	}
	// the key cannot be re-bound
	if err := client.RegisterDaemon(ctx, a.key.Fingerprint(), newTestKey(t).Public()); !errors.Is(err, port.ErrDaemonKeyMismatch) {
		t.Fatalf("rebind: %v", err)
	}
	if err := client.RegisterDaemon(ctx, "d2", "not-a-key"); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestGates_AskAndResolveOnTheServer(t *testing.T) {
	ctx := context.Background()
	srv, sledger := newServer(t)
	agentP := port.Principal{Org: "acme", User: "cc-1", Kind: "agent", Owner: "alice"}
	human := port.Principal{Org: "acme", User: "alice", Kind: "human", Owner: "alice"}
	req := port.GateRequest{Scope: team, ProposalRef: "p1", RequestedLevel: core.L1, Action: "edit billing"}

	g, err := srv.RequestGate(ctx, agentP, req)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != accountability.GateStateOpen || g.Decision != accountability.GateAsk || g.Actor.ID != "agent:cc-1" || g.Actor.Owner != "alice" {
		t.Fatalf("gate: %+v", g)
	}
	// idempotent per (actor, proposal)
	if again, _ := srv.RequestGate(ctx, agentP, req); again.ID != g.ID {
		t.Fatalf("second ask made a new gate: %s vs %s", again.ID, g.ID)
	}
	// a human cannot ask; an agent cannot resolve
	if _, err := srv.RequestGate(ctx, human, req); err == nil {
		t.Fatal("human ask accepted")
	}
	if _, err := srv.ResolveGate(ctx, agentP, g.ID, port.GateResolution{Decision: accountability.GateAllow, Reason: "self"}); err == nil {
		t.Fatal("agent resolve accepted")
	}
	if _, err := srv.ResolveGate(ctx, human, "gate-missing", port.GateResolution{Decision: accountability.GateAllow, Reason: "x"}); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("missing gate: %v", err)
	}
	open, _ := srv.ListGates(ctx, human, accountability.GateStateOpen)
	if len(open) != 1 {
		t.Fatalf("open gates: %d", len(open))
	}
	r, err := srv.ResolveGate(ctx, human, g.ID, port.GateResolution{Decision: accountability.GateAllow, Reason: "reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != accountability.GateStateResolved || r.Decision != accountability.GateAllow || r.Resolution.By.ID != "alice" {
		t.Fatalf("resolved: %+v", r)
	}
	if open, _ := srv.ListGates(ctx, human, accountability.GateStateOpen); len(open) != 0 {
		t.Fatal("still open")
	}
	// both the question and the answer are ledger records
	envs, _ := sledger.Read(ctx, accountability.GateStream(g.ID), 0)
	if len(envs) != 2 || envs[0].Kind != "GateOpened" || envs[1].Kind != "GateResolved" {
		t.Fatalf("gate stream: %+v", envs)
	}
	// and they are part of nobody's pull (gates are not the team layer)
	pull, _ := srv.Pull(ctx, human, 0, 0)
	if len(pull.Events) != 0 || pull.Cursor != 4 { // 2 gate records + 2 Rejected, none of them team layer
		t.Fatalf("pull: %+v", pull)
	}
}

type fakeOAuth struct {
	polls int
	login string
}

func (f *fakeOAuth) Start(context.Context) (port.DeviceStart, error) {
	return port.DeviceStart{DeviceCode: "dc", UserCode: "ABCD-EFGH", VerificationURI: "https://github.com/login/device", ExpiresIn: 900, Interval: 5}, nil
}

func (f *fakeOAuth) Poll(_ context.Context, code string) (string, string, int, error) {
	if code != "dc" {
		return "", "", 0, errors.New("bad code")
	}
	f.polls++
	if f.polls == 1 {
		return "pending", "", 5, nil
	}
	return "ok", f.login, 0, nil
}

type members map[string]bool

func (m members) IsMember(_ context.Context, org, user string) (bool, error) {
	return m[org+"/"+user], nil
}

type fakeIssuer struct {
	n          int
	user, kind string
}

func (f *fakeIssuer) IssueToken(_ context.Context, _, user, kind, _, _ string, _ time.Duration) (string, error) {
	f.n++
	f.user, f.kind = user, kind
	return "kl_" + strconv.Itoa(f.n), nil
}

func TestLogin_DeviceFlowIssuesTokensToMembers(t *testing.T) {
	ctx := context.Background()
	issuer := &fakeIssuer{}
	l := &Login{OAuth: &fakeOAuth{login: "alice"}, Members: members{"acme/alice": true}, Tokens: issuer}
	start, err := l.Start(ctx)
	if err != nil || start.UserCode != "ABCD-EFGH" {
		t.Fatalf("start: %+v %v", start, err)
	}
	p, err := l.Poll(ctx, "acme", start.DeviceCode)
	if err != nil || p.Status != "pending" {
		t.Fatalf("first poll: %+v %v", p, err)
	}
	p, err = l.Poll(ctx, "acme", start.DeviceCode)
	if err != nil || p.Status != "ok" || p.Token != "kl_1" || p.User != "alice" || p.Org != "acme" {
		t.Fatalf("second poll: %+v %v", p, err)
	}
	if issuer.kind != "human" || issuer.user != "alice" {
		t.Fatalf("issued: %+v", issuer)
	}
	// not a member of that org
	l2 := &Login{OAuth: &fakeOAuth{login: "mallory", polls: 1}, Members: members{"acme/alice": true}, Tokens: issuer}
	if _, err := l2.Poll(ctx, "acme", "dc"); !errors.Is(err, ErrNotMember) || !errors.Is(err, port.ErrForbidden) {
		t.Fatalf("non-member: %v", err)
	}
	// mixed-case provider logins map to the lower-case member id
	l3 := &Login{OAuth: &fakeOAuth{login: "Alice", polls: 1}, Members: members{"acme/alice": true}, Tokens: issuer}
	if p, err := l3.Poll(ctx, "acme", "dc"); err != nil || p.User != "alice" {
		t.Fatalf("case: %+v %v", p, err)
	}
}
