package treesitter

import (
	"context"
	"strings"
	"testing"
)

var rt *Runtime

func runtime(t *testing.T) *Runtime {
	t.Helper()
	if rt == nil {
		r, err := NewRuntime(context.Background(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		rt = r
	}
	return rt
}

func parser(t *testing.T, lang Language) *Parser {
	t.Helper()
	p, err := runtime(t).NewParser(context.Background(), lang)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

const sample = `// header comment
export function add(a: number, b: number): number {
  return a + b; // trailing
}

class Calc {
  private total = 0;
  add(n: number): this { this.total += n; return this; }
}

export const mul = (a: number, b: number): number => a * b;
interface Shape { area(): number }
type Pair<T> = [T, T];
enum Color { Red, Green }
`

func TestParse_TypeScript(t *testing.T) {
	ctx := context.Background()
	p := parser(t, TypeScript)
	abi, err := p.ABI(ctx)
	if err != nil || abi < 13 || abi > 15 {
		t.Fatalf("abi %d %v", abi, err)
	}
	tree, err := p.Parse(ctx, []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close(ctx)
	root := tree.Nodes[tree.Root()]
	if root.Type != "program" || root.Parent != -1 || root.HasError {
		t.Fatalf("root %+v", root)
	}
	if int(root.End) != len(sample) {
		t.Fatalf("root end %d", root.End)
	}
	fn, comment := -1, -1
	for i, n := range tree.Nodes {
		if n.Type == "function_declaration" && fn < 0 {
			fn = i
		}
		if n.Type == "comment" && comment < 0 {
			comment = i
		}
	}
	if fn < 0 {
		t.Fatal("no function_declaration")
	}
	name, ok := tree.ChildByField(fn, "name")
	if !ok || tree.Text(name) != "add" {
		t.Fatalf("name field: %v %q", ok, tree.Text(name))
	}
	params, ok := tree.ChildByField(fn, "parameters")
	if !ok || !strings.HasPrefix(tree.Text(params), "(a: number") {
		t.Fatalf("parameters: %v %q", ok, tree.Text(params))
	}
	if tree.Nodes[tree.Nodes[fn].Parent].Type != "export_statement" {
		t.Fatalf("parent %s", tree.Nodes[tree.Nodes[fn].Parent].Type)
	}
	if comment < 0 || !tree.Nodes[comment].Extra || !tree.Nodes[comment].Named {
		t.Fatalf("comment %d %+v", comment, tree.Nodes[comment])
	}
	// parent/child links agree
	for i, n := range tree.Nodes {
		for _, c := range n.Children {
			if tree.Nodes[c].Parent != i {
				t.Fatalf("node %d child %d parent %d", i, c, tree.Nodes[c].Parent)
			}
			if tree.Nodes[c].Start < n.Start || tree.Nodes[c].End > n.End {
				t.Fatalf("child %d outside parent %d", c, i)
			}
		}
	}
}

func TestParse_ErrorsAndTSX(t *testing.T) {
	ctx := context.Background()
	p := parser(t, TypeScript)
	tree, err := p.Parse(ctx, []byte("function (a { return }"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close(ctx)
	if !tree.Nodes[0].HasError {
		t.Fatal("broken source must flag has_error on the root")
	}
	// JSX only parses under the tsx grammar
	jsx := []byte("export const App = () => <div className=\"x\">hi</div>;\n")
	ts, _ := p.Parse(ctx, jsx)
	defer ts.Close(ctx)
	tsx := parser(t, TSX)
	tree2, err := tsx.Parse(ctx, jsx)
	if err != nil {
		t.Fatal(err)
	}
	defer tree2.Close(ctx)
	if tree2.Nodes[0].HasError {
		t.Fatal("tsx grammar must accept JSX")
	}
	found := false
	for _, n := range tree2.Nodes {
		if n.Type == "jsx_element" {
			found = true
		}
	}
	if !found {
		t.Fatal("no jsx_element")
	}
	// empty source
	empty, err := p.Parse(ctx, nil)
	if err != nil || empty.Nodes[0].Type != "program" {
		t.Fatalf("empty: %v %+v", err, empty.Nodes)
	}
	empty.Close(ctx)
}

func TestQuery(t *testing.T) {
	ctx := context.Background()
	p := parser(t, TypeScript)
	tree, err := p.Parse(ctx, []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close(ctx)
	q, err := p.NewQuery(ctx, `
(function_declaration name: (identifier) @name body: (statement_block) @body) @symbol
(class_declaration name: (type_identifier) @name body: (class_body) @body) @symbol
(method_definition name: (property_identifier) @name body: (statement_block) @body) @symbol
`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close(ctx)
	if got := q.CaptureNames(); len(got) != 3 || got[0] != "name" || got[1] != "body" || got[2] != "symbol" {
		t.Fatalf("captures %v", got)
	}
	matches, err := q.Exec(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range matches {
		if len(m.Captures) != 3 {
			t.Fatalf("match %+v", m)
		}
		for _, c := range m.Captures {
			if c.Name == "name" {
				names = append(names, tree.Text(c.Node))
			}
			if c.Name == "symbol" && tree.Nodes[c.Node].Type != []string{"function_declaration", "class_declaration", "method_definition"}[m.Pattern] {
				t.Fatalf("pattern %d captured %s", m.Pattern, tree.Nodes[c.Node].Type)
			}
		}
	}
	if strings.Join(names, ",") != "add,Calc,add" {
		t.Fatalf("names %v", names)
	}
	if _, err := p.NewQuery(ctx, "(not_a_node) @x"); err == nil || !strings.Contains(err.Error(), "node type") {
		t.Fatalf("bad query: %v", err)
	}
	if _, err := q.Exec(ctx, tree); err != nil {
		t.Fatal("query must be reusable")
	}
}

func TestParse_Reuse(t *testing.T) {
	ctx := context.Background()
	p := parser(t, TypeScript)
	for i := 0; i < 50; i++ {
		tree, err := p.Parse(ctx, []byte(sample))
		if err != nil {
			t.Fatal(err)
		}
		tree.Close(ctx)
	}
}

func BenchmarkParse(b *testing.B) {
	ctx := context.Background()
	r, err := NewRuntime(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close(ctx) }()
	p, err := r.NewParser(ctx, TypeScript)
	if err != nil {
		b.Fatal(err)
	}
	src := []byte(strings.Repeat(sample, 20))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree, err := p.Parse(ctx, src)
		if err != nil {
			b.Fatal(err)
		}
		tree.Close(ctx)
	}
}
