package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

// Ledger is one org's view of the store.
type Ledger struct {
	s   *Store
	org string
}

// Ledger returns the org-scoped ledger (the org must exist: EnsureOrg).
func (s *Store) Ledger(org string) *Ledger { return &Ledger{s: s, org: org} }

// Org returns the tenant id.
func (l *Ledger) Org() string { return l.org }

const columns = `seq, stream, ver, kind, v, ts, actor, meta, body, meta_hash, body_hash, prev_hash, hash, sig, idem, origin`

// Append implements port.Ledger. The org head row is locked for the
// transaction, which serialises appends per tenant.
func (l *Ledger) Append(ctx context.Context, stream string, expected int64, evs []core.Envelope) (port.Range, error) {
	if len(evs) == 0 {
		return port.Range{}, port.ErrEmptyAppend
	}
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return port.Range{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var headSeq int64
	var prev []byte
	if err := tx.QueryRowContext(ctx, `SELECT seq, hash FROM org_head WHERE org_id = $1 FOR UPDATE`, l.org).Scan(&headSeq, &prev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return port.Range{}, fmt.Errorf("postgres: unknown org %q", l.org)
		}
		return port.Range{}, err
	}
	seen := map[string]struct{}{}
	for _, e := range evs {
		if e.Idem == "" {
			continue
		}
		if _, dup := seen[e.Idem]; dup {
			return port.Range{}, port.ErrDuplicateIdempotencyKey
		}
		seen[e.Idem] = struct{}{}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(1) FROM event WHERE org_id = $1 AND stream = $2 AND idem = $3`, l.org, stream, e.Idem).Scan(&n); err != nil {
			return port.Range{}, err
		}
		if n > 0 {
			return port.Range{}, port.ErrDuplicateIdempotencyKey
		}
	}
	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(ver), 0) FROM event WHERE org_id = $1 AND stream = $2`, l.org, stream).Scan(&cur); err != nil {
		return port.Range{}, err
	}
	if expected != port.AnyVersion && expected != cur {
		return port.Range{}, port.ErrVersionConflict
	}
	fetch := func(id merkle.NodeID) ([]byte, error) {
		var h []byte
		err := tx.QueryRowContext(ctx, `SELECT hash FROM merkle_node WHERE org_id = $1 AND level = $2 AND idx = $3`, l.org, int(id.Level), int64(id.Index)).Scan(&h)
		if err != nil {
			return nil, fmt.Errorf("merkle node %d/%d: %w", id.Level, id.Index, err)
		}
		return h, nil
	}
	r := port.Range{Stream: stream, FromVer: cur + 1, ToVer: cur + int64(len(evs)), FromSeq: headSeq + 1}
	for i, e := range evs {
		e.Stream, e.Ver, e.Seq = stream, cur+int64(i)+1, headSeq+int64(i)+1
		if err := e.Seal(prev); err != nil {
			return port.Range{}, err
		}
		if l.s.signer != nil && len(e.Sig) == 0 {
			if e.Sig, err = l.s.signer.Sign(e.Hash); err != nil {
				return port.Range{}, err
			}
		}
		actor, _ := json.Marshal(e.Actor)
		meta, _ := json.Marshal(e.Meta)
		var origin any
		if e.Origin != nil {
			b, _ := json.Marshal(e.Origin)
			origin = string(b)
		}
		var idem any
		if e.Idem != "" {
			idem = e.Idem
		}
		leaf := merkle.LeafHash(e.MetaHash, e.BodyHash)
		_, err := tx.ExecContext(ctx, `INSERT INTO event (org_id, seq, stream, ver, kind, v, ts, actor, meta, body, meta_hash, body_hash, prev_hash, hash, leaf_hash, sig, origin, idem)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			l.org, e.Seq, e.Stream, e.Ver, e.Kind, e.V, e.TS.UTC(), string(actor), string(meta), string(e.Body),
			[]byte(e.MetaHash), []byte(e.BodyHash), []byte(e.PrevHash), []byte(e.Hash), leaf, e.Sig, origin, idem)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				switch pgErr.ConstraintName {
				case "event_org_origin":
					return port.Range{}, port.ErrDuplicateOrigin
				case "event_org_id_stream_idem_key":
					return port.Range{}, port.ErrDuplicateIdempotencyKey
				case "event_org_id_stream_ver_key":
					return port.Range{}, port.ErrVersionConflict
				}
			}
			return port.Range{}, fmt.Errorf("postgres: insert: %w", err)
		}
		nodes, err := merkle.ParentsToStore(uint64(e.Seq-1), leaf, fetch)
		if err != nil {
			return port.Range{}, err
		}
		for _, n := range nodes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO merkle_node (org_id, level, idx, hash) VALUES ($1,$2,$3,$4)`, l.org, int(n.ID.Level), int64(n.ID.Index), n.Hash); err != nil {
				return port.Range{}, fmt.Errorf("postgres: merkle node: %w", err)
			}
		}
		prev = e.Hash
		r.ToSeq = e.Seq
	}
	if _, err := tx.ExecContext(ctx, `UPDATE org_head SET seq = $2, hash = $3 WHERE org_id = $1`, l.org, r.ToSeq, prev); err != nil {
		return port.Range{}, err
	}
	if err := tx.Commit(); err != nil {
		return port.Range{}, fmt.Errorf("postgres: commit: %w", err)
	}
	return r, nil
}

