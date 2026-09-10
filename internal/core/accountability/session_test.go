package accountability

import (
	"testing"

	"github.com/young1ll/keelage/internal/core"
)

func openSession(t *testing.T) Session {
	t.Helper()
	evs, err := (Session{}).Decide(StartSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}, Repo: "r", Agent: agent}, dctx(agent, core.L0))
	if err != nil {
		t.Fatal(err)
	}
	return core.Replay(Session{}, evs)
}

func TestSession_Lifecycle(t *testing.T) {
	if _, err := (Session{}).Decide(StartSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}, Agent: human}, dctx(human, core.L0)); code(t, err) != "invalid" {
		t.Errorf("human-run session: %v", err)
	}
	s := openSession(t)
	if s.State != SessionStateOpen || s.Owner != "alice" {
		t.Fatalf("%+v", s)
	}
	if evs, err := s.Decide(StartSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}, Agent: agent}, dctx(agent, core.L0)); err != nil || len(evs) != 0 {
		t.Errorf("restart is a no-op: %v %d", err, len(evs))
	}
	if _, err := s.Decide(EndSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}}, dctx(agent, core.L0)); err != nil {
		t.Fatal(err)
	}
	turns := []RecordTurn{
		{SessionCmd{"claude-code", "s1", ""}, TurnAccept, []string{"src/a.ts"}, ""},
		{SessionCmd{"claude-code", "s1", ""}, TurnRedirect, []string{"src/a.ts", "src/b.ts"}, "ko-negation"},
		{SessionCmd{"claude-code", "s1", ""}, TurnRevert, []string{"src/b.ts"}, "git-checkout"},
	}
	for _, tr := range turns {
		evs, err := s.Decide(tr, dctx(agent, core.L0))
		if err != nil {
			t.Fatal(err)
		}
		s = core.Replay(s, evs)
	}
	if _, err := s.Decide(RecordTurn{SessionCmd{"claude-code", "s1", ""}, "meh", nil, ""}, dctx(agent, core.L0)); code(t, err) != "invalid" {
		t.Errorf("bad class: %v", err)
	}
	if len(s.Turns) != 3 || s.Turns[2].Seq != 3 || len(s.Touched) != 2 || len(s.Judgments) != 2 || s.Judgments[0].Class != TurnRedirect {
		t.Fatalf("after turns: %+v", s)
	}
	if _, err := s.Decide(ShareSession{SessionCmd{"claude-code", "s1", ""}, []string{"summary"}}, dctx(human, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("share open: %v", err)
	}
	evs, err := s.Decide(EndSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}, Reason: "other"}, dctx(agent, core.L0))
	if err != nil {
		t.Fatal(err)
	}
	s = core.Replay(s, evs)
	if s.State != SessionStateEnded || s.Summary != "claude-code session: 3 turns, 2 files, 2 judgment candidates" {
		t.Fatalf("ended: %+v", s)
	}
	if evs, err := s.Decide(EndSession{SessionCmd: SessionCmd{Tool: "claude-code", ID: "s1"}}, dctx(agent, core.L0)); err != nil || len(evs) != 0 {
		t.Errorf("end twice is a no-op: %v", err)
	}
	if _, err := s.Decide(RecordTurn{SessionCmd{"claude-code", "s1", ""}, TurnAccept, nil, ""}, dctx(agent, core.L0)); code(t, err) != "bad-state" {
		t.Errorf("turn after end: %v", err)
	}
	if _, err := s.Decide(ShareSession{SessionCmd{"claude-code", "s1", ""}, []string{"summary"}}, dctx(agent, core.L0)); code(t, err) != "human-only" {
		t.Errorf("agent shares: %v", err)
	}
	if _, err := s.Decide(ShareSession{SessionCmd{"claude-code", "s1", ""}, []string{"transcript"}}, dctx(human, core.L0)); code(t, err) != "invalid" {
		t.Errorf("transcript is never shareable: %v", err)
	}
	evs, err = s.Decide(ShareSession{SessionCmd{"claude-code", "s1", ""}, []string{"summary", "judgments"}}, dctx(human, core.L0))
	if err != nil || core.Replay(s, evs).State != SessionStateShared {
		t.Fatalf("share: %v", err)
	}
	if _, err := (Session{}).Decide(EndSession{SessionCmd: SessionCmd{Tool: "x", ID: "y"}}, dctx(agent, core.L0)); code(t, err) != "not-found" {
		t.Errorf("end unknown: %v", err)
	}
}
