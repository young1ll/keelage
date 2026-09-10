package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/core/supply"
)

var t0 = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, event, raw string
		handled          bool
		kind             supply.EventKind
		files            int
		errs             bool
	}{
		{"session start", "SessionStart", `{"session_id":"s1","cwd":"/r","hook_event_name":"SessionStart","source":"startup"}`, true, supply.SessionStart, 0, false},
		{"prompt", "UserPromptSubmit", `{"session_id":"s1","cwd":"/r","hook_event_name":"UserPromptSubmit","prompt":"fix it"}`, true, supply.Prompt, 0, false},
		{"pre edit", "PreToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/r/src/a.ts","old_string":"x","new_string":"y"}}`, true, supply.PreEdit, 1, false},
		{"pre write", "PreToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/r/b.ts","content":""}}`, true, supply.PreEdit, 1, false},
		{"pre notebook", "PreToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PreToolUse","tool_name":"NotebookEdit","tool_input":{"notebook_path":"/r/n.ipynb"}}`, true, supply.PreEdit, 1, false},
		{"pre bash ignored", "PreToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, false, "", 0, false},
		{"post edit", "PostToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"/r/src/a.ts"},"tool_response":{"filePath":"/r/src/a.ts","success":true}}`, true, supply.PostEdit, 1, false},
		{"post bash", "PostToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git checkout -- a.ts"}}`, true, supply.ToolResult, 0, false},
		{"post read ignored", "PostToolUse", `{"session_id":"s1","cwd":"/r","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/r/a"}}`, false, "", 0, false},
		{"session end", "SessionEnd", `{"session_id":"s1","cwd":"/r","hook_event_name":"SessionEnd","reason":"other"}`, true, supply.SessionEnd, 0, false},
		{"unknown event", "Notification", `{"session_id":"s1","hook_event_name":"Notification"}`, false, "", 0, false},
		{"mismatch", "PreToolUse", `{"session_id":"s1","hook_event_name":"PostToolUse"}`, false, "", 0, true},
		{"no session", "SessionStart", `{"cwd":"/r","hook_event_name":"SessionStart"}`, false, "", 0, true},
		{"bad json", "SessionStart", `{`, false, "", 0, true},
	}
	for _, c := range cases {
		ev, handled, err := Normalize(c.event, []byte(c.raw), t0)
		if (err != nil) != c.errs {
			t.Errorf("%s: err %v", c.name, err)
			continue
		}
		if handled != c.handled {
			t.Errorf("%s: handled %v", c.name, handled)
			continue
		}
		if handled && (ev.Kind != c.kind || len(ev.Files) != c.files || ev.Tool != Name || ev.SessionID != "s1" || !ev.At.Equal(t0)) {
			t.Errorf("%s: %+v", c.name, ev)
		}
	}
	ev, _, _ := Normalize("UserPromptSubmit", []byte(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"secret"}`), t0)
	if ev.Prompt != "secret" {
		t.Error("prompt is carried transiently")
	}
	ev, _, _ = Normalize("PostToolUse", []byte(`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git checkout -- a.ts"}}`), t0)
	if ev.Command != "git checkout -- a.ts" {
		t.Error("bash command is carried transiently")
	}
}

func TestRender(t *testing.T) {
	if b, err := Render("PreToolUse", supply.Response{}); err != nil || b != nil {
		t.Fatalf("empty: %v %s", err, b)
	}
	if b, _ := Render("SessionEnd", supply.Response{Inject: "x"}); b != nil {
		t.Fatal("SessionEnd output is discarded by Claude Code; print nothing")
	}
	b, err := Render("PreToolUse", supply.Response{Inject: "keelage · a.ts:\n- rule: no retries", Warn: []string{"anchor a is stale"}})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	hso := out["hookSpecificOutput"].(map[string]any)
	if hso["hookEventName"] != "PreToolUse" || hso["permissionDecision"] != nil || !strings.Contains(hso["additionalContext"].(string), "no retries") || !strings.Contains(hso["additionalContext"].(string), "stale") {
		t.Fatalf("inject+warn: %s", b)
	}
	if !strings.Contains(out["systemMessage"].(string), "stale") {
		t.Fatalf("warnings reach the user: %s", b)
	}
	b, _ = Render("PreToolUse", supply.Response{Block: &supply.Block{Reason: "humans only"}, Inject: "ctx"})
	_ = json.Unmarshal(b, &out)
	hso = out["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "deny" || hso["permissionDecisionReason"] != "humans only" || hso["additionalContext"] != "ctx" {
		t.Fatalf("deny: %s", b)
	}
	// a block on an event that cannot block degrades to context
	b, _ = Render("PostToolUse", supply.Response{Block: &supply.Block{Reason: "humans only"}})
	_ = json.Unmarshal(b, &out)
	hso = out["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != nil || !strings.Contains(hso["additionalContext"].(string), "humans only") {
		t.Fatalf("degrade: %s", b)
	}
	if !strings.Contains(HooksSettings(), `"PreToolUse"`) || !strings.Contains(Skill(), "what_touches") || !strings.Contains(Version(), "checked:") {
		t.Fatal("embedded artifacts")
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(HooksSettings()), &settings); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
}
