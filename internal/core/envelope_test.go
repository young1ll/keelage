package core

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

func mkEnv(seq, ver int64, stream, body string) Envelope {
	return Envelope{
		Seq: seq, Stream: stream, Ver: ver, Kind: "T", V: 1,
		TS:    time.Date(2026, 9, 10, 0, 0, 0, int(seq), time.UTC),
		Actor: ActorRef{Kind: ActorHuman, ID: "u1"},
		Meta:  EventMeta{Refs: []string{"a"}, Autonomy: L1},
		Body:  json.RawMessage(body),
	}
}

// 속성: 임의 시퀀스를 봉인하면 항상 검증되고, 어느 바이트를 바꿔도 깨진다.
func TestEnvelope_ChainProperty(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for round := 0; round < 50; round++ {
		n := 1 + r.Intn(20)
		var prev Hash
		evs := make([]Envelope, 0, n)
		for i := 0; i < n; i++ {
			e := mkEnv(int64(i+1), int64(i+1), "s", `{"n":`+string(rune('0'+r.Intn(10)))+`}`)
			if err := e.Seal(prev); err != nil {
				t.Fatal(err)
			}
			prev = e.Hash
			evs = append(evs, e)
		}
		head, err := VerifyChain(nil, evs)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if !head.Equal(evs[n-1].Hash) {
			t.Fatal("head mismatch")
		}
		// tamper
		i := r.Intn(n)
		tampered := make([]Envelope, n)
		copy(tampered, evs)
		switch r.Intn(4) {
		case 0:
			tampered[i].Body = json.RawMessage(`{"n":99}`)
		case 1:
			tampered[i].Actor.ID = "mallory"
		case 2:
			tampered[i].Meta.Autonomy = L3
		case 3:
			tampered[i].TS = tampered[i].TS.Add(time.Second)
		}
		if _, err := VerifyChain(nil, tampered); err == nil {
			t.Fatalf("round %d: tampering at %d not detected", round, i)
		}
	}
}

func TestEnvelope_SeqIsNotHashed(t *testing.T) {
	a := mkEnv(1, 1, "s", `{}`)
	b := mkEnv(7, 1, "s", `{}`)
	b.TS = a.TS
	_ = a.Seal(nil)
	_ = b.Seal(nil)
	if !a.Hash.Equal(b.Hash) {
		t.Fatal("seq must not affect the hash (server assigns its own seq)")
	}
	if _, err := VerifyChain(nil, []Envelope{a, b}); err == nil {
		t.Fatal("seq gap must fail chain verification")
	}
}

func TestEnvelope_SealRequiresIdentity(t *testing.T) {
	e := Envelope{}
	if err := e.Seal(nil); err == nil {
		t.Fatal("empty envelope must not seal")
	}
}

func TestHash_JSONHex(t *testing.T) {
	e := mkEnv(1, 1, "s", `{}`)
	_ = e.Seal(nil)
	b, _ := json.Marshal(e)
	var back Envelope
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Hash.Equal(e.Hash) || !back.MetaHash.Equal(e.MetaHash) {
		t.Fatal("hash hex round trip")
	}
}
