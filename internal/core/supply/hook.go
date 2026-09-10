package supply

import (
	"sort"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// EventKind is one of the six canonical hook events (architecture-patterns
// §6). Tool adapters map their own events onto these and back.
type EventKind string

const (
	SessionStart EventKind = "session_start"
	Prompt       EventKind = "prompt"
	PreEdit      EventKind = "pre_edit"
	PostEdit     EventKind = "post_edit"
	ToolResult   EventKind = "tool_result"
	SessionEnd   EventKind = "session_end"
)

// Event is a canonical hook event. Prompt text is transient: it is used for
// turn classification in memory and never persisted (spec §8).
type Event struct {
	Kind      EventKind `json:"kind"`
	Tool      string    `json:"tool"` // adapter name, e.g. "claude-code"
	SessionID string    `json:"session_id"`
	CWD       string    `json:"cwd"`
	Files     []string  `json:"files,omitempty"` // absolute paths for pre_edit/post_edit
	ToolName  string    `json:"tool_name,omitempty"`
	OK        bool      `json:"ok,omitempty"`     // tool_result
	Prompt    string    `json:"prompt,omitempty"` // transient
	Reason    string    `json:"reason,omitempty"` // session_end
	At        time.Time `json:"at"`
}

// Block asks the tool to refuse the edit. Only an autonomy constraint that
// says so explicitly produces one (spec §6: L0 = warn/inject, block only
// when the autonomy constraint states it).
type Block struct {
	Reason string `json:"reason"`
}

// Response is the canonical hook answer: context to inject, warnings, and
// an optional block. Tools without block support degrade it to inject.
type Response struct {
	Inject string   `json:"inject,omitempty"`
	Warn   []string `json:"warn,omitempty"`
	Block  *Block   `json:"block,omitempty"`
}

// Empty reports whether there is nothing to say.
func (r Response) Empty() bool { return r.Inject == "" && len(r.Warn) == 0 && r.Block == nil }

// AnchorFact is what the daemon knows about one anchor in an edited file.
type AnchorFact struct {
	Key     string
	State   string // recorded|verified|review|stale|unrealized
	MovedTo string
}

// ConstraintFact is a constraint in force for the edited file.
type ConstraintFact struct {
	ID          core.ID
	Kind        string
	State       string
	Scope       core.ScopeKey
	Body        string
	Explanation string
	Anchors     []string
	Level       core.Level
	DenyEdit    bool // autonomy constraint that explicitly blocks agent edits
}

// Facts are the resolved inputs for one file of a pre_edit event.
type Facts struct {
	File        string // repo-relative
	Repo        string
	Anchors     []AnchorFact
	Constraints []ConstraintFact
	Actor       core.ActorKind
}

// DenyEditMarker is the Checkable value an autonomy constraint carries to
// block agent edits in its scope. Anything else warns or injects only.
const DenyEditMarker = "deny-edit"

// Compose renders the facts of every edited file into one Response. Pure:
// same facts, same text. The text is written as factual statements (the
// hook docs ask for that so it reads as project information, not as an
// out-of-band instruction).
func Compose(facts []Facts) Response {
	var r Response
	var b strings.Builder
	for _, f := range facts {
		if len(f.Anchors) == 0 && len(f.Constraints) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("keelage · ")
		b.WriteString(f.File)
		b.WriteString(":")
		anchors := append([]AnchorFact(nil), f.Anchors...)
		sort.Slice(anchors, func(i, j int) bool { return anchors[i].Key < anchors[j].Key })
		for _, a := range anchors {
			b.WriteString("\n- anchor ")
			b.WriteString(a.Key)
			b.WriteString(" is ")
			b.WriteString(a.State)
			if a.MovedTo != "" {
				b.WriteString(" (moved to ")
				b.WriteString(a.MovedTo)
				b.WriteString(")")
			}
			switch a.State {
			case "stale":
				r.Warn = append(r.Warn, "anchor "+a.Key+" is stale: its signature changed since the bound constraints were verified")
			case "review":
				r.Warn = append(r.Warn, "anchor "+a.Key+" is under review: its body changed since the bound constraints were verified")
			case "unrealized":
				r.Warn = append(r.Warn, "anchor "+a.Key+" no longer exists in the working tree")
			}
		}
		cs := append([]ConstraintFact(nil), f.Constraints...)
		sort.Slice(cs, func(i, j int) bool {
			wi, wj := cs[i].Scope.Width(), cs[j].Scope.Width()
			if wi != wj {
				return wi > wj
			}
			return cs[i].ID < cs[j].ID
		})
		for _, c := range cs {
			b.WriteString("\n- ")
			b.WriteString(c.Kind)
			b.WriteString(" (")
			b.WriteString(c.State)
			b.WriteString(", ")
			b.WriteString(scopeLabel(c.Scope))
			b.WriteString("): ")
			b.WriteString(strings.TrimSpace(c.Body))
			if c.Explanation != "" {
				b.WriteString(" — ")
				b.WriteString(strings.TrimSpace(c.Explanation))
			}
			if c.State == "review" {
				r.Warn = append(r.Warn, "constraint "+string(c.ID)+" is under review")
			}
			if c.DenyEdit && f.Actor == core.ActorAgent && r.Block == nil {
				r.Block = &Block{Reason: "keelage: " + strings.TrimSpace(c.Body) + " (autonomy " + c.Level.String() + " in " + scopeLabel(c.Scope) + " denies agent edits to " + f.File + "; a human edits or raises the level)"}
			}
		}
	}
	r.Inject = b.String()
	sort.Strings(r.Warn)
	return r
}

func scopeLabel(k core.ScopeKey) string {
	var parts []string
	if k.Product != "" {
		parts = append(parts, "product "+k.Product)
	}
	if k.Team != "" {
		parts = append(parts, "team "+k.Team)
	}
	if k.Repo != "" {
		if k.PathGlob != "" {
			parts = append(parts, k.Repo+":"+k.PathGlob)
		} else {
			parts = append(parts, "repo "+k.Repo)
		}
	}
	if k.Person != "" {
		parts = append(parts, "personal")
	}
	if len(parts) == 0 {
		return "global"
	}
	return strings.Join(parts, ", ")
}

// Touches is the answer to what_touches(anchor): the hookless path (spec
// §3.5b) gives agents the same facts the pre_edit hook injects.
type Touches struct {
	Anchor      string           `json:"anchor"`
	State       string           `json:"state,omitempty"`
	MovedTo     string           `json:"moved_to,omitempty"`
	Constraints []ConstraintFact `json:"constraints"`
}

// Related is the answer to related(id) for a constraint or an anchor.
type Related struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"` // constraint | anchor
	Anchors      []string `json:"anchors,omitempty"`
	Constraints  []string `json:"constraints,omitempty"`
	Supersedes   string   `json:"supersedes,omitempty"`
	SupersededBy string   `json:"superseded_by,omitempty"`
	MovedTo      string   `json:"moved_to,omitempty"`
	State        string   `json:"state,omitempty"`
}
