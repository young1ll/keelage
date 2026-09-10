package realization

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"

	"github.com/young1ll/keelage/internal/core"
)

// Syntax is a language-neutral parsed file: nodes in preorder plus the
// source bytes. Adapters (tree-sitter) produce it; this package reads it.
type Syntax struct {
	Nodes  []SyntaxNode
	Source []byte
}

// SyntaxNode is one node. Children are indices into Syntax.Nodes.
type SyntaxNode struct {
	Type     string
	Field    string
	Start    uint32
	End      uint32
	Named    bool
	Comment  bool // excluded from body hashes
	Parent   int
	Children []int
}

// Text returns the source of node i.
func (s Syntax) Text(i int) string { return string(s.Source[s.Nodes[i].Start:s.Nodes[i].End]) }

// Capture is a node matched by the language's symbols query, tagged by
// role: "symbol" (the declaration), "name", "body". One Symbol is built per
// "symbol" capture group.
type Capture struct {
	Role string
	Node int
}

// SymbolMatch groups the captures of one query match.
type SymbolMatch struct {
	Captures []Capture
}

// Symbol is a named declaration with the material for its Hash3.
type Symbol struct {
	Name      string // qualified: Outer.inner
	Kind      string // node type of the declaration
	Exported  bool
	Start     uint32
	End       uint32
	Signature string // normalized header text
	Body      string // normalized S-expression of the body subtree
	node      int
}

// ExtractSymbols builds symbols from query matches: the signature is the
// declaration text before its body (whitespace-normalized, plus the export
// flag); the body is the S-expression of the body subtree with comments
// dropped and leaf texts kept. Names are qualified by enclosing symbols.
// Pure; deterministic order (by start offset, outer first).
func ExtractSymbols(s Syntax, matches []SymbolMatch) []Symbol {
	type raw struct {
		sym, name, body int
	}
	raws := make([]raw, 0, len(matches))
	for _, m := range matches {
		r := raw{sym: -1, name: -1, body: -1}
		for _, c := range m.Captures {
			switch c.Role {
			case "symbol":
				r.sym = c.Node
			case "name":
				r.name = c.Node
			case "body":
				r.body = c.Node
			}
		}
		if r.sym < 0 || r.name < 0 {
			continue
		}
		raws = append(raws, r)
	}
	sort.SliceStable(raws, func(i, j int) bool {
		a, b := s.Nodes[raws[i].sym], s.Nodes[raws[j].sym]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.End > b.End
	})
	byNode := map[int]int{} // symbol node -> index in out
	out := make([]Symbol, 0, len(raws))
	for _, r := range raws {
		if _, dup := byNode[r.sym]; dup {
			continue
		}
		n := s.Nodes[r.sym]
		sym := Symbol{Kind: n.Type, Start: n.Start, End: n.End, node: r.sym}
		sym.Name = s.Text(r.name)
		// qualify by the nearest enclosing symbol
		for p := n.Parent; p >= 0; p = s.Nodes[p].Parent {
			if oi, ok := byNode[p]; ok {
				sym.Name = out[oi].Name + "." + sym.Name
				break
			}
		}
		if n.Parent >= 0 && s.Nodes[n.Parent].Type == "export_statement" {
			sym.Exported = true
		}
		headEnd := n.End
		if r.body >= 0 {
			headEnd = s.Nodes[r.body].Start
			sym.Body = sexp(s, r.body)
		}
		sym.Signature = normalizeSignature(string(s.Source[n.Start:headEnd]))
		if sym.Exported {
			sym.Signature = "export " + sym.Signature
		}
		byNode[r.sym] = len(out)
		out = append(out, sym)
	}
	return out
}

