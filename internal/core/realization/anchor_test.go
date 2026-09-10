package realization

import (
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

var (
	t0    = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	human = core.ActorRef{Kind: core.ActorHuman, ID: "alice"}
	base  = core.Hash3{Signature: "s1", Body: "b1", File: "f1"}
)

func dctx() core.DecideContext {
	return core.DecideContext{Now: t0, Actor: human, Policy: core.DefaultPolicy()}
}

func code(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	r, ok := core.AsRejection(err)
	if !ok {
		t.Fatalf("not a rejection: %v", err)
	}
	return r.Code
}

func recorded(t *testing.T) Anchor {
	t.Helper()
	evs, err := (Anchor{}).Decide(RecordAnchor{AnchorCmd{"code://src/a.ts#f", ""}, base, "c1"}, dctx())
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(Anchor{}, evs)
}

func TestAnchor_Record(t *testing.T) {
	if _, err := (Anchor{}).Decide(RecordAnchor{AnchorCmd{"nope", ""}, base, ""}, dctx()); code(t, err) != "invalid" {
		t.Errorf("bad anchor: %v", err)
	}
	if _, err := (Anchor{}).Decide(RecordAnchor{AnchorCmd{"code://a.ts", ""}, core.Hash3{}, ""}, dctx()); code(t, err) != "invalid" {
		t.Errorf("empty hash: %v", err)
	}
	a := recorded(t)
	if a.State != StateRecorded || a.Hash != base || a.Ref != "c1" || a.Key != "code://src/a.ts#f" {
		t.Fatalf("%+v", a)
	}
	if evs, err := a.Decide(RecordAnchor{AnchorCmd{a.Key, ""}, base, "c2"}, dctx()); err != nil || len(evs) != 0 {
		t.Errorf("same baseline is a no-op: %v %d", err, len(evs))
	}
	if _, err := (Anchor{}).Decide(VerifyAnchor{AnchorCmd{"code://a.ts", ""}, base, true, ""}, dctx()); code(t, err) != "not-found" {
		t.Errorf("verify unrecorded: %v", err)
	}
}

// 2단계: 파일만 → verified, 본문 → review, 시그니처 → stale(무조건), 소실 → unrealized.
func TestAnchor_VerifyStaging(t *testing.T) {
	cases := []struct {
		name    string
		current core.Hash3
		exists  bool
		state   AnchorState
		event   string
	}{
		{"identical", base, true, StateVerified, "AnchorVerified"},
		{"file only", core.Hash3{Signature: "s1", Body: "b1", File: "f2"}, true, StateVerified, "AnchorVerified"},
		{"body", core.Hash3{Signature: "s1", Body: "b2", File: "f2"}, true, StateReview, "AnchorStaled"},
		{"signature", core.Hash3{Signature: "s2", Body: "b1", File: "f1"}, true, StateStale, "AnchorStaled"},
		{"gone", core.Hash3{}, false, StateUnrealized, "AnchorStaled"},
	}
	for _, c := range cases {
		a := recorded(t)
		evs, err := a.Decide(VerifyAnchor{AnchorCmd{a.Key, ""}, c.current, c.exists, "c2"}, dctx())
		if err != nil || len(evs) != 1 || evs[0].Kind() != c.event {
			t.Errorf("%s: %v %v", c.name, err, evs)
			continue
		}
		after := core.Replay(a, evs)
		if after.State != c.state {
			t.Errorf("%s: state %s want %s", c.name, after.State, c.state)
		}
		if c.state == StateVerified && after.Hash != c.current {
			t.Errorf("%s: verified anchors track the file layer", c.name)
		}
		if c.state != StateVerified && after.Hash != base {
			t.Errorf("%s: staled anchors keep the baseline until re-recorded", c.name)
		}
	}
	// stale does not downgrade to review on a later body-only diff
	a := recorded(t)
	evs, _ := a.Decide(VerifyAnchor{AnchorCmd{a.Key, ""}, core.Hash3{Signature: "s2", Body: "b1", File: "f1"}, true, ""}, dctx())
	stale := core.Replay(a, evs)
	if evs, err := stale.Decide(VerifyAnchor{AnchorCmd{a.Key, ""}, core.Hash3{Signature: "s1", Body: "b9", File: "f1"}, true, ""}, dctx()); err != nil || len(evs) != 0 {
		t.Errorf("stale stays stale: %v %d", err, len(evs))
	}
	// re-baseline after a human accepts
	evs, err := stale.Decide(RecordAnchor{AnchorCmd{a.Key, ""}, core.Hash3{Signature: "s2", Body: "b1", File: "f1"}, "c3"}, dctx())
	if err != nil || core.Replay(stale, evs).State != StateRecorded {
		t.Errorf("re-record: %v", err)
	}
	if got := core.Replay(stale, evs); got.Staled != 1 {
		t.Errorf("counters survive re-record: %+v", got)
	}
}

func TestAnchor_Move(t *testing.T) {
	a := recorded(t)
	if _, err := a.Decide(MoveAnchor{AnchorCmd{a.Key, ""}, a.Key, base, ""}, dctx()); code(t, err) != "invalid" {
		t.Errorf("self move: %v", err)
	}
	if _, err := a.Decide(MoveAnchor{AnchorCmd{a.Key, ""}, "code://b.ts#f", core.Hash3{Signature: "s9", Body: "b1", File: "f1"}, ""}, dctx()); code(t, err) != "not-a-move" {
		t.Errorf("different signature: %v", err)
	}
	evs, err := a.Decide(MoveAnchor{AnchorCmd{a.Key, ""}, "code://b.ts#f", core.Hash3{Signature: "s1", Body: "b7", File: "f7"}, "c2"}, dctx())
	if err != nil {
		t.Fatal(err)
	}
	if core.Replay(a, evs).MovedTo != "code://b.ts#f" {
		t.Error("moved_to")
	}
	if _, err := (Anchor{}).Decide(MoveAnchor{AnchorCmd{"code://a.ts", ""}, "code://b.ts", base, ""}, dctx()); code(t, err) != "not-found" {
		t.Errorf("move unrecorded: %v", err)
	}
}
