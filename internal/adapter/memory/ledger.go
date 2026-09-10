// Package memory holds in-memory adapters: a ledger for tests and the
// command pipeline's unit tests, a fixed clock and a sequential ID generator.
package memory

import (
	"context"
	"strconv"
	"sync"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// Ledger is an in-memory port.Ledger with the same hash-chain, idempotency
// and optimistic-concurrency semantics as the SQLite ledger.
type Ledger struct {
	mu      sync.Mutex
	all     []core.Envelope
	streams map[string][]int // stream -> indices into all
	idem    map[string]int   // stream\x00idem -> index
	origins map[string]int   // daemon\x00local_seq -> index
	signer  port.Signer
}

// NewLedger returns an empty ledger. signer may be nil.
func NewLedger(signer port.Signer) *Ledger {
	return &Ledger{streams: map[string][]int{}, idem: map[string]int{}, origins: map[string]int{}, signer: signer}
}

func idemKey(stream, idem string) string { return stream + "\x00" + idem }

func originKey(daemon string, localSeq int64) string {
	return daemon + "\x00" + strconv.FormatInt(localSeq, 10)
}

func clone(e core.Envelope) core.Envelope {
	e.Body = append([]byte(nil), e.Body...)
	e.Meta.Refs = append([]string(nil), e.Meta.Refs...)
	e.MetaHash = append(core.Hash(nil), e.MetaHash...)
	e.BodyHash = append(core.Hash(nil), e.BodyHash...)
	e.PrevHash = append(core.Hash(nil), e.PrevHash...)
	e.Hash = append(core.Hash(nil), e.Hash...)
	e.Sig = append([]byte(nil), e.Sig...)
	if e.Origin != nil {
		o := *e.Origin
		o.PrevHash = append(core.Hash(nil), o.PrevHash...)
		e.Origin = &o
	}
	return e
}

// Append implements port.Ledger.
func (l *Ledger) Append(_ context.Context, stream string, expected int64, evs []core.Envelope) (port.Range, error) {
	if len(evs) == 0 {
		return port.Range{}, port.ErrEmptyAppend
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	batch := map[string]struct{}{}
	for _, e := range evs {
		if e.Idem == "" {
			continue
		}
		k := idemKey(stream, e.Idem)
		if _, dup := l.idem[k]; dup {
			return port.Range{}, port.ErrDuplicateIdempotencyKey
		}
		if _, dup := batch[k]; dup {
			return port.Range{}, port.ErrDuplicateIdempotencyKey
		}
		batch[k] = struct{}{}
	}
	for _, e := range evs {
		if e.Origin == nil {
			continue
		}
		if _, dup := l.origins[originKey(e.Origin.DaemonID, e.Origin.LocalSeq)]; dup {
			return port.Range{}, port.ErrDuplicateOrigin
		}
	}
	cur := int64(len(l.streams[stream]))
	if expected != port.AnyVersion && expected != cur {
		return port.Range{}, port.ErrVersionConflict
	}
	var prev core.Hash
	if n := len(l.all); n > 0 {
		prev = l.all[n-1].Hash
	}
	r := port.Range{Stream: stream, FromVer: cur + 1, FromSeq: int64(len(l.all)) + 1}
	sealed := make([]core.Envelope, 0, len(evs))
	for i, e := range evs {
		e = clone(e)
		e.Stream = stream
		e.Ver = cur + int64(i) + 1
		e.Seq = int64(len(l.all)+i) + 1
		if err := e.Seal(prev); err != nil {
			return port.Range{}, err
		}
		if l.signer != nil && len(e.Sig) == 0 {
			sig, err := l.signer.Sign(e.Hash)
			if err != nil {
				return port.Range{}, err
			}
			e.Sig = sig
		}
		prev = e.Hash
		sealed = append(sealed, e)
	}
	for _, e := range sealed {
		idx := len(l.all)
		l.all = append(l.all, e)
		l.streams[stream] = append(l.streams[stream], idx)
		if e.Idem != "" {
			l.idem[idemKey(stream, e.Idem)] = idx
		}
		if e.Origin != nil {
			l.origins[originKey(e.Origin.DaemonID, e.Origin.LocalSeq)] = idx
		}
	}
	r.ToVer = cur + int64(len(evs))
	r.ToSeq = int64(len(l.all))
	return r, nil
}

// Read implements port.Ledger.
func (l *Ledger) Read(_ context.Context, stream string, from int64) ([]core.Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	idxs := l.streams[stream]
	out := make([]core.Envelope, 0, len(idxs))
	for _, i := range idxs {
		if l.all[i].Ver >= from {
			out = append(out, clone(l.all[i]))
		}
	}
	return out, nil
}

// ReadAll implements port.Ledger.
func (l *Ledger) ReadAll(_ context.Context, fromSeq int64, limit int) ([]core.Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fromSeq < 1 {
		fromSeq = 1
	}
	out := []core.Envelope{}
	for i := int(fromSeq) - 1; i < len(l.all); i++ {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, clone(l.all[i]))
	}
	return out, nil
}

// Head implements port.Ledger.
func (l *Ledger) Head(_ context.Context) (port.Head, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.all)
	if n == 0 {
		return port.Head{}, nil
	}
	return port.Head{Seq: int64(n), Hash: append(core.Hash(nil), l.all[n-1].Hash...)}, nil
}

// FindOrigin implements port.OriginLedger.
func (l *Ledger) FindOrigin(_ context.Context, daemonID string, localSeq int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i, ok := l.origins[originKey(daemonID, localSeq)]
	if !ok {
		return 0, port.ErrNotFound
	}
	return l.all[i].Seq, nil
}

// Lookup implements port.Ledger.
func (l *Ledger) Lookup(_ context.Context, stream, idem string) (core.Envelope, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i, ok := l.idem[idemKey(stream, idem)]
	if !ok {
		return core.Envelope{}, port.ErrNotFound
	}
	return clone(l.all[i]), nil
}
