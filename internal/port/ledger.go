package port

import (
	"context"
	"errors"

	"github.com/young1ll/keelage/internal/core"
)

// AnyVersion disables the optimistic-concurrency check in Append.
const AnyVersion int64 = -1

var (
	// ErrVersionConflict: the stream's current version differs from expected.
	ErrVersionConflict = errors.New("ledger: version conflict")
	// ErrDuplicateIdempotencyKey: (stream, idem) was already appended.
	ErrDuplicateIdempotencyKey = errors.New("ledger: duplicate idempotency key")
	// ErrEmptyAppend: Append was called with no events.
	ErrEmptyAppend = errors.New("ledger: nothing to append")
	// ErrNotFound: no record matches.
	ErrNotFound = errors.New("ledger: not found")
)

// Range describes the records one Append wrote.
type Range struct {
	Stream  string
	FromVer int64
	ToVer   int64
	FromSeq int64
	ToSeq   int64
}

// Head is the chain head: the last global seq and its hash (0, nil when empty).
type Head struct {
	Seq  int64
	Hash core.Hash
}

// Ledger is the append-only event store (architecture-patterns-v0 §4.1).
// The daemon (SQLite) and the server (Postgres) implement the same port.
//
// Append assigns Stream, Ver (per stream, from 1) and Seq (global, from 1),
// seals the hash chain (hash = H(prev || meta_hash || body_hash), chained by
// global seq) and signs when a signer is configured. It is atomic: either all
// events are written or none. Idempotency is checked before the version so a
// retried command reports ErrDuplicateIdempotencyKey rather than a conflict.
type Ledger interface {
	Append(ctx context.Context, stream string, expected int64, evs []core.Envelope) (Range, error)
	// Read returns the stream's records with Ver >= from (from <= 1 means all).
	Read(ctx context.Context, stream string, from int64) ([]core.Envelope, error)
	// ReadAll returns records with Seq >= fromSeq in seq order, at most limit (<= 0: no limit).
	ReadAll(ctx context.Context, fromSeq int64, limit int) ([]core.Envelope, error)
	// Head returns the chain head.
	Head(ctx context.Context) (Head, error)
	// Lookup returns the record that carries (stream, idem), or ErrNotFound.
	Lookup(ctx context.Context, stream, idem string) (core.Envelope, error)
}
