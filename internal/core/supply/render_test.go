package supply

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/young1ll/keelage/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestManagedBlock(t *testing.T) {
	user := "# My rules\n\nKeep it simple.\n"
	out := UpsertBlock(user, "context", "@.keelage/context/claude-code/context.md")
	if !strings.HasPrefix(out, user) || !strings.Contains(out, "<!-- keelage:begin context ") || !strings.HasSuffix(out, "<!-- keelage:end -->\n") {
		t.Fatalf("upsert:\n%s", out)
	}
	hash, body, ok := FindBlock(out, "context")
	if !ok || body != "@.keelage/context/claude-code/context.md" || hash != BlockHash(body) {
		t.Fatalf("find: %v %q %q", ok, hash, body)
	}
	// replace keeps the surrounding text
	out2 := UpsertBlock(out+"\n# Later\nmore\n", "context", "@other.md")
	if !strings.HasPrefix(out2, user) || !strings.HasSuffix(out2, "# Later\nmore\n") || strings.Count(out2, "keelage:begin") != 1 || !strings.Contains(out2, "@other.md") {
		t.Fatalf("replace:\n%s", out2)
	}
	// a user edit inside the block shows as a hash mismatch (diverged)
	tampered := strings.Replace(out, "@.keelage", "@.tampered", 1)
	if h, b, _ := FindBlock(tampered, "context"); h == BlockHash(b) {
		t.Fatal("tampering must change the body hash")
	}
	// remove restores the original bytes
	back, removed := RemoveBlock(out, "context")
	if !removed || back != user {
		t.Fatalf("remove: %v %q", removed, back)
	}
	if _, removed := RemoveBlock(user, "context"); removed {
		t.Fatal("nothing to remove")
	}
	if got := UpsertBlock("", "context", "x"); !strings.HasPrefix(got, "<!-- keelage:begin") {
		t.Fatalf("empty file: %q", got)
	}
	if back, _ := RemoveBlock(UpsertBlock("", "context", "x"), "context"); back != "" {
		t.Fatalf("empty round trip: %q", back)
	}
	// other blocks are untouched
	two := UpsertBlock(UpsertBlock(user, "a", "A"), "b", "B")
	one, _ := RemoveBlock(two, "a")
	if _, _, ok := FindBlock(one, "b"); !ok || strings.Contains(one, "keelage:begin a") {
		t.Fatalf("remove a keeps b:\n%s", one)
	}
}

// golden: testdata/render/claude-code/<case>/{input.json, expected/...}
func TestRenderClaudeCode_Golden(t *testing.T) {
	cases, _ := filepath.Glob("../../../testdata/render/claude-code/*")
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	for _, dir := range cases {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "input.json"))
			if err != nil {
				t.Fatal(err)
			}
			var in RenderInput
			if err := json.Unmarshal(raw, &in); err != nil {
				t.Fatal(err)
			}
			ops := RenderClaudeCode(in)
			for _, op := range ops {
				golden := filepath.Join(dir, "expected", filepath.FromSlash(op.Path))
				if op.Managed {
					golden += ".block"
				}
				if *update {
					_ = os.MkdirAll(filepath.Dir(golden), 0o755)
					if err := os.WriteFile(golden, []byte(op.Content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%s: %v (run with -update)", op.Path, err)
				}
				if string(want) != op.Content {
					t.Errorf("%s differs from golden:\n%s", op.Path, op.Content)
				}
			}
		})
	}
	// determinism
	in := RenderInput{Repo: "r", Tool: "claude-code", Constraints: []ConstraintFact{{ID: "b", Kind: "rule", State: "verified", Body: "B"}, {ID: "a", Kind: "rule", State: "verified", Body: "A", Scope: core.ScopeKey{Team: "t"}}}}
	first, second := RenderClaudeCode(in), RenderClaudeCode(in)
	if first[0].Content != second[0].Content || !strings.Contains(first[0].Content, "team t") {
		t.Fatal("render must be deterministic and label scopes")
	}
}