// sexp renders a subtree as an S-expression: named nodes by type, leaves by
// text, comments dropped, no whitespace.
func sexp(s Syntax, i int) string {
	var b strings.Builder
	var walk func(int)
	walk = func(i int) {
		n := s.Nodes[i]
		if n.Comment {
			return
		}
		if len(n.Children) == 0 {
			b.WriteString(n.Type)
			if n.Named {
				b.WriteByte('=')
				b.WriteString(s.Text(i))
			}
			return
		}
		b.WriteByte('(')
		b.WriteString(n.Type)
		for _, c := range n.Children {
			if s.Nodes[c].Comment {
				continue
			}
			b.WriteByte(' ')
			walk(c)
		}
		b.WriteByte(')')
	}
	walk(i)
	return b.String()
}

// normalizeSignature collapses whitespace and drops it next to punctuation,
// so `add( a : number ) : number` and `add(a: number): number` hash alike
// while `function f` keeps its separator.
func normalizeSignature(str string) string {
	var out []rune
	pending := false
	for _, r := range strings.TrimSpace(str) {
		if unicode.IsSpace(r) {
			pending = true
			continue
		}
		if pending {
			if len(out) > 0 && isWord(out[len(out)-1]) && isWord(r) {
				out = append(out, ' ')
			}
			pending = false
		}
		out = append(out, r)
	}
	return string(out)
}

func isWord(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func sha(str string) string {
	h := sha256.Sum256([]byte(str))
	return hex.EncodeToString(h[:])
}

// FileHash hashes file bytes.
func FileHash(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:])
}

// Hash3Of computes the three-layer hash for a symbol in a file.
func Hash3Of(sym Symbol, fileHash string) core.Hash3 {
	return core.Hash3{Signature: sha(sym.Signature), Body: sha(sym.Body), File: fileHash}
}

// GenericHash3 is the conservative hash for languages without a resolver:
// every layer is the file hash, so any change is a signature change.
func GenericHash3(src []byte) core.Hash3 {
	h := FileHash(src)
	return core.Hash3{Signature: h, Body: h, File: h}
}

// FindSymbol returns the symbol named name (qualified), if any.
func FindSymbol(syms []Symbol, name string) (Symbol, bool) {
	for _, s := range syms {
		if s.Name == name {
			return s, true
		}
	}
	return Symbol{}, false
}

// SmallestContaining returns the innermost symbol whose range contains
// [start, end). ok is false when none does: the caller falls back to the
// file anchor.
func SmallestContaining(syms []Symbol, start, end uint32) (Symbol, bool) {
	best, found := Symbol{}, false
	for _, s := range syms {
		if s.Start <= start && end <= s.End {
			if !found || (s.End-s.Start) < (best.End-best.Start) {
				best, found = s, true
			}
		}
	}
	return best, found
}

// Move is an AnchorMoved candidate: the same signature hash disappeared at
// From and appeared at To.
type Move struct {
	From core.Anchor
	To   core.Anchor
}

// DetectMoves compares two snapshots (anchor -> Hash3) and reports symbols
// whose signature hash vanished from one anchor and surfaced at exactly
// one new anchor. Ambiguous cases (several candidates) are not reported:
// a move is automatic only when it is unambiguous.
func DetectMoves(before, after map[string]core.Hash3) []Move {
	gone := map[string][]string{}    // signature -> anchors that disappeared
	arrived := map[string][]string{} // signature -> anchors that appeared
	for a, h := range before {
		if _, still := after[a]; !still {
			gone[h.Signature] = append(gone[h.Signature], a)
		}
	}
	for a, h := range after {
		if _, was := before[a]; !was {
			arrived[h.Signature] = append(arrived[h.Signature], a)
		}
	}
	var moves []Move
	for sig, from := range gone {
		to := arrived[sig]
		if len(from) != 1 || len(to) != 1 {
			continue
		}
		f, err1 := core.ParseAnchor(from[0])
		t, err2 := core.ParseAnchor(to[0])
		if err1 != nil || err2 != nil {
			continue
		}
		moves = append(moves, Move{From: f, To: t})
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].From.String() < moves[j].From.String() })
	return moves
}
