package treesitter

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/young1ll/keelage/internal/core/realization"
)

//go:embed queries/typescript.scm
var typescriptQuery string

// Resolver implements port.SyntaxParser for TypeScript and TSX: parse,
// run the language's symbols query, and hand a language-neutral Syntax
// plus symbol matches to core/realization.
type Resolver struct {
	rt      *Runtime
	mu      sync.Mutex
	parsers map[Language]*Parser
	queries map[Language]*Query
}

// NewResolver creates parsers lazily per language.
func NewResolver(rt *Runtime) *Resolver {
	return &Resolver{rt: rt, parsers: map[Language]*Parser{}, queries: map[Language]*Query{}}
}

// Supports reports whether the resolver handles the file (by extension).
func (r *Resolver) Supports(file string) bool {
	_, ok := languageFor(file)
	return ok
}

// LanguageName returns the grammar name for a file, or "".
func (r *Resolver) LanguageName(file string) string {
	l, ok := languageFor(file)
	if !ok {
		return ""
	}
	return l.String()
}

func languageFor(file string) (Language, bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".ts", ".mts", ".cts":
		return TypeScript, true
	case ".tsx":
		return TSX, true
	}
	return 0, false
}

func (r *Resolver) parserFor(ctx context.Context, lang Language) (*Parser, *Query, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.parsers[lang]; ok {
		return p, r.queries[lang], nil
	}
	p, err := r.rt.NewParser(ctx, lang)
	if err != nil {
		return nil, nil, err
	}
	q, err := p.NewQuery(ctx, typescriptQuery)
	if err != nil {
		_ = p.Close(ctx)
		return nil, nil, fmt.Errorf("treesitter: symbols query for %s: %w", lang, err)
	}
	r.parsers[lang] = p
	r.queries[lang] = q
	return p, q, nil
}

// Parse implements port.SyntaxParser.
func (r *Resolver) Parse(ctx context.Context, file string, src []byte) (realization.Syntax, []realization.SymbolMatch, error) {
	lang, ok := languageFor(file)
	if !ok {
		return realization.Syntax{}, nil, fmt.Errorf("treesitter: unsupported file %s", file)
	}
	p, q, err := r.parserFor(ctx, lang)
	if err != nil {
		return realization.Syntax{}, nil, err
	}
	tree, err := p.Parse(ctx, src)
	if err != nil {
		return realization.Syntax{}, nil, err
	}
	defer tree.Close(ctx)
	matches, err := q.Exec(ctx, tree)
	if err != nil {
		return realization.Syntax{}, nil, err
	}
	syn := realization.Syntax{Source: tree.Source, Nodes: make([]realization.SyntaxNode, len(tree.Nodes))}
	for i, n := range tree.Nodes {
		syn.Nodes[i] = realization.SyntaxNode{
			Type: n.Type, Field: n.Field, Start: n.Start, End: n.End, Named: n.Named,
			Comment: n.Type == "comment", Parent: n.Parent, Children: n.Children,
		}
	}
	out := make([]realization.SymbolMatch, 0, len(matches))
	for _, m := range matches {
		sm := realization.SymbolMatch{}
		for _, c := range m.Captures {
			sm.Captures = append(sm.Captures, realization.Capture{Role: c.Name, Node: c.Node})
		}
		out = append(out, sm)
	}
	return syn, out, nil
}

// Close frees every parser.
func (r *Resolver) Close(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for lang, q := range r.queries {
		q.Close(ctx)
		delete(r.queries, lang)
	}
	for lang, p := range r.parsers {
		_ = p.Close(ctx)
		delete(r.parsers, lang)
	}
}
