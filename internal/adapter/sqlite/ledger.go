// Package sqlite is the daemon's ledger: an append-only event table with a
// hash chain, idempotency keys and optimistic concurrency, on SQLite
// (modernc, no CGO) in WAL mode (architecture-patterns-v0 §4.2).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// migrations are applied in order; the ledger schema is add-only.
var migrations = []string{
	`CREATE TABLE event (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		stream     TEXT    NOT NULL,
		ver        INTEGER NOT NULL,
		kind       TEXT    NOT NULL,
		v          INTEGER NOT NULL,
		ts         TEXT    NOT NULL,
		actor      TEXT    NOT NULL,
		meta       TEXT    NOT NULL,
		body       BLOB    NOT NULL,
		meta_hash  BLOB    NOT NULL,
		body_hash  BLOB    NOT NULL,
		prev_hash  BLOB,
		hash       BLOB    NOT NULL,
		sig        BLOB,
		idem       TEXT,
		UNIQUE(stream, ver),
		UNIQUE(stream, idem)
	);
	CREATE INDEX event_stream ON event(stream, ver);`,
	// v2: provenance of synced copies (the team cache keeps the server's origin)
	`ALTER TABLE event ADD COLUMN origin TEXT`,
}

// Ledger implements port.Ledger on SQLite.
type Ledger struct {
	db     *sql.DB
	mu     sync.Mutex // one writer: the daemon is the only process appending
	signer port.Signer
}

// Open opens (creating if needed) the ledger at path and applies migrations.
// signer may be nil.
func Open(path string, signer port.Signer) (*Ledger, error) {
	// Personal data: create the file 0600 before SQLite touches it, so the
	// -wal/-shm siblings inherit the mode.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sqlite: create: %w", err)
	}
	_ = f.Close()
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": []string{"journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)", "foreign_keys(ON)"},
	}.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	l := &Ledger{db: db, signer: signer}
	if err := l.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
}

// Close closes the database.
func (l *Ledger) Close() error { return l.db.Close() }

func (l *Ledger) migrate(ctx context.Context) error {
	if _, err := l.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	var current int
	if err := l.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migration`).Scan(&current); err != nil {
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	for i := current; i < len(migrations); i++ {
		tx, err := l.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migration(version, applied_at) VALUES (?, ?)`, i+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const columns = `seq, stream, ver, kind, v, ts, actor, meta, body, meta_hash, body_hash, prev_hash, hash, sig, idem, origin`

// Append implements port.Ledger.
func (l *Ledger) Append(ctx context.Context, stream string, expected int64, evs []core.Envelope) (port.Range, error) {
	if len(evs) == 0 {
		return port.Range{}, port.ErrEmptyAppend
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return port.Range{}, fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM event WHERE stream = ? AND idem = ?`, stream, e.Idem).Scan(&n); err != nil {
			return port.Range{}, err
		}
		if n > 0 {
			return port.Range{}, port.ErrDuplicateIdempotencyKey
		}
	}
	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ver), 0) FROM event WHERE stream = ?`, stream).Scan(&cur); err != nil {
		return port.Range{}, err
	}
	if expected != port.AnyVersion && expected != cur {
		return port.Range{}, port.ErrVersionConflict
	}
	var prev core.Hash
	var prevSeq int64
	err = tx.QueryRowContext(ctx, `SELECT seq, hash FROM event ORDER BY seq DESC LIMIT 1`).Scan(&prevSeq, (*[]byte)(&prev))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return port.Range{}, err
	}

	r := port.Range{Stream: stream, FromVer: cur + 1, ToVer: cur + int64(len(evs)), FromSeq: prevSeq + 1}
	for i, e := range evs {
		e.Stream = stream
		e.Ver = cur + int64(i) + 1
		e.Seq = prevSeq + int64(i) + 1
		if err := e.Seal(prev); err != nil {
			return port.Range{}, err
		}
		if l.signer != nil && len(e.Sig) == 0 {
			if e.Sig, err = l.signer.Sign(e.Hash); err != nil {
				return port.Range{}, err
			}
		}
		actor, err := json.Marshal(e.Actor)
		if err != nil {
			return port.Range{}, err
		}
		meta, err := json.Marshal(e.Meta)
		if err != nil {
			return port.Range{}, err
		}
		var idem, origin any
		if e.Idem != "" {
			idem = e.Idem
		}
		if e.Origin != nil {
			b, _ := json.Marshal(e.Origin)
			origin = string(b)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO event (seq, stream, ver, kind, v, ts, actor, meta, body, meta_hash, body_hash, prev_hash, hash, sig, idem, origin)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Seq, e.Stream, e.Ver, e.Kind, e.V, e.TS.UTC().Format(time.RFC3339Nano), string(actor), string(meta), []byte(e.Body),
			[]byte(e.MetaHash), []byte(e.BodyHash), []byte(e.PrevHash), []byte(e.Hash), e.Sig, idem, origin)
		if err != nil {
			return port.Range{}, fmt.Errorf("sqlite: insert: %w", err)
		}
		if id, _ := res.LastInsertId(); id != e.Seq {
			return port.Range{}, fmt.Errorf("sqlite: seq mismatch: wanted %d got %d", e.Seq, id)
		}
		prev = e.Hash
		r.ToSeq = e.Seq
	}
	if err := tx.Commit(); err != nil {
		return port.Range{}, fmt.Errorf("sqlite: commit: %w", err)
	}
	return r, nil
}

