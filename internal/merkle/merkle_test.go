package merkle

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// an in-memory node store driven exactly like the ledger drives it
type store map[NodeID][]byte

func (s store) fetch(id NodeID) ([]byte, error) {
	h, ok := s[id]
	if !ok {
		return nil, fmt.Errorf("missing node %v", id)
	}
	return h, nil
}

func (s store) append(i uint64, leaf []byte) {
	nodes, err := ParentsToStore(i, leaf, s.fetch)
	if err != nil {
		panic(err)
	}
	for _, n := range nodes {
		s[n.ID] = n.Hash
	}
}

// 속성: 임의 크기의 로그에서 모든 잎의 포함 증명과 모든 (a<b) 일관성 증명이 참이고, 잎을 바꾸면 거짓.
func TestProofs_Property(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for round := 0; round < 30; round++ {
		n := uint64(1 + r.IntN(40))
		s := store{}
		leaves := make([][]byte, n)
		roots := make([][]byte, n+1)
		roots[0], _ = Root(0, s.fetch)
		for i := uint64(0); i < n; i++ {
			m := sha256.Sum256([]byte(fmt.Sprintf("meta-%d-%d", round, i)))
			b := sha256.Sum256([]byte(fmt.Sprintf("body-%d-%d", round, i)))
			leaves[i] = LeafHash(m[:], b[:])
			s.append(i, leaves[i])
			var err error
			if roots[i+1], err = Root(i+1, s.fetch); err != nil {
				t.Fatal(err)
			}
		}
		for i := uint64(0); i < n; i++ {
			p, err := InclusionProof(i, n, s.fetch)
			if err != nil {
				t.Fatalf("round %d leaf %d: %v", round, i, err)
			}
			if err := VerifyInclusion(i, n, leaves[i], p, roots[n]); err != nil {
				t.Fatalf("round %d leaf %d/%d: %v", round, i, n, err)
			}
			if err := VerifyInclusion(i, n, leaves[(i+1)%n], p, roots[n]); n > 1 && err == nil {
				t.Fatalf("round %d: wrong leaf must fail", round)
			}
		}
		for a := uint64(1); a < n; a += 1 + uint64(r.IntN(3)) {
			for b := a + 1; b <= n; b += 1 + uint64(r.IntN(5)) {
				p, err := ConsistencyProof(a, b, s.fetch)
				if err != nil {
					t.Fatal(err)
				}
				if err := VerifyConsistency(a, b, p, roots[a], roots[b]); err != nil {
					t.Fatalf("round %d consistency %d→%d: %v", round, a, b, err)
				}
				if a > 1 {
					if err := VerifyConsistency(a, b, p, roots[a-1], roots[b]); err == nil {
						t.Fatalf("round %d: wrong old root must fail", round)
					}
				}
			}
		}
	}
}

func TestCheckpointAndBundle(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(crand.Reader)
	s := store{}
	m := sha256.Sum256([]byte("m"))
	b := sha256.Sum256([]byte("b"))
	leaf := LeafHash(m[:], b[:])
	s.append(0, leaf)
	m2 := sha256.Sum256([]byte("m2"))
	s.append(1, LeafHash(m2[:], b[:]))
	root, _ := Root(2, s.fetch)
	cp := Checkpoint{Org: "acme", TreeSize: 2, Root: root, At: time.Now()}
	cp.Sign(priv, "k1")
	if err := cp.Verify(pub); err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(crand.Reader)
	if err := cp.Verify(other); err == nil {
		t.Fatal("wrong key must fail")
	}
	p, _ := InclusionProof(0, 2, s.fetch)
	bundle := Bundle{Org: "acme", Seq: 1, LeafIndex: 0, LeafHash: leaf, MetaHash: m[:], BodyHash: b[:], TreeSize: 2, Proof: p, Checkpoint: cp}
	if err := VerifyBundle(bundle, pub); err != nil {
		t.Fatal(err)
	}
	bad := bundle
	bad.BodyHash = m[:]
	if err := VerifyBundle(bad, pub); err == nil {
		t.Fatal("body hash swap must fail")
	}
	tampered := bundle
	tampered.Checkpoint.TreeSize = 3
	if err := VerifyBundle(tampered, pub); err == nil {
		t.Fatal("tampered checkpoint must fail")
	}
	// consistency bundle 1 → 2
	root1, _ := Root(1, s.fetch)
	cp1 := Checkpoint{Org: "acme", TreeSize: 1, Root: root1, At: time.Now()}
	cp1.Sign(priv, "k1")
	cproof, _ := ConsistencyProof(1, 2, s.fetch)
	if err := VerifyConsistencyBundle(ConsistencyBundle{Org: "acme", From: cp1, To: cp, Proof: cproof}, pub); err != nil {
		t.Fatal(err)
	}
}
