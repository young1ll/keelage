package accountability

import (
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// TurnClass mirrors supply.TurnClass without importing it (contexts share
// only value objects).
type TurnClass string

const (
	TurnAccept   TurnClass = "accept"
	TurnRedirect TurnClass = "redirect"
	TurnRevert   TurnClass = "revert"
)

// Turn is one classified turn: what was touched and how the human reacted.
// No prompt text, ever (spec §8): Signal names the rule that fired.
type Turn struct {
	Seq    int       `json:"seq"`
	Class  TurnClass `json:"class"`
	Files  []string  `json:"files,omitempty"` // repo-relative
	Signal string    `json:"signal,omitempty"`
	At     time.Time `json:"at"`
}

// SessionJudgment is a judgment candidate extracted from a redirect or
// revert turn (spec §6): it carries the touched files, the class and the
// signal — the reason is added by a human later, if at all.
type SessionJudgment struct {
	Turn   int       `json:"turn"`
	Class  TurnClass `json:"class"`
	Files  []string  `json:"files,omitempty"`
	Signal string    `json:"signal,omitempty"`
	At     time.Time `json:"at"`
}

// SessionState is open | ended | shared.
type SessionState string

const (
	SessionStateNone   SessionState = ""
	SessionStateOpen   SessionState = "open"
	SessionStateEnded  SessionState = "ended"
	SessionStateShared SessionState = "shared"
)

// Session is the personal-layer record of one tool session (spec §3.4):
// tool, actors, touched files, classified turns, judgment candidates and a
// summary — never the transcript.
type Session struct {
	ID        string            `json:"id"`
	Tool      string            `json:"tool"`
	Repo      string            `json:"repo,omitempty"`
	Agent     core.ActorRef     `json:"agent"`
	Owner     core.ID           `json:"owner"`
	State     SessionState      `json:"state"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Turns     []Turn            `json:"turns,omitempty"`
	Touched   []string          `json:"touched,omitempty"`
	Judgments []SessionJudgment `json:"judgments,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Shared    []string          `json:"shared,omitempty"` // parts shared upstream: summary|judgments
}

// SessionStream is the ledger stream for a session.
func SessionStream(tool, id string) string { return "session/" + tool + "/" + id }

// SessionCmd is the identity part of every session command (embed it).
type SessionCmd struct {
	Tool string
	ID   string
	Idem string
}

func (c SessionCmd) Stream() string         { return SessionStream(c.Tool, c.ID) }
func (c SessionCmd) IdempotencyKey() string { return c.Idem }

// StartSession opens a session.
type StartSession struct {
	SessionCmd
	Repo  string
	Agent core.ActorRef
}

func (StartSession) Kind() string { return "StartSession" }

// RecordTurn appends a classified turn.
type RecordTurn struct {
	SessionCmd
	Class  TurnClass
	Files  []string
	Signal string
}

func (RecordTurn) Kind() string { return "RecordTurn" }

// EndSession closes a session; Summary may be empty (the fallback summary
// is derived: tool, turns, files, judgments).
type EndSession struct {
	SessionCmd
	Reason  string
	Summary string
}

func (EndSession) Kind() string { return "EndSession" }

// ShareSession marks which parts the user chose to share upstream.
type ShareSession struct {
	SessionCmd
	Parts []string // summary | judgments
}

func (ShareSession) Kind() string { return "ShareSession" }

// SessionStarted opens the stream.
type SessionStarted struct {
	ID    string        `json:"id"`
	Tool  string        `json:"tool"`
	Repo  string        `json:"repo,omitempty"`
	Agent core.ActorRef `json:"agent"`
	At    time.Time     `json:"at"`
}

func (SessionStarted) Kind() string { return "SessionStarted" }
func (SessionStarted) Version() int { return 1 }

// TurnRecorded is one classified turn.
type TurnRecorded struct {
	ID   string `json:"id"`
	Turn Turn   `json:"turn"`
}

func (TurnRecorded) Kind() string { return "TurnRecorded" }
func (TurnRecorded) Version() int { return 1 }

// SessionEnded closes the session with its derived summary.
type SessionEnded struct {
	ID      string    `json:"id"`
	Reason  string    `json:"reason,omitempty"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

func (SessionEnded) Kind() string { return "SessionEnded" }
func (SessionEnded) Version() int { return 1 }

// SessionShared records the user's sharing choice.
type SessionShared struct {
	ID    string    `json:"id"`
	Parts []string  `json:"parts"`
	At    time.Time `json:"at"`
}

func (SessionShared) Kind() string { return "SessionShared" }
func (SessionShared) Version() int { return 1 }

// Decide applies the session rules: turns only while open, judgment
// candidates only from redirect/revert, sharing only the chosen parts.
func (s Session) Decide(cmd core.Command, ctx core.DecideContext) ([]core.Event, error) {
	switch m := cmd.(type) {
	case StartSession:
		if s.State != SessionStateNone {
			return nil, nil // idempotent: the tool may fire SessionStart on resume
		}
		if m.ID == "" || m.Tool == "" {
			return nil, core.Reject("invalid", "session id and tool required")
		}
		if err := m.Agent.Validate(); err != nil || !m.Agent.IsAgent() {
			return nil, core.Reject("invalid", "a session is run by an agent actor with an owner")
		}
		return []core.Event{SessionStarted{ID: m.ID, Tool: m.Tool, Repo: m.Repo, Agent: m.Agent, At: ctx.Now}}, nil
	case RecordTurn:
		if s.State != SessionStateOpen {
			return nil, core.Reject("bad-state", "session is %q", s.State)
		}
		switch m.Class {
		case TurnAccept, TurnRedirect, TurnRevert:
		default:
			return nil, core.Reject("invalid", "unknown turn class %q", m.Class)
		}
		return []core.Event{TurnRecorded{ID: s.ID, Turn: Turn{Seq: len(s.Turns) + 1, Class: m.Class, Files: m.Files, Signal: m.Signal, At: ctx.Now}}}, nil
	case EndSession:
		if s.State == SessionStateNone {
			return nil, core.Reject("not-found", "session does not exist")
		}
		if s.State != SessionStateOpen {
			return nil, nil
		}
		summary := m.Summary
		if summary == "" {
			summary = s.fallbackSummary()
		}
		return []core.Event{SessionEnded{ID: s.ID, Reason: m.Reason, Summary: summary, At: ctx.Now}}, nil
	case ShareSession:
		if s.State != SessionStateEnded && s.State != SessionStateShared {
			return nil, core.Reject("bad-state", "share an ended session")
		}
		if !ctx.Actor.IsHuman() {
			return nil, core.Reject("human-only", "sharing is the user's choice")
		}
		for _, p := range m.Parts {
			if p != "summary" && p != "judgments" {
				return nil, core.Reject("invalid", "unknown part %q", p)
			}
		}
		if len(m.Parts) == 0 {
			return nil, core.Reject("invalid", "nothing to share")
		}
		return []core.Event{SessionShared{ID: s.ID, Parts: m.Parts, At: ctx.Now}}, nil
	}
	return nil, core.Reject("unknown-command", "session: %s", cmd.Kind())
}

// fallbackSummary is the deterministic summary (spec §5 fallback: title
// only): counts, no content.
func (s Session) fallbackSummary() string {
	return s.Tool + " session: " + itoa(len(s.Turns)) + " turns, " + itoa(len(s.Touched)) + " files, " + itoa(len(s.Judgments)) + " judgment candidates"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Apply folds an event.
func (s Session) Apply(e core.Event) Session {
	switch v := e.(type) {
	case SessionStarted:
		s = Session{ID: v.ID, Tool: v.Tool, Repo: v.Repo, Agent: v.Agent, Owner: v.Agent.Owner, State: SessionStateOpen, StartedAt: v.At}
	case TurnRecorded:
		s.Turns = append(s.Turns, v.Turn)
		for _, f := range v.Turn.Files {
			if !containsStr(s.Touched, f) {
				s.Touched = append(s.Touched, f)
			}
		}
		if v.Turn.Class == TurnRedirect || v.Turn.Class == TurnRevert {
			s.Judgments = append(s.Judgments, SessionJudgment{Turn: v.Turn.Seq, Class: v.Turn.Class, Files: v.Turn.Files, Signal: v.Turn.Signal, At: v.Turn.At})
		}
	case SessionEnded:
		s.State, s.EndedAt, s.Reason, s.Summary = SessionStateEnded, v.At, v.Reason, v.Summary
	case SessionShared:
		s.State, s.Shared = SessionStateShared, v.Parts
	}
	return s
}

func containsStr(list []string, x string) bool {
	for _, s := range list {
		if s == x {
			return true
		}
	}
	return false
}
