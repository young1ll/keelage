// Package claudecode maps Claude Code hook events onto keelage's six
// canonical events and renders the canonical response back into Claude
// Code's hook JSON. Protocol details and the checked date live in VERSION.
package claudecode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core/supply"
)

// Name is the adapter/tool name carried in canonical events.
const Name = "claude-code"

//go:embed VERSION
var version string

//go:embed hooks.json
var hooksSettings string

//go:embed skills/keelage/SKILL.md
var skill string

// Version returns the VERSION file.
func Version() string { return version }

// HooksSettings returns the settings.json snippet that installs the hooks.
func HooksSettings() string { return hooksSettings }

// Skill returns the SKILL.md for the hookless path.
func Skill() string { return skill }

// MCPCommand returns the command that registers the local MCP server.
func MCPCommand() string {
	return "claude mcp add --transport stdio --scope user keelage -- keelage mcp"
}

// Events lists the Claude Code hook events the adapter handles.
var Events = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd"}

// rawInput is the subset of the hook stdin JSON keelage reads.
type rawInput struct {
	SessionID     string          `json:"session_id"`
	CWD           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	Prompt        string          `json:"prompt"`
	Reason        string          `json:"reason"`
	Source        string          `json:"source"`
}

type fileInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"`
}

func editedFiles(toolName string, in json.RawMessage) []string {
	var fi fileInput
	_ = json.Unmarshal(in, &fi)
	switch toolName {
	case "Edit", "Write", "MultiEdit":
		if fi.FilePath != "" {
			return []string{fi.FilePath}
		}
	case "NotebookEdit":
		if fi.NotebookPath != "" {
			return []string{fi.NotebookPath}
		}
	}
	return nil
}

// Normalize turns one hook invocation into a canonical event. handled is
// false when the event is one keelage ignores (nothing to send).
func Normalize(event string, raw []byte, now time.Time) (ev supply.Event, handled bool, err error) {
	var in rawInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return supply.Event{}, false, fmt.Errorf("claude-code: input: %w", err)
	}
	if in.HookEventName != "" && in.HookEventName != event {
		return supply.Event{}, false, fmt.Errorf("claude-code: stdin says %s, argument says %s", in.HookEventName, event)
	}
	ev = supply.Event{Tool: Name, SessionID: in.SessionID, CWD: in.CWD, At: now}
	switch event {
	case "SessionStart":
		ev.Kind = supply.SessionStart
		ev.Reason = in.Source
	case "UserPromptSubmit":
		ev.Kind = supply.Prompt
		ev.Prompt = in.Prompt
	case "PreToolUse":
		files := editedFiles(in.ToolName, in.ToolInput)
		if len(files) == 0 {
			return supply.Event{}, false, nil
		}
		ev.Kind, ev.Files, ev.ToolName = supply.PreEdit, files, in.ToolName
	case "PostToolUse":
		ev.ToolName = in.ToolName
		if files := editedFiles(in.ToolName, in.ToolInput); len(files) > 0 {
			ev.Kind, ev.Files = supply.PostEdit, files
		} else if in.ToolName == "Bash" || in.ToolName == "PowerShell" {
			var fi fileInput
			_ = json.Unmarshal(in.ToolInput, &fi)
			ev.Kind, ev.OK, ev.Command = supply.ToolResult, true, fi.Command
		} else {
			return supply.Event{}, false, nil
		}
	case "SessionEnd":
		ev.Kind = supply.SessionEnd
		ev.Reason = in.Reason
	default:
		return supply.Event{}, false, nil
	}
	if ev.SessionID == "" {
		return supply.Event{}, false, fmt.Errorf("claude-code: session_id missing")
	}
	return ev, true, nil
}

type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

type output struct {
	SystemMessage      string        `json:"systemMessage,omitempty"`
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

// Render turns the canonical response into Claude Code's stdout JSON. nil
// means print nothing (exit 0, no decision).
func Render(event string, r supply.Response) ([]byte, error) {
	if r.Empty() || event == "SessionEnd" {
		return nil, nil
	}
	ctx := r.Inject
	if len(r.Warn) > 0 {
		if ctx != "" {
			ctx += "\n"
		}
		ctx += "keelage warnings:\n- " + strings.Join(r.Warn, "\n- ")
	}
	out := output{HookSpecificOutput: &hookSpecific{HookEventName: event, AdditionalContext: ctx}}
	if len(r.Warn) > 0 {
		out.SystemMessage = "keelage: " + strings.Join(r.Warn, "; ")
	}
	if r.Block != nil {
		if event == "PreToolUse" {
			out.HookSpecificOutput.PermissionDecision = "deny"
			out.HookSpecificOutput.PermissionDecisionReason = r.Block.Reason
		} else {
			// the tool cannot block here: degrade to context (patterns §6)
			out.HookSpecificOutput.AdditionalContext = strings.TrimSpace(r.Block.Reason + "\n" + ctx)
		}
	}
	return json.Marshal(out)
}
