package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Hash is a SHA-256 digest, hex in JSON.
type Hash []byte

// MarshalText encodes hex.
func (h Hash) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(h)), nil }

// UnmarshalText decodes hex.
func (h *Hash) UnmarshalText(b []byte) error {
	v, err := hex.DecodeString(string(b))
	if err != nil {
		return err
	}
	*h = v
	return nil
}

func (h Hash) String() string { return hex.EncodeToString(h) }

// Equal compares digests.
func (h Hash) Equal(o Hash) bool { return bytes.Equal(h, o) }

// EventMeta is the reference part of an event: what it points at and the
// harness snapshot it was decided under. It is hashed separately from the
// body so a body can be withheld and the proof still holds (spec §3.7).
type EventMeta struct {
	Refs            []string `json:"refs,omitempty"`
	ConstraintsHash string   `json:"constraints_hash,omitempty"`
	Autonomy        Level    `json:"autonomy"`
}

// Envelope is a ledger record: one event with its ordering, actor, meta,
// body and the hash chain. Seq is assigned by the ledger and excluded from
// the hashes (the server assigns its own seq on sync); Stream/Ver are
// included.
//
//	body_hash = H(body)
//	meta_hash = H(canonical JSON of {stream, ver, kind, v, ts, actor, meta, body_hash})
//	hash      = H(prev_hash || meta_hash || body_hash)
type Envelope struct {
	Seq    int64           `json:"seq"`
	Stream string          `json:"stream"`
	Ver    int64           `json:"ver"`
	Kind   string          `json:"kind"`
	V      int             `json:"v"`
	TS     time.Time       `json:"ts"`
	Actor  ActorRef        `json:"actor"`
	Meta   EventMeta       `json:"meta"`
	Body   json.RawMessage `json:"body"`

	MetaHash Hash `json:"meta_hash"`
	BodyHash Hash `json:"body_hash"`
	PrevHash Hash `json:"prev_hash"`
	Hash     Hash `json:"hash"`
	// Sig is the signature over Hash by the appending actor's key (optional in v0).
	Sig []byte `json:"sig,omitempty"`
	// Idem is the command idempotency key, set on the first event of a
	// command; the ledger enforces UNIQUE(stream, idem).
	Idem string `json:"idem,omitempty"`
}

type sealedMeta struct {
	Stream   string    `json:"stream"`
	Ver      int64     `json:"ver"`
	Kind     string    `json:"kind"`
	V        int       `json:"v"`
	TS       string    `json:"ts"`
	Actor    ActorRef  `json:"actor"`
	Meta     EventMeta `json:"meta"`
	BodyHash Hash      `json:"body_hash"`
}

func sum(parts ...[]byte) Hash {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func (e *Envelope) metaHash() (Hash, error) {
	m := sealedMeta{
		Stream: e.Stream, Ver: e.Ver, Kind: e.Kind, V: e.V,
		TS: e.TS.UTC().Format(time.RFC3339Nano), Actor: e.Actor, Meta: e.Meta, BodyHash: e.BodyHash,
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return sum(b), nil
}

// Seal computes BodyHash, MetaHash, PrevHash and Hash given the previous
// chain hash (nil for genesis). Ledgers call it inside Append.
func (e *Envelope) Seal(prev Hash) error {
	if e.Stream == "" || e.Kind == "" || e.Ver <= 0 {
		return errors.New("envelope: stream, kind and ver are required")
	}
	e.BodyHash = sum(e.Body)
	mh, err := e.metaHash()
	if err != nil {
		return err
	}
	e.MetaHash = mh
	e.PrevHash = append(Hash(nil), prev...)
	e.Hash = sum(e.PrevHash, e.MetaHash, e.BodyHash)
	return nil
}

// Verify recomputes the hashes and checks them against the stored ones and
// the given previous hash.
func (e Envelope) Verify(prev Hash) error {
	if !e.PrevHash.Equal(prev) {
		return fmt.Errorf("envelope seq %d: prev_hash mismatch", e.Seq)
	}
	if !sum(e.Body).Equal(e.BodyHash) {
		return fmt.Errorf("envelope seq %d: body_hash mismatch", e.Seq)
	}
	mh, err := e.metaHash()
	if err != nil {
		return err
	}
	if !mh.Equal(e.MetaHash) {
		return fmt.Errorf("envelope seq %d: meta_hash mismatch", e.Seq)
	}
	if !sum(e.PrevHash, e.MetaHash, e.BodyHash).Equal(e.Hash) {
		return fmt.Errorf("envelope seq %d: hash mismatch", e.Seq)
	}
	return nil
}

// VerifyChain checks a contiguous run of envelopes in seq order, starting
// from prev (nil when the run starts at genesis). Returns the head hash.
func VerifyChain(prev Hash, evs []Envelope) (Hash, error) {
	for i := range evs {
		if i > 0 && evs[i].Seq != evs[i-1].Seq+1 {
			return nil, fmt.Errorf("envelope seq %d: gap after %d", evs[i].Seq, evs[i-1].Seq)
		}
		if err := evs[i].Verify(prev); err != nil {
			return nil, err
		}
		prev = evs[i].Hash
	}
	return prev, nil
}
