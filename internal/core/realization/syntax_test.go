package realization

import (
	"reflect"
	"strings"
	"testing"

	"github.com/young1ll/keelage/internal/core"
)

// A hand-built tree for `export function f(a) { return a; }` and a class
// with a method, enough to exercise qualification, export detection, the
// signature/body split and comment removal.
func fixture() (Syntax, []SymbolMatch) {
	src := "export function f(a) { /*c*/ return a; }\nclass C { m() { return 1; } }"
	s := Syntax{Source: []byte(src)}
	add := func(typ, field string, start, end int, named bool, parent int) int {
		s.Nodes = append(s.Nodes, SyntaxNode{Type: typ, Field: field, Start: uint32(start), End: uint32(end), Named: named, Parent: parent, Comment: typ == "comment"})
		i := len(s.Nodes) - 1
		if parent >= 0 {
			s.Nodes[parent].Children = append(s.Nodes[parent].Children, i)
		}
		return i
	}
	prog := add("program", "", 0, len(src), true, -1)
	exp := add("export_statement", "", 0, 40, true, prog)
	fn := add("function_declaration", "declaration", 7, 40, true, exp)
	add("function", "", 7, 15, false, fn)
	fname := add("identifier", "name", 16, 17, true, fn)
	params := add("formal_parameters", "parameters", 17, 20, true, fn)
	add("identifier", "", 18, 19, true, params)
	body := add("statement_block", "body", 21, 40, true, fn)
	add("{", "", 21, 22, false, body)
	add("comment", "", 23, 28, true, body)
	ret := add("return_statement", "", 29, 38, true, body)
	add("return", "", 29, 35, false, ret)
	add("identifier", "", 36, 37, true, ret)
	add("}", "", 39, 40, false, body)
	cls := add("class_declaration", "", 41, 70, true, prog)
	cname := add("type_identifier", "name", 47, 48, true, cls)
	cbody := add("class_body", "body", 49, 70, true, cls)
	meth := add("method_definition", "", 51, 68, true, cbody)
	mname := add("property_identifier", "name", 51, 52, true, meth)
	mbody := add("statement_block", "body", 55, 68, true, meth)
	add("return_statement", "", 57, 66, true, mbody)
	matches := []SymbolMatch{
		{Captures: []Capture{{"symbol", meth}, {"name", mname}, {"body", mbody}}},
		{Captures: []Capture{{"symbol", fn}, {"name", fname}, {"body", body}}},
		{Captures: []Capture{{"symbol", cls}, {"name", cname}, {"body", cbody}}},
		{Captures: []Capture{{"symbol", cls}, {"name", cname}}}, // duplicate symbol node: ignored
		{Captures: []Capture{{"name", cname}}},                  // no symbol: ignored
	}
	return s, matches
}

func TestExtractSymbols(t *testing.T) {
	s, matches := fixture()
	syms := ExtractSymbols(s, matches)
	if len(syms) != 3 {
		t.Fatalf("got %d symbols: %+v", len(syms), syms)
	}
	names := []string{syms[0].Name, syms[1].Name, syms[2].Name}
	if !reflect.DeepEqual(names, []string{"f", "C", "C.m"}) {
		t.Fatalf("names %v", names)
	}
	f := syms[0]
	if !f.Exported || f.Kind != "function_declaration" || f.Signature != "export function f(a)" {
		t.Fatalf("f: %+v", f)
	}
	if f.Body != "(statement_block { (return_statement return identifier=a) })" {
		t.Fatalf("body sexp: %s", f.Body)
	}
	if syms[1].Exported || syms[1].Signature != "class C" {
		t.Fatalf("C: %+v", syms[1])
	}
	if syms[2].Signature != "m()" {
		t.Fatalf("C.m: %+v", syms[2])
	}
	// hashes: a comment edit (same length, so the fixture offsets hold) is file-only
	h := Hash3Of(f, FileHash(s.Source))
	s2, m2 := fixture()
	s2.Source = []byte(strings.Replace(string(s2.Source), "/*c*/", "/*d*/", 1))
	syms2 := ExtractSymbols(s2, m2)
	h2 := Hash3Of(syms2[0], FileHash(s2.Source))
	if h2.Signature != h.Signature || h2.Body != h.Body {
		t.Fatalf("comment must not change signature/body:\n%+v\n%+v", h, h2)
	}
	if h2.File == h.File {
		t.Fatal("file hash must change")
	}
	if h.Cmp(h2) != core.HashFile {
		t.Fatalf("cmp %s", h.Cmp(h2))
	}
}

func TestNormalizeSignature(t *testing.T) {
	cases := map[string]string{
		"export function add(a: number, b: number): number": "export function add(a:number,b:number):number",
		"  add( a : number , b : number ) : number ":        "add(a:number,b:number):number",
		"const mul = (a: number) =>":                        "const mul=(a:number)=>",
		"type\tPair<T> =":                                   "type Pair<T>=",
		"class   C extends  Base":                           "class C extends Base",
		"async function* gen()":                             "async function*gen()",
	}
	for in, want := range cases {
		if got := normalizeSignature(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSmallestContaining(t *testing.T) {
	syms := []Symbol{{Name: "C", Start: 41, End: 70}, {Name: "C.m", Start: 51, End: 68}, {Name: "f", Start: 7, End: 40}}
	if s, ok := SmallestContaining(syms, 55, 60); !ok || s.Name != "C.m" {
		t.Fatalf("inner: %+v %v", s, ok)
	}
	if s, ok := SmallestContaining(syms, 42, 50); !ok || s.Name != "C" {
		t.Fatalf("outer: %+v %v", s, ok)
	}
	if _, ok := SmallestContaining(syms, 0, 5); ok {
		t.Fatal("outside any symbol must fall back to the file")
	}
	if _, ok := SmallestContaining(syms, 30, 45); ok {
		t.Fatal("range spanning symbols is not contained")
	}
}

func TestGenericHash3(t *testing.T) {
	h := GenericHash3([]byte("x"))
	if h.Signature != h.Body || h.Body != h.File || h.File != FileHash([]byte("x")) {
		t.Fatalf("%+v", h)
	}
	if h.Cmp(GenericHash3([]byte("y"))) != core.HashSignature {
		t.Fatal("any generic change is a signature change")
	}
}

func TestDetectMoves(t *testing.T) {
	sig := func(s string) core.Hash3 { return core.Hash3{Signature: s, Body: "b", File: "f"} }
	before := map[string]core.Hash3{
		"code://a.ts#f": sig("F"), "code://a.ts#g": sig("G"), "code://a.ts#h": sig("H"), "code://a.ts#i": sig("I"),
	}
	after := map[string]core.Hash3{
		"code://b.ts#f": sig("F"),                            // moved
		"code://a.ts#g": sig("G"),                            // unchanged
		"code://x.ts#h": sig("H"), "code://y.ts#h": sig("H"), // ambiguous: two candidates
		// i: deleted
		"code://a.ts#j": sig("J"), // new
	}
	moves := DetectMoves(before, after)
	if len(moves) != 1 || moves[0].From.String() != "code://a.ts#f" || moves[0].To.String() != "code://b.ts#f" {
		t.Fatalf("moves %+v", moves)
	}
	if len(DetectMoves(before, before)) != 0 {
		t.Fatal("no moves when nothing changed")
	}
}
