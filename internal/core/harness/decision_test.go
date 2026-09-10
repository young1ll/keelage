package harness

import (
	"testing"

	"github.com/young1ll/keelage/internal/core"
)

func TestDecision_Lifecycle(t *testing.T) {
	draft := DraftDecision{DecisionCmd: DecisionCmd{ID: "d1"}, Title: "0001. Unit is change", Body: "# 0001\nStatus: accepted", Scope: repo, Anchors: []string{"code://src/a.ts"}}
	if _, err := (Decision{}).Decide(DraftDecision{DecisionCmd: DecisionCmd{ID: "d1"}, Title: "x", Scope: repo}, dctx(human)); code(t, err) != "invalid" {
		t.Errorf("empty body: %v", err)
	}
	evs, err := (Decision{}).Decide(draft, dctx(human))
	if err != nil {
		t.Fatal(err)
	}
	d := core.Replay(Decision{}, evs)
	if d.State != DecisionStateGenerated || d.DecisionKind != "adr" || len(d.Anchors) != 1 {
		t.Fatalf("%+v", d)
	}
	if _, err := d.Decide(draft, dctx(human)); code(t, err) != "exists" {
		t.Errorf("draft twice: %v", err)
	}
	if _, err := d.Decide(VerifyDecision{DecisionCmd{"d1", ""}}, dctx(agent)); code(t, err) != "human-only" {
		t.Errorf("agent verify: %v", err)
	}
	evs, _ = d.Decide(VerifyDecision{DecisionCmd{"d1", ""}}, dctx(human))
	d = core.Replay(d, evs)
	if d.State != DecisionStateVerified {
		t.Fatal("verify")
	}
	if evs, err := d.Decide(ReviseDecision{DecisionCmd{"d1", ""}, d.Title, d.Body}, dctx(human)); err != nil || len(evs) != 0 {
		t.Errorf("same text is a no-op: %v", err)
	}
	evs, _ = d.Decide(ReviseDecision{DecisionCmd{"d1", ""}, d.Title, "# 0001\nStatus: superseded"}, dctx(human))
	d = core.Replay(d, evs)
	if d.State != DecisionStateGenerated {
		t.Fatal("revision needs re-verification")
	}
	if _, err := d.Decide(SupersedeDecision{DecisionCmd{"d1", ""}, "d1"}, dctx(human)); code(t, err) != "invalid" {
		t.Errorf("supersede self: %v", err)
	}
	evs, _ = d.Decide(SupersedeDecision{DecisionCmd{"d1", ""}, "d2"}, dctx(human))
	d = core.Replay(d, evs)
	if d.State != DecisionStateRetired || d.SupersededBy != "d2" {
		t.Fatalf("superseded: %+v", d)
	}
	if _, err := d.Decide(RetireDecision{DecisionCmd{"d1", ""}}, dctx(human)); code(t, err) != "bad-state" {
		t.Errorf("retire retired: %v", err)
	}
	if _, err := (Decision{}).Decide(RetireDecision{DecisionCmd{"zz", ""}}, dctx(human)); code(t, err) != "not-found" {
		t.Errorf("retire unknown: %v", err)
	}
}
