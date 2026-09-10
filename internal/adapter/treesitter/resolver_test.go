package treesitter

import (
	"context"
	"strings"
	"testing"

	"github.com/young1ll/keelage/internal/core/realization"
)

func TestResolver_Symbols(t *testing.T) {
	ctx := context.Background()
	r := NewResolver(runtime(t))
	defer r.Close(ctx)
	if !r.Supports("a.ts") || !r.Supports("b.tsx") || r.Supports("c.go") || r.Supports("d.js") {
		t.Fatal("supported extensions")
	}
	syn, matches, err := r.Parse(ctx, "src/calc.ts", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	syms := realization.ExtractSymbols(syn, matches)
	var names []string
	byName := map[string]realization.Symbol{}
	for _, s := range syms {
		names = append(names, s.Name)
		byName[s.Name] = s
	}
	if got := strings.Join(names, ","); got != "add,Calc,Calc.add,mul,Shape,Shape.area,Pair,Color" {
		t.Fatalf("symbols: %s", got)
	}
	add := byName["add"]
	if !add.Exported || add.Signature != "export function add(a:number,b:number):number" {
		t.Fatalf("add: %+v", add)
	}
	if !strings.Contains(add.Body, "return_statement") || strings.Contains(add.Body, "trailing") {
		t.Fatalf("body must keep structure and drop comments: %s", add.Body)
	}
	mul := byName["mul"]
	if !mul.Exported || mul.Signature != "export const mul=(a:number,b:number):number=>" || mul.Kind != "lexical_declaration" {
		t.Fatalf("mul: %+v", mul)
	}
	if byName["Calc.add"].Signature != "add(n:number):this" || byName["Calc"].Exported {
		t.Fatalf("method: %+v", byName["Calc.add"])
	}
	if byName["Pair"].Signature != "type Pair<T>=" || byName["Color"].Signature != "enum Color" {
		t.Fatalf("type/enum: %+v %+v", byName["Pair"], byName["Color"])
	}
	// hashes react to the right layer
	file := realization.FileHash(syn.Source)
	h1 := realization.Hash3Of(add, file)
	src2 := strings.Replace(sample, "return a + b; // trailing", "return b + a;", 1)
	syn2, m2, _ := r.Parse(ctx, "src/calc.ts", []byte(src2))
	add2, _ := realization.FindSymbol(realization.ExtractSymbols(syn2, m2), "add")
	h2 := realization.Hash3Of(add2, realization.FileHash(syn2.Source))
	if h1.Cmp(h2).String() != "body" {
		t.Fatalf("body change: %s", h1.Cmp(h2))
	}
	src3 := strings.Replace(sample, "add(a: number, b: number): number", "add(a: number, b: number): string", 1)
	syn3, m3, _ := r.Parse(ctx, "src/calc.ts", []byte(src3))
	add3, _ := realization.FindSymbol(realization.ExtractSymbols(syn3, m3), "add")
	if h1.Cmp(realization.Hash3Of(add3, "x")).String() != "signature" {
		t.Fatal("return type change must be a signature change")
	}
	src4 := strings.Replace(sample, "// header comment", "// new header", 1)
	syn4, m4, _ := r.Parse(ctx, "src/calc.ts", []byte(src4))
	add4, _ := realization.FindSymbol(realization.ExtractSymbols(syn4, m4), "add")
	if h1.Cmp(realization.Hash3Of(add4, realization.FileHash(syn4.Source))).String() != "file" {
		t.Fatal("a comment elsewhere is a file-only change")
	}
	src5 := strings.Replace(sample, "add(a: number, b: number): number {", "add( a : number , b : number ) : number {", 1)
	syn5, m5, _ := r.Parse(ctx, "src/calc.ts", []byte(src5))
	add5, _ := realization.FindSymbol(realization.ExtractSymbols(syn5, m5), "add")
	if h1.Cmp(realization.Hash3Of(add5, realization.FileHash(syn5.Source))).String() != "file" {
		t.Fatal("whitespace in the header is a file-only change")
	}
	// range → symbol
	idx := strings.Index(sample, "this.total += n")
	if s, ok := realization.SmallestContaining(syms, uint32(idx), uint32(idx+5)); !ok || s.Name != "Calc.add" {
		t.Fatalf("containing: %+v %v", s, ok)
	}
}
