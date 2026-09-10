// Package merkle is keelage's transparency-log toolkit (ADR 0004): rfc6962
// hashing over ledger records, inclusion/consistency proofs, signed
// checkpoints, and offline verification shared by the server, the ledger
// adapter and the `verify-proof` CLI.
package merkle

import (
	"crypto"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/transparency-dev/merkle/compact"
	"github.com/transparency-dev/merkle/proof"
	"github.com/transparency-dev/merkle/rfc6962"
)

// Hasher is the rfc6962 SHA-256 hasher.
var Hasher = rfc6962.New(crypto.SHA256)

// LeafHash is the tree leaf for a record: H(0x00 || meta_hash || body_hash).
// The body can be withheld and the proof still holds (spec §3.7).
func LeafHash(metaHash, bodyHash []byte) []byte {
	data := make([]byte, 0, len(metaHash)+len(bodyHash))
	data = append(data, metaHash...)
	data = append(data, bodyHash...)
	return Hasher.HashLeaf(data)
}

// NodeID re-exports compact.NodeID.
type NodeID = compact.NodeID

// NewNodeID re-exports compact.NewNodeID.
func NewNodeID(level uint, index uint64) NodeID { return compact.NewNodeID(level, index) }

// HashChildren re-exports the interior node hash.
func HashChildren(l, r []byte) []byte { return Hasher.HashChildren(l, r) }

// ParentsToStore lists the perfect-subtree nodes completed by appending the
// leaf at index (level 0 is the leaf itself). fetch returns a stored node's
// hash. The caller stores every returned node.
func ParentsToStore(index uint64, leaf []byte, fetch func(NodeID) ([]byte, error)) ([]struct {
	ID   NodeID
	Hash []byte
}, error) {
	type node = struct {
		ID   NodeID
		Hash []byte
	}
	out := []node{{NewNodeID(0, index), leaf}}
	cur, hash := NewNodeID(0, index), leaf
	for cur.Index%2 == 1 {
		left, err := fetch(NewNodeID(cur.Level, cur.Index-1))
		if err != nil {
			return nil, err
		}
		hash = HashChildren(left, hash)
		cur = cur.Parent()
		out = append(out, node{cur, hash})
	}
	return out, nil
}

// Root computes the root of the first size leaves from stored perfect
// subtrees.
func Root(size uint64, fetch func(NodeID) ([]byte, error)) ([]byte, error) {
	if size == 0 {
		return Hasher.EmptyRoot(), nil
	}
	ids := compact.RangeNodes(0, size, nil)
	hashes := make([][]byte, 0, len(ids))
	for _, id := range ids {
		h, err := fetch(id)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}
	rf := compact.RangeFactory{Hash: HashChildren}
	r, err := rf.NewRange(0, size, hashes)
	if err != nil {
		return nil, err
	}
	return r.GetRootHash(nil)
}

func fetchAll(n proof.Nodes, fetch func(NodeID) ([]byte, error)) ([][]byte, error) {
	hashes := make([][]byte, 0, len(n.IDs))
	for _, id := range n.IDs {
		h, err := fetch(id)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}
	return n.Rehash(hashes, HashChildren)
}

// InclusionProof builds the proof for leaf index in a tree of size.
func InclusionProof(index, size uint64, fetch func(NodeID) ([]byte, error)) ([][]byte, error) {
	n, err := proof.Inclusion(index, size)
	if err != nil {
		return nil, err
	}
	return fetchAll(n, fetch)
}

// ConsistencyProof builds the proof between tree sizes.
func ConsistencyProof(size1, size2 uint64, fetch func(NodeID) ([]byte, error)) ([][]byte, error) {
	n, err := proof.Consistency(size1, size2)
	if err != nil {
		return nil, err
	}
	return fetchAll(n, fetch)
}

// VerifyInclusion checks a leaf against a root.
func VerifyInclusion(index, size uint64, leaf []byte, p [][]byte, root []byte) error {
	return proof.VerifyInclusion(Hasher, index, size, leaf, p, root)
}