func scan(rows *sql.Rows) (core.Envelope, error) {
	var (
		e                       core.Envelope
		ts, actor, meta         string
		body, mh, bh, ph, h, sg []byte
		idem, origin            sql.NullString
	)
	if err := rows.Scan(&e.Seq, &e.Stream, &e.Ver, &e.Kind, &e.V, &ts, &actor, &meta, &body, &mh, &bh, &ph, &h, &sg, &idem, &origin); err != nil {
		return e, err
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return e, fmt.Errorf("sqlite: ts: %w", err)
	}
	e.TS = t
	if err := json.Unmarshal([]byte(actor), &e.Actor); err != nil {
		return e, fmt.Errorf("sqlite: actor: %w", err)
	}
	if err := json.Unmarshal([]byte(meta), &e.Meta); err != nil {
		return e, fmt.Errorf("sqlite: meta: %w", err)
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
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query: %w", err)
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
	return l.query(ctx, `SELECT `+columns+` FROM event WHERE stream = ? AND ver >= ? ORDER BY ver`, stream, from)
}

// ReadAll implements port.Ledger.
func (l *Ledger) ReadAll(ctx context.Context, fromSeq int64, limit int) ([]core.Envelope, error) {
	if limit <= 0 {
		limit = -1
	}
	return l.query(ctx, `SELECT `+columns+` FROM event WHERE seq >= ? ORDER BY seq LIMIT ?`, fromSeq, limit)
}

// Head implements port.Ledger.
func (l *Ledger) Head(ctx context.Context) (port.Head, error) {
	var h port.Head
	var hash []byte
	err := l.db.QueryRowContext(ctx, `SELECT seq, hash FROM event ORDER BY seq DESC LIMIT 1`).Scan(&h.Seq, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return port.Head{}, nil
	}
	if err != nil {
		return port.Head{}, err
	}
	h.Hash = hash
	return h, nil
}

// FindOrigin implements port.OriginLedger (the team cache keeps origins).
func (l *Ledger) FindOrigin(ctx context.Context, daemonID string, localSeq int64) (int64, error) {
	var seq int64
	err := l.db.QueryRowContext(ctx, `SELECT seq FROM event WHERE json_extract(origin, '$.daemon_id') = ? AND json_extract(origin, '$.local_seq') = ? LIMIT 1`, daemonID, localSeq).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, port.ErrNotFound
	}
	return seq, err
}

// Lookup implements port.Ledger.
func (l *Ledger) Lookup(ctx context.Context, stream, idem string) (core.Envelope, error) {
	evs, err := l.query(ctx, `SELECT `+columns+` FROM event WHERE stream = ? AND idem = ?`, stream, idem)
	if err != nil {
		return core.Envelope{}, err
	}
	if len(evs) == 0 {
		return core.Envelope{}, port.ErrNotFound
	}
	return evs[0], nil
}

var (
	_ port.Ledger       = (*Ledger)(nil)
	_ port.OriginLedger = (*Ledger)(nil)
)
