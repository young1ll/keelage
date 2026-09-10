package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeAndRemoveHooks(t *testing.T) {
	original := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(git *)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/lint.sh"}]}
    ]
  }
}
`
	merged, changed, err := MergeHooks([]byte(original))
	if err != nil || !changed {
		t.Fatalf("merge: %v %v", err, changed)
	}
	var doc map[string]any
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "opus" || doc["permissions"] == nil {
		t.Fatal("other keys must survive")
	}
	hooks := doc["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 || !strings.Contains(string(merged), "/usr/local/bin/lint.sh") {
		t.Fatalf("existing PreToolUse handler must be kept: %s", merged)
	}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "SessionEnd"} {
		if hooks[ev] == nil {
			t.Errorf("%s missing", ev)
		}
	}
	if !HasHooks(merged) || HasHooks([]byte(original)) {
		t.Fatal("HasHooks")
	}
	// idempotent
	again, changed, _ := MergeHooks(merged)
	if changed || string(again) != string(merged) {
		t.Fatal("second merge must not change anything")
	}
	// remove restores the original semantics (same JSON value)
	restored, changed, err := RemoveHooks(merged)
	if err != nil || !changed {
		t.Fatalf("remove: %v %v", err, changed)
	}
	var a, b any
	_ = json.Unmarshal([]byte(original), &a)
	_ = json.Unmarshal(restored, &b)
	if !jsonEqual(a, b) {
		t.Fatalf("remove must restore the original document:\n%s", restored)
	}
	if _, changed, _ := RemoveHooks(restored); changed {
		t.Fatal("nothing left to remove")
	}
	// empty / missing file
	m, _, err := MergeHooks(nil)
	if err != nil || !HasHooks(m) {
		t.Fatalf("merge into empty: %v", err)
	}
	r, _, _ := RemoveHooks(m)
	if strings.TrimSpace(string(r)) != "{}" {
		t.Fatalf("remove from ours-only: %q", r)
	}
	if _, _, err := MergeHooks([]byte("[1,2]")); err == nil {
		t.Fatal("non-object settings must error")
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