// VerifyConsistency checks that root2 extends root1.
func VerifyConsistency(size1, size2 uint64, p [][]byte, root1, root2 []byte) error {
	return proof.VerifyConsistency(Hasher, size1, size2, p, root1, root2)
}

// Checkpoint is a signed tree head (rfc6962 root at a size) for an org.
type Checkpoint struct {
	Org      string    `json:"org"`
	TreeSize uint64    `json:"tree_size"`
	Root     []byte    `json:"root"`
	At       time.Time `json:"at"`
	Sig      []byte    `json:"sig"`
	KeyID    string    `json:"key_id"`
}

// Canonical is the signed text: one line per field, hex root, RFC3339 UTC.
func (c Checkpoint) Canonical() []byte {
	return []byte("keelage-checkpoint/v1\n" + c.Org + "\n" + strconv.FormatUint(c.TreeSize, 10) + "\n" + hex.EncodeToString(c.Root) + "\n" + c.At.UTC().Format(time.RFC3339) + "\n")
}

// Sign signs the checkpoint with the server key.
func (c *Checkpoint) Sign(priv ed25519.PrivateKey, keyID string) {
	c.At = c.At.UTC().Truncate(time.Second)
	c.KeyID = keyID
	c.Sig = ed25519.Sign(priv, c.Canonical())
}

// Verify checks the signature with the server's public key.
func (c Checkpoint) Verify(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, c.Canonical(), c.Sig) {
		return errors.New("checkpoint: signature does not verify")
	}
	return nil
}

// Bundle is what the server hands out for one record: everything an
// auditor needs to verify offline against the server's public key.
type Bundle struct {
	Org        string     `json:"org"`
	Seq        int64      `json:"seq"` // 1-based server seq
	LeafIndex  uint64     `json:"leaf_index"`
	LeafHash   []byte     `json:"leaf_hash"`
	MetaHash   []byte     `json:"meta_hash"`
	BodyHash   []byte     `json:"body_hash"`
	TreeSize   uint64     `json:"tree_size"`
	Proof      [][]byte   `json:"proof"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

// VerifyBundle checks the checkpoint signature, that the leaf hash matches
// its meta/body hashes, and the inclusion proof against the checkpoint root.
func VerifyBundle(b Bundle, pub ed25519.PublicKey) error {
	if err := b.Checkpoint.Verify(pub); err != nil {
		return err
	}
	if b.Checkpoint.TreeSize != b.TreeSize || b.Checkpoint.Org != b.Org {
		return fmt.Errorf("bundle: checkpoint is for %s@%d, proof for %s@%d", b.Checkpoint.Org, b.Checkpoint.TreeSize, b.Org, b.TreeSize)
	}
	if want := LeafHash(b.MetaHash, b.BodyHash); !equal(want, b.LeafHash) {
		return errors.New("bundle: leaf hash does not match meta/body hashes")
	}
	return VerifyInclusion(b.LeafIndex, b.TreeSize, b.LeafHash, b.Proof, b.Checkpoint.Root)
}

// ConsistencyBundle proves that a later checkpoint extends an earlier one.
type ConsistencyBundle struct {
	Org   string     `json:"org"`
	From  Checkpoint `json:"from"`
	To    Checkpoint `json:"to"`
	Proof [][]byte   `json:"proof"`
}

// VerifyConsistencyBundle checks both signatures and the proof.
func VerifyConsistencyBundle(b ConsistencyBundle, pub ed25519.PublicKey) error {
	if err := b.From.Verify(pub); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := b.To.Verify(pub); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	if b.From.Org != b.Org || b.To.Org != b.Org {
		return errors.New("bundle: org mismatch")
	}
	return VerifyConsistency(b.From.TreeSize, b.To.TreeSize, b.Proof, b.From.Root, b.To.Root)
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HexList renders proofs for humans.
func HexList(hs [][]byte) string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = hex.EncodeToString(h)
	}
	return strings.Join(out, ",")
}
