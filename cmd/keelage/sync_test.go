package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/adapter/httpapi"
	"github.com/young1ll/keelage/internal/adapter/keys"
	"github.com/young1ll/keelage/internal/adapter/postgres"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

// e2e for weeks 10–11 (completion criterion): two daemons share a team
// constraint through the server, an inclusion proof verifies offline
// against the server key, and an agent asks /gates while a person answers
// — every step a ledger record. Needs a Postgres (make test-pg).
func TestTeam_TwoDaemonsShareThroughServer(t *testing.T) {
	dsn := os.Getenv("KEELAGE_TEST_PG")
	if dsn == "" {
		t.Skip("KEELAGE_TEST_PG not set (make test-pg)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	org := fmt.Sprintf("e2e-%d", time.Now().UnixNano())

	// ---- server: Postgres ledger, checkpoint key, tokens ----
	serverKey, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, dsn, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.EnsureOrg(ctx, org))
	must(store.AddMember(ctx, org, "alice", "owner"))
	must(store.AddMember(ctx, org, "bob", "member"))
	tokAlice, err := store.IssueToken(ctx, org, "alice", "human", "", "", 0)
	must(err)
	tokBob, err := store.IssueToken(ctx, org, "bob", "human", "", "", 0)
	must(err)
	tokAgent, err := store.IssueToken(ctx, org, "cc-1", "agent", "alice", "", 0)
	must(err)

	team := &app.TeamServer{
		Orgs: &app.Orgs{
			Open:  func(_ context.Context, o string) (port.OriginLedger, error) { return store.Ledger(o), nil },
			Codec: app.NewCodec(), Clock: clock{},
		},
		Daemons: store, Keys: keys.Verifier{}, Clock: clock{},
	}
	r := httpapi.New("keelage-server", "test")
	httpapi.MountServer(r, &httpapi.Server{Auth: store, Team: team, Proofs: &e2eProofs{store: store, key: serverKey}, PublicKey: keys.EncodePublic(serverKey.Public)})
	srv := httptest.NewServer(r)
	defer srv.Close()

	// ---- three homes: alice's daemon, bob's daemon, alice's agent ----
	repo := t.TempDir()
	gitc := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitc("init", "-q")
	gitc("config", "user.email", "dev@example.com")
	gitc("config", "user.name", "dev")
	must(os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644))
	gitc("add", ".")
	gitc("commit", "-qm", "init")

	homes := map[string]string{"alice": t.TempDir(), "bob": t.TempDir(), "agent": t.TempDir()}
	run := func(home string, args ...string) (string, error) {
		t.Helper()
		t.Setenv("KEELAGE_HOME", homes[home])
		c := rootCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		err := c.ExecuteContext(ctx)
		return out.String(), err
	}
	ok := func(home string, args ...string) string {
		t.Helper()
		out, err := run(home, args...)
		if err != nil {
			t.Fatalf("keelage %v: %v\n%s", args, err, out)
		}
		return out
	}

	out := ok("alice", "server", "login", "--url", srv.URL, "--token", tokAlice)
	if !strings.Contains(out, "as alice") {
		t.Fatalf("login: %s", out)
	}
	ok("bob", "server", "login", "--url", srv.URL, "--token", tokBob)
	ok("agent", "server", "login", "--url", srv.URL, "--token", tokAgent)
	if _, err := run("bob", "server", "login", "--url", srv.URL, "--token", "kl_nope"); err == nil {
		t.Fatal("bad token accepted")
	}

	// alice authors a team constraint and a personal one, then pushes
	ok("alice", "constraint", "add", "--repo", repo, "--scope", "team=core", "--kind", "rule", "no retries in billing")
	ok("alice", "constraint", "add", "--repo", repo, "--scope", "personal", "--kind", "rule", "my private habit")
	out = ok("alice", "sync", "push", "--json")
	var push app.PushReport
	must(json.Unmarshal([]byte(out), &push))
	if push.Offered != 1 || push.Accepted != 1 || len(push.Rejected) != 0 {
		t.Fatalf("push: %+v", push)
	}
	if out := ok("alice", "sync", "push"); !strings.Contains(out, "offered 0") {
		t.Fatalf("second push: %s", out)
	}

	// bob pulls and sees it; his own list before the pull is empty
	if out := ok("bob", "constraint", "list", "--repo", repo); strings.Contains(out, "no retries") {
		t.Fatalf("bob before pull: %s", out)
	}
	out = ok("bob", "sync", "pull", "--json")
	var pull app.PullReport
	must(json.Unmarshal([]byte(out), &pull))
	if pull.Cached != 1 {
		t.Fatalf("pull: %+v", pull)
	}
	out = ok("bob", "constraint", "list", "--repo", repo)
	if !strings.Contains(out, "no retries in billing") || strings.Contains(out, "private habit") {
		t.Fatalf("bob after pull: %s", out)
	}
	if out := ok("bob", "sync", "status"); !strings.Contains(out, "1 team records cached") {
		t.Fatalf("status: %s", out)
	}

	// an inclusion proof for alice's record verifies offline against the server key
	bundle := ok("alice", "server", "proof", "--seq", "1")
	bundleFile := filepath.Join(t.TempDir(), "bundle.json")
	must(os.WriteFile(bundleFile, []byte(bundle), 0o600))
	out = ok("alice", "verify-proof", "--server-key", keys.EncodePublic(serverKey.Public), "--file", bundleFile)
	if !strings.HasPrefix(out, "VALID inclusion") {
		t.Fatalf("verify-proof: %s", out)
	}
	other, _ := keys.Generate()
	if out, err := run("alice", "verify-proof", "--server-key", keys.EncodePublic(other.Public), "--file", bundleFile); err == nil {
		t.Fatalf("wrong key verified: %s", out)
	}
	if k := strings.TrimSpace(ok("bob", "server", "key")); k != keys.EncodePublic(serverKey.Public) {
		t.Fatalf("server key: %s", k)
	}

	// the agent asks before acting; alice answers; both are on the server ledger
	scope := "org=|product=|team=core|repo=|path=|person="
	out = ok("agent", "gate", "ask", "--scope", scope, "--proposal", "refactor-billing", "--level", "L1", "--json")
	var gate struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Decision string `json:"decision"`
	}
	must(json.Unmarshal([]byte(out), &gate))
	if gate.State != "open" || gate.Decision != "ask" {
		t.Fatalf("ask: %s", out)
	}
	if _, err := run("alice", "gate", "ask", "--scope", scope, "--proposal", "x"); err == nil {
		t.Fatal("a person asking a gate must be refused")
	}
	if _, err := run("agent", "gate", "resolve", gate.ID, "--allow", "--reason", "self"); err == nil {
		t.Fatal("an agent resolving must be refused")
	}
	out = ok("alice", "gate", "resolve", gate.ID, "--allow", "--reason", "reviewed the plan")
	if !strings.Contains(out, "resolved  allow") {
		t.Fatalf("resolve: %s", out)
	}
	if out := ok("bob", "gate", "list", "--state", "resolved"); !strings.Contains(out, gate.ID) {
		t.Fatalf("list: %s", out)
	}
	envs, err := store.Ledger(org).Read(ctx, "gate/"+gate.ID, 0)
	must(err)
	if len(envs) != 2 || envs[0].Kind != "GateOpened" || envs[1].Kind != "GateResolved" {
		t.Fatalf("gate stream: %+v", envs)
	}
	// the server signed its own records; alice's pushed record keeps her daemon's signature
	first, err := store.Ledger(org).ReadAll(ctx, 1, 1)
	must(err)
	if first[0].Origin == nil || !keys.Verify(serverKey.Public, envs[0].Hash, envs[0].Sig) {
		t.Fatalf("signatures: origin %+v", first[0].Origin)
	}
	pubA, _, err := store.DaemonKey(ctx, org, first[0].Origin.DaemonID)
	must(err)
	if !(keys.Verifier{}).Verify(pubA, first[0].OriginHash(), first[0].Sig) {
		t.Fatal("alice's daemon signature must verify from the server copy")
	}
}

type e2eProofs struct {
	store *postgres.Store
	key   *keys.Key
}

func (p *e2eProofs) Checkpoint(ctx context.Context, org string) (merkle.Checkpoint, error) {
	return p.store.Ledger(org).Checkpoint(ctx, p.key.Private, p.key.Fingerprint(), time.Now())
}

func (p *e2eProofs) Inclusion(ctx context.Context, org string, seq int64) (merkle.Bundle, error) {
	l := p.store.Ledger(org)
	if _, err := l.Checkpoint(ctx, p.key.Private, p.key.Fingerprint(), time.Now()); err != nil {
		return merkle.Bundle{}, err
	}
	return l.InclusionBundle(ctx, seq)
}

func (p *e2eProofs) Consistency(ctx context.Context, org string, from, to uint64) (merkle.ConsistencyBundle, error) {
	return p.store.Ledger(org).ConsistencyBundle(ctx, from, to)
}
