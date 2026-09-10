package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
	"github.com/young1ll/keelage/internal/port/ledgertest"
)

func open(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestLedger_Contract(t *testing.T) {
	ledgertest.Run(t, func(t *testing.T) port.Ledger { return open(t) })
}

func TestLedger_Reopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := core.Envelope{Kind: "A", V: 1, Actor: core.ActorRef{Kind: core.ActorHuman, ID: "u"}, Body: []byte(`{}`), Idem: "k"}
	if _, err := l.Append(ctx, "s", 0, []core.Envelope{ev}); err != nil {
		t.Fatal(err)
	}
	h1, _ := l.Head(ctx)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(path, nil) // migrations are idempotent, data persists
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	h2, _ := l2.Head(ctx)
	if h2.Seq != 1 || !h2.Hash.Equal(h1.Hash) {
		t.Fatalf("head after reopen: %+v vs %+v", h2, h1)
	}
	if _, err := l2.Append(ctx, "s", 0, []core.Envelope{ev}); err == nil {
		t.Fatal("idempotency must survive reopen")
	}
	ev.Idem = ""
	if _, err := l2.Append(ctx, "s", 1, []core.Envelope{ev}); err != nil {
		t.Fatal(err)
	}
	all, _ := l2.ReadAll(ctx, 0, 0)
	if _, err := core.VerifyChain(nil, all); err != nil || len(all) != 2 {
		t.Fatalf("chain across reopen: %v (%d)", err, len(all))
	}
	var mode string
	if err := l2.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("%s: %v", suffix, err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %o, personal data must be 0600", suffix, fi.Mode().Perm())
		}
	}
}

type fakeSigner struct{}

func (fakeSigner) Sign(h []byte) ([]byte, error) { return append([]byte("sig:"), h...), nil }
func (fakeSigner) Fingerprint() string           { return "fake" }

func TestLedger_Signs(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"), fakeSigner{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	ev := core.Envelope{Kind: "A", V: 1, Actor: core.ActorRef{Kind: core.ActorHuman, ID: "u"}, Body: []byte(`{}`)}
	if _, err := l.Append(context.Background(), "s", 0, []core.Envelope{ev}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Read(context.Background(), "s", 0)
	if string(got[0].Sig[:4]) != "sig:" || !core.Hash(got[0].Sig[4:]).Equal(got[0].Hash) {
		t.Fatalf("sig %x", got[0].Sig)
	}
}
