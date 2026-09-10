// Package ledgertest is the contract test suite every port.Ledger
// implementation must pass (architecture-patterns-v0 §11: the same suite for
// SQLite and Postgres — and the in-memory ledger).
package ledgertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// Open returns a fresh, empty ledger for one test.
type Open func(t *testing.T) port.Ledger

func env(kind string, body string, idem string) core.Envelope {
	return core.Envelope{
		Kind: kind, V: 1, TS: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Actor: core.ActorRef{Kind: core.ActorHuman, ID: "u1"},
		Meta:  core.EventMeta{Refs: []string{"x"}, Autonomy: core.L1},
		Body:  json.RawMessage(body), Idem: idem,
	}
}

// Run executes the suite.
func Run(t *testing.T, open Open) {
	t.Helper()
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		l := open(t)
		h, err := l.Head(ctx)
		if err != nil || h.Seq != 0 || len(h.Hash) != 0 {
			t.Fatalf("empty head: %+v %v", h, err)
		}
		evs, err := l.Read(ctx, "nope", 0)
		if err != nil || len(evs) != 0 {
			t.Fatalf("read unknown stream: %v %v", evs, err)
		}
		all, err := l.ReadAll(ctx, 0, 0)
		if err != nil || len(all) != 0 {
			t.Fatalf("readall empty: %v %v", all, err)
		}
		if _, err := l.Lookup(ctx, "s", "k"); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("lookup on empty: %v", err)
		}
	})

	t.Run("append and read", func(t *testing.T) {
		l := open(t)
		r, err := l.Append(ctx, "s1", 0, []core.Envelope{env("A", `{"n":1}`, "k1"), env("B", `{"n":2}`, "")})
		if err != nil {
			t.Fatal(err)
		}
		if r.Stream != "s1" || r.FromVer != 1 || r.ToVer != 2 || r.FromSeq != 1 || r.ToSeq != 2 {
			t.Fatalf("range: %+v", r)
		}
		got, err := l.Read(ctx, "s1", 0)
		if err != nil || len(got) != 2 {
			t.Fatalf("read: %v %v", got, err)
		}
		for i, e := range got {
			if e.Stream != "s1" || e.Ver != int64(i+1) || e.Seq != int64(i+1) {
				t.Errorf("record %d: stream/ver/seq %s %d %d", i, e.Stream, e.Ver, e.Seq)
			}
			if e.Kind != []string{"A", "B"}[i] || e.V != 1 || e.Actor.ID != "u1" || e.Meta.Autonomy != core.L1 || len(e.Meta.Refs) != 1 {
				t.Errorf("record %d: fields not preserved: %+v", i, e)
			}
			if !e.TS.Equal(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)) {
				t.Errorf("record %d: ts %v", i, e.TS)
			}
			if string(e.Body) != fmt.Sprintf(`{"n":%d}`, i+1) {
				t.Errorf("record %d: body %s", i, e.Body)
			}
			if len(e.Hash) == 0 || len(e.MetaHash) == 0 || len(e.BodyHash) == 0 {
				t.Errorf("record %d: not sealed", i)
			}
		}
		if got[0].Idem != "k1" || got[1].Idem != "" {
			t.Errorf("idem not preserved: %q %q", got[0].Idem, got[1].Idem)
		}
		if _, err := core.VerifyChain(nil, got); err != nil {
			t.Fatalf("chain: %v", err)
		}
		h, _ := l.Head(ctx)
		if h.Seq != 2 || !h.Hash.Equal(got[1].Hash) {
			t.Fatalf("head: %+v", h)
		}
		from2, _ := l.Read(ctx, "s1", 2)
		if len(from2) != 1 || from2[0].Ver != 2 {
			t.Fatalf("read from 2: %v", from2)
		}
		// second append continues versions
		r2, err := l.Append(ctx, "s1", 2, []core.Envelope{env("C", `{}`, "")})
		if err != nil || r2.FromVer != 3 || r2.ToVer != 3 || r2.FromSeq != 3 {
			t.Fatalf("second append: %+v %v", r2, err)
		}
		// returned slices are copies
		got[0].Body[2] = 'X'
		again, _ := l.Read(ctx, "s1", 0)
		if string(again[0].Body) != `{"n":1}` {
			t.Fatal("ledger must return copies")
		}
	})

	t.Run("optimistic concurrency", func(t *testing.T) {
		l := open(t)
		if _, err := l.Append(ctx, "s", 1, []core.Envelope{env("A", `{}`, "")}); !errors.Is(err, port.ErrVersionConflict) {
			t.Fatalf("new stream with expected 1: %v", err)
		}
		if _, err := l.Append(ctx, "s", 0, []core.Envelope{env("A", `{}`, "")}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(ctx, "s", 0, []core.Envelope{env("A", `{}`, "")}); !errors.Is(err, port.ErrVersionConflict) {
			t.Fatalf("stale expected: %v", err)
		}
		if _, err := l.Append(ctx, "s", 5, []core.Envelope{env("A", `{}`, "")}); !errors.Is(err, port.ErrVersionConflict) {
			t.Fatalf("future expected: %v", err)
		}
		h, _ := l.Head(ctx)
		if h.Seq != 1 {
			t.Fatalf("conflicts must write nothing: head %d", h.Seq)
		}
		if _, err := l.Append(ctx, "s", port.AnyVersion, []core.Envelope{env("A", `{}`, "")}); err != nil {
			t.Fatalf("AnyVersion: %v", err)
		}
		if _, err := l.Append(ctx, "s", 2, []core.Envelope{env("A", `{}`, "")}); err != nil {
			t.Fatalf("exact expected: %v", err)
		}
	})

	t.Run("empty append", func(t *testing.T) {
		l := open(t)
		if _, err := l.Append(ctx, "s", 0, nil); !errors.Is(err, port.ErrEmptyAppend) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("idempotency", func(t *testing.T) {
		l := open(t)
		r, err := l.Append(ctx, "s", 0, []core.Envelope{env("A", `{}`, "cmd-1")})
		if err != nil {
			t.Fatal(err)
		}
		// same key, even with a stale version, reports the duplicate — not a conflict
		if _, err := l.Append(ctx, "s", 0, []core.Envelope{env("A", `{}`, "cmd-1")}); !errors.Is(err, port.ErrDuplicateIdempotencyKey) {
			t.Fatalf("duplicate: %v", err)
		}
		if _, err := l.Append(ctx, "s", 1, []core.Envelope{env("A", `{}`, ""), env("B", `{}`, "cmd-1")}); !errors.Is(err, port.ErrDuplicateIdempotencyKey) {
			t.Fatalf("duplicate on later event: %v", err)
		}
		h, _ := l.Head(ctx)
		if h.Seq != 1 {
			t.Fatal("rejected batch must write nothing")
		}
		e, err := l.Lookup(ctx, "s", "cmd-1")
		if err != nil || e.Seq != r.FromSeq || e.Idem != "cmd-1" {
			t.Fatalf("lookup: %+v %v", e, err)
		}
		// same key on another stream is fine
		if _, err := l.Append(ctx, "other", 0, []core.Envelope{env("A", `{}`, "cmd-1")}); err != nil {
			t.Fatalf("other stream: %v", err)
		}
		// duplicate key within one batch
		if _, err := l.Append(ctx, "s", 1, []core.Envelope{env("A", `{}`, "cmd-2"), env("B", `{}`, "cmd-2")}); err == nil {
			t.Fatal("duplicate key inside a batch must fail")
		}
	})

	t.Run("global chain across streams", func(t *testing.T) {
		l := open(t)
		for i := 0; i < 10; i++ {
			s := []string{"a", "b", "c"}[i%3]
			if _, err := l.Append(ctx, s, port.AnyVersion, []core.Envelope{env("E", fmt.Sprintf(`{"i":%d}`, i), "")}); err != nil {
				t.Fatal(err)
			}
		}
		all, err := l.ReadAll(ctx, 0, 0)
		if err != nil || len(all) != 10 {
			t.Fatalf("readall: %d %v", len(all), err)
		}
		head, err := core.VerifyChain(nil, all)
		if err != nil {
			t.Fatal(err)
		}
		h, _ := l.Head(ctx)
		if !h.Hash.Equal(head) || h.Seq != 10 {
			t.Fatalf("head %+v vs %s", h, head)
		}
		page, _ := l.ReadAll(ctx, 4, 3)
		if len(page) != 3 || page[0].Seq != 4 || page[2].Seq != 6 {
			t.Fatalf("paging: %v", page)
		}
		tail, _ := l.ReadAll(ctx, 9, 100)
		if len(tail) != 2 {
			t.Fatalf("tail: %d", len(tail))
		}
		a, _ := l.Read(ctx, "a", 0)
		if len(a) != 4 || a[3].Ver != 4 {
			t.Fatalf("stream a: %v", a)
		}
	})

	t.Run("concurrent appends", func(t *testing.T) {
		l := open(t)
		const n = 16
		var wg sync.WaitGroup
		errs := make(chan error, n*2)
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				_, err := l.Append(ctx, fmt.Sprintf("s%d", i), port.AnyVersion, []core.Envelope{env("E", `{}`, "")})
				errs <- err
			}(i)
			go func() {
				defer wg.Done()
				// all racing for version 0 of one stream: exactly one wins
				_, err := l.Append(ctx, "shared", 0, []core.Envelope{env("E", `{}`, "")})
				if err != nil && !errors.Is(err, port.ErrVersionConflict) {
					errs <- err
					return
				}
				errs <- nil
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		all, _ := l.ReadAll(ctx, 0, 0)
		if len(all) != n+1 {
			t.Fatalf("expected %d records, got %d", n+1, len(all))
		}
		if _, err := core.VerifyChain(nil, all); err != nil {
			t.Fatal(err)
		}
		shared, _ := l.Read(ctx, "shared", 0)
		if len(shared) != 1 {
			t.Fatalf("exactly one append must win version 0, got %d", len(shared))
		}
	})
}
