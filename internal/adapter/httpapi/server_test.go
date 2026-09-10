package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

type stubAuth struct{}

func (stubAuth) Authenticate(_ context.Context, tok string) (port.Principal, error) {
	if tok == "kl_alice" {
		return port.Principal{Org: "acme", User: "alice", Kind: "human", Owner: "alice"}, nil
	}
	return port.Principal{}, port.ErrUnauthenticated
}

type stubTeam struct {
	pushedBy port.Principal
	pushed   port.PushRequest
}

func (s *stubTeam) Push(_ context.Context, p port.Principal, req port.PushRequest) (port.PushResponse, error) {
	s.pushedBy, s.pushed = p, req
	return port.PushResponse{Accepted: []port.PushAccepted{{LocalSeq: 1, Seq: 9}}, Rejected: []port.PushRejected{}, Head: 9}, nil
}

func (s *stubTeam) Pull(_ context.Context, _ port.Principal, since int64, _ int) (port.PullResponse, error) {
	return port.PullResponse{Cursor: since + 1, Events: []core.Envelope{}}, nil
}

func (s *stubTeam) RegisterDaemon(_ context.Context, _ port.Principal, id, _ string) error {
	if id == "taken" {
		return port.ErrDaemonKeyMismatch
	}
	return nil
}

func (s *stubTeam) RequestGate(_ context.Context, p port.Principal, _ port.GateRequest) (accountability.Gate, error) {
	if p.Kind == "human" {
		return accountability.Gate{}, core.Reject("agent-only", "gates are asked by agents")
	}
	return accountability.Gate{ID: "g1", State: accountability.GateStateOpen}, nil
}

func (s *stubTeam) ResolveGate(_ context.Context, _ port.Principal, id core.ID, _ port.GateResolution) (accountability.Gate, error) {
	if id != "g1" {
		return accountability.Gate{}, port.ErrNotFound
	}
	return accountability.Gate{ID: id, State: accountability.GateStateResolved}, nil
}

func (s *stubTeam) ListGates(context.Context, port.Principal, accountability.GateState) ([]accountability.Gate, error) {
	return []accountability.Gate{}, nil
}

type stubProofs struct{}

func (stubProofs) Checkpoint(_ context.Context, org string) (merkle.Checkpoint, error) {
	return merkle.Checkpoint{Org: org, TreeSize: 3}, nil
}

func (stubProofs) Inclusion(_ context.Context, org string, seq int64) (merkle.Bundle, error) {
	if seq > 3 {
		return merkle.Bundle{}, errors.New("past the head")
	}
	return merkle.Bundle{Org: org, Seq: seq, TreeSize: 3}, nil
}

func (stubProofs) Consistency(_ context.Context, org string, from, to uint64) (merkle.ConsistencyBundle, error) {
	return merkle.ConsistencyBundle{Org: org, From: merkle.Checkpoint{TreeSize: from}, To: merkle.Checkpoint{TreeSize: to}}, nil
}

func call(t *testing.T, h http.Handler, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestMountServer_AuthAndRouting(t *testing.T) {
	team := &stubTeam{}
	r := New("keelage-server", "test")
	MountServer(r, &Server{Auth: stubAuth{}, Team: team, Proofs: stubProofs{}, PublicKey: "ed25519:AAA"})

	// public: health and the server key
	if code, body := call(t, r, http.MethodGet, "/ledger/key", "", nil); code != 200 || !bytes.Contains(body, []byte("ed25519:AAA")) {
		t.Fatalf("key: %d %s", code, body)
	}
	// everything else needs a token
	for _, p := range []string{"/sync/pull", "/gates", "/ledger/checkpoint", "/me"} {
		if code, _ := call(t, r, http.MethodGet, p, "", nil); code != 401 {
			t.Fatalf("%s without token: %d", p, code)
		}
		if code, _ := call(t, r, http.MethodGet, p, "kl_bad", nil); code != 401 {
			t.Fatalf("%s bad token: %d", p, code)
		}
	}
	code, body := call(t, r, http.MethodGet, "/me", "kl_alice", nil)
	if code != 200 || !bytes.Contains(body, []byte(`"User":"alice"`)) {
		t.Fatalf("me: %d %s", code, body)
	}
	// push carries the principal
	code, body = call(t, r, http.MethodPost, "/sync/push", "kl_alice", port.PushRequest{DaemonID: "d1", Events: []core.Envelope{{Seq: 1, Stream: "constraint/c1"}}})
	if code != 200 || team.pushedBy.User != "alice" || team.pushed.DaemonID != "d1" || !bytes.Contains(body, []byte(`"seq":9`)) {
		t.Fatalf("push: %d %s (%+v)", code, body, team.pushedBy)
	}
	if code, body := call(t, r, http.MethodPost, "/sync/push", "kl_alice", "not json"); code != 400 {
		t.Fatalf("bad body: %d %s", code, body)
	}
	code, body = call(t, r, http.MethodGet, "/sync/pull?since=7", "kl_alice", nil)
	if code != 200 || !bytes.Contains(body, []byte(`"cursor":8`)) {
		t.Fatalf("pull: %d %s", code, body)
	}
	// error mapping: rejection → 400 with code, not found → 404, key clash → 409
	code, body = call(t, r, http.MethodPost, "/gates", "kl_alice", port.GateRequest{ProposalRef: "p"})
	if code != 400 || !bytes.Contains(body, []byte(`"code":"agent-only"`)) {
		t.Fatalf("rejected gate: %d %s", code, body)
	}
	if code, _ := call(t, r, http.MethodPost, "/gates/nope/resolve", "kl_alice", port.GateResolution{Decision: "allow", Reason: "r"}); code != 404 {
		t.Fatalf("missing gate: %d", code)
	}
	if code, body := call(t, r, http.MethodPost, "/gates/g1/resolve", "kl_alice", port.GateResolution{Decision: "allow", Reason: "r"}); code != 200 || !bytes.Contains(body, []byte(`"resolved"`)) {
		t.Fatalf("resolve: %d %s", code, body)
	}
	if code, _ := call(t, r, http.MethodPost, "/sync/register", "kl_alice", map[string]string{"daemon_id": "taken", "public_key": "k"}); code != 409 {
		t.Fatalf("rebind: %d", code)
	}
	// proofs: parameters validated, org taken from the principal
	if code, _ := call(t, r, http.MethodGet, "/ledger/proof/inclusion?seq=0", "kl_alice", nil); code != 400 {
		t.Fatalf("seq 0: %d", code)
	}
	code, body = call(t, r, http.MethodGet, "/ledger/proof/inclusion?seq=2", "kl_alice", nil)
	if code != 200 || !bytes.Contains(body, []byte(`"org":"acme"`)) {
		t.Fatalf("inclusion: %d %s", code, body)
	}
	if code, _ := call(t, r, http.MethodGet, "/ledger/proof/consistency?from=3&to=2", "kl_alice", nil); code != 400 {
		t.Fatalf("from>to: %d", code)
	}
	// device login is 501 until an identity provider is configured
	if code, _ := call(t, r, http.MethodPost, "/auth/device/start", "", map[string]string{}); code != 501 {
		t.Fatalf("device start: %d", code)
	}
}