func scan(rows *sql.Rows) (core.Envelope, error) {
	var (
		e                 core.Envelope
		ts                time.Time
		actor, meta, body string
		mh, bh, ph, h, sg []byte
		idem, origin      sql.NullString
	)
	if err := rows.Scan(&e.Seq, &e.Stream, &e.Ver, &e.Kind, &e.V, &ts, &actor, &meta, &body, &mh, &bh, &ph, &h, &sg, &idem, &origin); err != nil {
		return e, err
	}
	e.TS = ts.UTC()
	if err := json.Unmarshal([]byte(actor), &e.Actor); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(meta), &e.Meta); err != nil {
		return e, err
	}
	e.Body = json.RawMessage(body)
	e.MetaHash, e.BodyHash, e.PrevHash, e.Hash, e.Sig = mh, bh, ph, h, sg
	if idem.Valid {
		e.Idem = idem.String
	}
	if origin.Valid {
		var o core.Origin
		if err := json.Unmarshal([]byte(origin.String), &o); err == nil {
			e.Origin = &o
		}
	}
	return e, nil
}

func (l *Ledger) query(ctx context.Context, q string, args ...any) ([]core.Envelope, error) {
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []core.Envelope{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Read implements port.Ledger.
func (l *Ledger) Read(ctx context.Context, stream string, from int64) ([]core.Envelope, error) {
	return l.query(ctx, `SELECT `+columns+` FROM event WHERE org_id = $1 AND stream = $2 AND ver >= $3 ORDER BY ver`, l.org, stream, from)
}

// ReadAll implements port.Ledger.
func (l *Ledger) ReadAll(ctx context.Context, fromSeq int64, limit int) ([]core.Envelope, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	return l.query(ctx, `SELECT `+columns+` FROM event WHERE org_id = $1 AND seq >= $2 ORDER BY seq LIMIT $3`, l.org, fromSeq, limit)
}

// Head implements port.Ledger.
func (l *Ledger) Head(ctx context.Context) (port.Head, error) {
	var h port.Head
	var hash []byte
	err := l.s.db.QueryRowContext(ctx, `SELECT seq, hash FROM org_head WHERE org_id = $1`, l.org).Scan(&h.Seq, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return port.Head{}, nil
	}
	if err != nil {
		return port.Head{}, err
	}
	h.Hash = hash
	return h, nil
}

// Lookup implements port.Ledger.
func (l *Ledger) Lookup(ctx context.Context, stream, idem string) (core.Envelope, error) {
	evs, err := l.query(ctx, `SELECT `+columns+` FROM event WHERE org_id = $1 AND stream = $2 AND idem = $3`, l.org, stream, idem)
	if err != nil {
		return core.Envelope{}, err
	}
	if len(evs) == 0 {
		return core.Envelope{}, port.ErrNotFound
	}
	return evs[0], nil
}

// FindOrigin implements port.OriginLedger.
func (l *Ledger) FindOrigin(ctx context.Context, daemonID string, localSeq int64) (int64, error) {
	tx, err := l.s.tx(ctx, l.org)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var seq int64
	err = tx.QueryRowContext(ctx, `SELECT seq FROM event WHERE org_id = $1 AND origin->>'daemon_id' = $2 AND (origin->>'local_seq')::bigint = $3`, l.org, daemonID, localSeq).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, port.ErrNotFound
	}
	return seq, err
}

var (
	_ port.Ledger       = (*Ledger)(nil)
	_ port.OriginLedger = (*Ledger)(nil)
)
