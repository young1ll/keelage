package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
	"github.com/young1ll/keelage/internal/port/ledgertest"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("KEELAGE_TEST_PG")
	if dsn == "" {
		t.Skip("KEELAGE_TEST_PG not set (make test-pg)")
	}
	s, err := Open(context.Background(), dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var orgCounter int

func freshLedger(t *testing.T, s *Store) *Ledger {
	t.Helper()
	orgCounter++
	org := fmt.Sprintf("t-%d-%d", time.Now().UnixNano(), orgCounter)
	if err := s.EnsureOrg(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return s.Ledger(org)
}

func TestLedger_Contract(t *testing.T) {
	s := openStore(t)
	ledgertest.Run(t, func(t *testing.T) port.Ledger { return freshLedger(t, s) })
}

func env(kind, body string) core.Envelope {
	return core.Envelope{Kind: kind, V: 1, TS: time.Now(), Actor: core.ActorRef{Kind: core.ActorHuman, ID: "u"}, Body: json.RawMessage(body)}
}

func TestLedger_MerkleAndCheckpoints(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	l := freshLedger(t, s)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	for i := 0; i < 13; i++ {
		if _, err := l.Append(ctx, fmt.Sprintf("s%d", i%3), port.AnyVersion, []core.Envelope{env("E", fmt.Sprintf(`{"i":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
		if i == 4 {
			cp, err := l.Checkpoint(ctx, priv, "k1", time.Now())
			if err != nil || cp.TreeSize != 5 {
				t.Fatalf("checkpoint@5: %+v %v", cp, err)
			}
		}
	}
	cp, err := l.Checkpoint(ctx, priv, "k1", time.Now())
	if err != nil || cp.TreeSize != 13 {
		t.Fatalf("checkpoint: %+v %v", cp, err)
	}
	if err := cp.Verify(pub); err != nil {
		t.Fatal(err)
	}
	again, _ := l.Checkpoint(ctx, priv, "k1", time.Now().Add(time.Hour))
	if !again.At.Equal(cp.At) {
		t.Fatal("checkpoint at the same size must be reused")
	}
	// every record verifies against the latest checkpoint, offline
	for seq := int64(1); seq <= 13; seq++ {
		b, err := l.InclusionBundle(ctx, seq)
		if err != nil {
			t.Fatalf("bundle %d: %v", seq, err)
		}
		if err := merkle.VerifyBundle(b, pub); err != nil {
			t.Fatalf("verify %d: %v", seq, err)
		}
		b.BodyHash = b.MetaHash
		if err := merkle.VerifyBundle(b, pub); err == nil {
			t.Fatal("tampered bundle must fail")
		}
	}
	if _, err := l.InclusionBundle(ctx, 14); err == nil {
		t.Fatal("seq beyond the checkpoint must fail")
	}
	cb, err := l.ConsistencyBundle(ctx, 5, 13)
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyConsistencyBundle(cb, pub); err != nil {
		t.Fatal(err)
	}
	// the chain and the tree agree with a fresh read
	all, _ := l.ReadAll(ctx, 0, 0)
	if _, err := core.VerifyChain(nil, all); err != nil || len(all) != 13 {
		t.Fatalf("chain: %v (%d)", err, len(all))
	}
	root, _ := l.Root(ctx, 13)
	if string(root) != string(cp.Root) {
		t.Fatal("root mismatch")
	}
}

func TestLedger_OriginAndTenancy(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, b := freshLedger(t, s), freshLedger(t, s)
	e := env("E", `{}`)
	e.Origin = &core.Origin{DaemonID: "d1", LocalSeq: 7}
	if _, err := a.Append(ctx, "s", 0, []core.Envelope{e}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Append(ctx, "s", 1, []core.Envelope{e}); !errors.Is(err, port.ErrDuplicateOrigin) {
		t.Fatalf("duplicate origin: %v", err)
	}
	if seq, err := a.FindOrigin(ctx, "d1", 7); err != nil || seq != 1 {
		t.Fatalf("find origin: %d %v", seq, err)
	}
	// the same origin in another org is a different tenant
	if _, err := b.Append(ctx, "s", 0, []core.Envelope{e}); err != nil {
		t.Fatalf("other org: %v", err)
	}
	if _, err := b.FindOrigin(ctx, "d1", 8); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("unknown origin: %v", err)
	}
	ha, _ := a.Head(ctx)
	hb, _ := b.Head(ctx)
	if ha.Seq != 1 || hb.Seq != 1 {
		t.Fatalf("per-org seq: %d %d", ha.Seq, hb.Seq)
	}
	if got, _ := a.Read(ctx, "s", 0); len(got) != 1 || got[0].Origin == nil || got[0].Origin.LocalSeq != 7 {
		t.Fatalf("origin round trip: %+v", got)
	}
	if got, _ := b.ReadAll(ctx, 0, 0); len(got) != 1 {
		t.Fatalf("tenant isolation: %d", len(got))
	}
}
