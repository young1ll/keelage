package postgres

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/young1ll/keelage/internal/merkle"
)

func (l *Ledger) fetcher(ctx context.Context) func(merkle.NodeID) ([]byte, error) {
	return func(id merkle.NodeID) ([]byte, error) {
		tx, err := l.s.tx(ctx, l.org)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		var h []byte
		if err := tx.QueryRowContext(ctx, `SELECT hash FROM merkle_node WHERE org_id = $1 AND level = $2 AND idx = $3`, l.org, int(id.Level), int64(id.Index)).Scan(&h); err != nil {
			return nil, fmt.Errorf("merkle node %d/%d: %w", id.Level, id.Index, err)
		}
		return h, nil
	}
}

// Root returns the Merkle root over the first size records.
func (l *Ledger) Root(ctx context.Context, size uint64) ([]byte, error) {
	return merkle.Root(size, l.fetcher(ctx))
}

// Checkpoint signs the current tree head and stores it. A checkpoint at
// the same size is returned unchanged (idempotent).
func (l *Ledger) Checkpoint(ctx context.Context, priv ed25519.PrivateKey, keyID string, now time.Time) (merkle.Checkpoint, error) {
	head, err := l.Head(ctx)
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	if cp, err := l.CheckpointAt(ctx, uint64(head.Seq)); err == nil {
		return cp, nil
	}
	root, err := l.Root(ctx, uint64(head.Seq))
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	cp := merkle.Checkpoint{Org: l.org, TreeSize: uint64(head.Seq), Root: root, At: now}
	cp.Sign(priv, keyID)
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoint (org_id, tree_size, root, ts, sig, key_id) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
		l.org, int64(cp.TreeSize), cp.Root, cp.At, cp.Sig, cp.KeyID); err != nil {
		return merkle.Checkpoint{}, err
	}
	return cp, tx.Commit()
}

// CheckpointAt returns the stored checkpoint for a tree size.
func (l *Ledger) CheckpointAt(ctx context.Context, size uint64) (merkle.Checkpoint, error) {
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cp := merkle.Checkpoint{Org: l.org, TreeSize: size}
	err = tx.QueryRowContext(ctx, `SELECT root, ts, sig, key_id FROM checkpoint WHERE org_id = $1 AND tree_size = $2`, l.org, int64(size)).Scan(&cp.Root, &cp.At, &cp.Sig, &cp.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return merkle.Checkpoint{}, errors.New("postgres: no checkpoint at that size")
	}
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	cp.At = cp.At.UTC()
	return cp, nil
}

// LatestCheckpoint returns the newest checkpoint.
func (l *Ledger) LatestCheckpoint(ctx context.Context) (merkle.Checkpoint, error) {
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cp := merkle.Checkpoint{Org: l.org}
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT tree_size, root, ts, sig, key_id FROM checkpoint WHERE org_id = $1 ORDER BY tree_size DESC LIMIT 1`, l.org).Scan(&size, &cp.Root, &cp.At, &cp.Sig, &cp.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return merkle.Checkpoint{}, errors.New("postgres: no checkpoint yet")
	}
	if err != nil {
		return merkle.Checkpoint{}, err
	}
	cp.TreeSize, cp.At = uint64(size), cp.At.UTC()
	return cp, nil
}

// InclusionBundle proves record seq (1-based) against the latest checkpoint
// that covers it.
func (l *Ledger) InclusionBundle(ctx context.Context, seq int64) (merkle.Bundle, error) {
	cp, err := l.LatestCheckpoint(ctx)
	if err != nil {
		return merkle.Bundle{}, err
	}
	if seq < 1 || uint64(seq) > cp.TreeSize {
		return merkle.Bundle{}, fmt.Errorf("postgres: seq %d is not covered by the latest checkpoint (size %d)", seq, cp.TreeSize)
	}
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return merkle.Bundle{}, err
	}
	var mh, bh, leaf []byte
	err = tx.QueryRowContext(ctx, `SELECT meta_hash, body_hash, leaf_hash FROM event WHERE org_id = $1 AND seq = $2`, l.org, seq).Scan(&mh, &bh, &leaf)
	_ = tx.Rollback()
	if err != nil {
		return merkle.Bundle{}, err
	}
	p, err := merkle.InclusionProof(uint64(seq-1), cp.TreeSize, l.fetcher(ctx))
	if err != nil {
		return merkle.Bundle{}, err
	}
	return merkle.Bundle{Org: l.org, Seq: seq, LeafIndex: uint64(seq - 1), LeafHash: leaf, MetaHash: mh, BodyHash: bh, TreeSize: cp.TreeSize, Proof: p, Checkpoint: cp}, nil
}

// ConsistencyBundle proves that the checkpoint at size `to` extends the one at `from`.
func (l *Ledger) ConsistencyBundle(ctx context.Context, from, to uint64) (merkle.ConsistencyBundle, error) {
	a, err := l.CheckpointAt(ctx, from)
	if err != nil {
		return merkle.ConsistencyBundle{}, fmt.Errorf("from: %w", err)
	}
	b, err := l.CheckpointAt(ctx, to)
	if err != nil {
		return merkle.ConsistencyBundle{}, fmt.Errorf("to: %w", err)
	}
	p, err := merkle.ConsistencyProof(from, to, l.fetcher(ctx))
	if err != nil {
		return merkle.ConsistencyBundle{}, err
	}
	return merkle.ConsistencyBundle{Org: l.org, From: a, To: b, Proof: p}, nil
}
