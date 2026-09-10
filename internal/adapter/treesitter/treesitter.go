// Package treesitter runs tree-sitter (runtime + TypeScript/TSX grammars)
// as a single wasm32-wasi module under wazero: no CGO, no dynamic linking.
// The wasm is built by scripts/build-treesitter-wasm.sh from the C sources
// pinned in VERSION and embedded here.
//
// The module exposes a flat, data-only API (see csrc/keelage_ts.c); this
// package turns its dumps into Go trees and query matches.
package treesitter

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed wasm/keelage-ts.wasm
var wasmBytes []byte

// Language selects a grammar in the module.
type Language uint32

const (
	TypeScript Language = 0
	TSX        Language = 1
)

func (l Language) String() string {
	switch l {
	case TypeScript:
		return "typescript"
	case TSX:
		return "tsx"
	}
	return fmt.Sprintf("lang(%d)", uint32(l))
}

// Runtime holds the compiled module. Create one per process; it is safe for
// concurrent use. Parsers are instantiated from it.
type Runtime struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// NewRuntime compiles the embedded module. cacheDir, when non-empty, stores
// the compiled code so later starts skip compilation.
func NewRuntime(ctx context.Context, cacheDir string) (*Runtime, error) {
	cfg := wazero.NewRuntimeConfig()
	if cacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("treesitter: compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("treesitter: wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("treesitter: compile: %w", err)
	}
	return &Runtime{rt: rt, compiled: compiled}, nil
}

// Close releases the runtime and every parser created from it.
func (r *Runtime) Close(ctx context.Context) error { return r.rt.Close(ctx) }

// Parser is one module instance bound to a language. Not safe for
// concurrent use; it serialises calls with a mutex.
type Parser struct {
	mu     sync.Mutex
	lang   Language
	mod    api.Module
	mem    api.Memory
	parser uint32
	fn     map[string]api.Function

	symbols   []string // symbol id -> type name
	fields    []string // field id -> name
	namedSyms []bool
}

// NewParser instantiates the module for lang.
func (r *Runtime) NewParser(ctx context.Context, lang Language) (*Parser, error) {
	mod, err := r.rt.InstantiateModule(ctx, r.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return nil, fmt.Errorf("treesitter: instantiate: %w", err)
	}
	p := &Parser{lang: lang, mod: mod, mem: mod.Memory(), fn: map[string]api.Function{}}
	for _, name := range []string{
		"ks_malloc", "ks_free", "ks_lang_count", "ks_lang_abi", "ks_lang_symbol_count", "ks_lang_symbol_name",
		"ks_lang_symbol_named", "ks_lang_field_count", "ks_lang_field_name", "ks_parser_new", "ks_parser_delete",
		"ks_parse", "ks_tree_delete", "ks_tree_dump", "ks_query_new", "ks_query_delete", "ks_query_capture_count",
		"ks_query_capture_name", "ks_query_exec",
	} {
		f := mod.ExportedFunction(name)
		if f == nil {
			_ = mod.Close(ctx)
			return nil, fmt.Errorf("treesitter: module lacks export %s", name)
		}
		p.fn[name] = f
	}
	n, err := p.call(ctx, "ks_lang_count")
	if err != nil {
		_ = mod.Close(ctx)
		return nil, err
	}
	if uint32(lang) >= uint32(n) {
		_ = mod.Close(ctx)
		return nil, fmt.Errorf("treesitter: unknown language %d", lang)
	}
	pp, err := p.call(ctx, "ks_parser_new", uint64(lang))
	if err != nil {
		_ = mod.Close(ctx)
		return nil, fmt.Errorf("treesitter: parser for %s: %w", lang, err)
	}
	if pp == 0 {
		_ = mod.Close(ctx)
		return nil, fmt.Errorf("treesitter: parser for %s: language rejected by runtime", lang)
	}
	p.parser = uint32(pp)
	if err := p.loadMetadata(ctx); err != nil {
		_ = mod.Close(ctx)
		return nil, err
	}
	return p, nil
}

// Close frees the instance.
func (p *Parser) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.call(ctx, "ks_parser_delete", uint64(p.parser))
	return p.mod.Close(ctx)
}

// Language returns the parser's grammar.
func (p *Parser) Language() Language { return p.lang }

// ABI returns the grammar's language ABI version.
func (p *Parser) ABI(ctx context.Context) (uint32, error) {
	v, err := p.call(ctx, "ks_lang_abi", uint64(p.lang))
	return uint32(v), err
}

func (p *Parser) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	res, err := p.fn[name].Call(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("treesitter: %s: %w", name, err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

func (p *Parser) cstring(ptr uint32) (string, error) {
	if ptr == 0 {
		return "", errors.New("treesitter: null string")
	}
	var out []byte
	for {
		chunk, ok := p.mem.Read(ptr+uint32(len(out)), 64)
		if !ok {
			// near the end of memory: read byte by byte
			b, ok := p.mem.ReadByte(ptr + uint32(len(out)))
			if !ok {
				return "", errors.New("treesitter: string out of bounds")
			}
			if b == 0 {
				return string(out), nil
			}
			out = append(out, b)
			continue
		}
		for i, b := range chunk {
			if b == 0 {
				return string(append(out, chunk[:i]...)), nil
			}
		}
		out = append(out, chunk...)
	}
}

func (p *Parser) loadMetadata(ctx context.Context) error {
	n, err := p.call(ctx, "ks_lang_symbol_count", uint64(p.lang))
	if err != nil {
		return err
	}
	p.symbols = make([]string, n)
	p.namedSyms = make([]bool, n)
	for i := uint64(0); i < n; i++ {
		ptr, err := p.call(ctx, "ks_lang_symbol_name", uint64(p.lang), i)
		if err != nil {
			return err
		}
		if p.symbols[i], err = p.cstring(uint32(ptr)); err != nil {
			return err
		}
		named, err := p.call(ctx, "ks_lang_symbol_named", uint64(p.lang), i)
		if err != nil {
			return err
		}
		p.namedSyms[i] = named != 0
	}
	fc, err := p.call(ctx, "ks_lang_field_count", uint64(p.lang))
	if err != nil {
		return err
	}
	p.fields = make([]string, fc+1) // field ids are 1-based; 0 = none
	for i := uint64(1); i <= fc; i++ {
		ptr, err := p.call(ctx, "ks_lang_field_name", uint64(p.lang), i)
		if err != nil {
			return err
		}
		if p.fields[i], err = p.cstring(uint32(ptr)); err != nil {
			return err
		}
	}
	return nil
}

// alloc copies b into module memory; the caller frees with free.
func (p *Parser) alloc(ctx context.Context, b []byte) (uint32, error) {
	ptr, err := p.call(ctx, "ks_malloc", uint64(len(b)))
	if err != nil {
		return 0, err
	}
	if ptr == 0 {
		return 0, errors.New("treesitter: out of memory")
	}
	if len(b) > 0 && !p.mem.Write(uint32(ptr), b) {
		return 0, errors.New("treesitter: write out of bounds")
	}
	return uint32(ptr), nil
}

func (p *Parser) free(ctx context.Context, ptr uint32) {
	if ptr != 0 {
		_, _ = p.call(ctx, "ks_free", uint64(ptr))
	}
}

func (p *Parser) readU32(ptr uint32) (uint32, error) {
	v, ok := p.mem.ReadUint32Le(ptr)
	if !ok {
		return 0, errors.New("treesitter: read out of bounds")
	}
	return v, nil
}

// Node is one syntax node in a Tree.
type Node struct {
	Type     string // grammar symbol name, e.g. "function_declaration"
	Field    string // field name under its parent, or ""
	Start    uint32 // byte offsets into the source
	End      uint32
	Named    bool
	Missing  bool
	Extra    bool // e.g. comments
	HasError bool
	IsError  bool
	Parent   int // index, -1 for the root
	Children []int
	depth    int
}

// Tree is a parsed file: a preorder slice of nodes plus the source. It
// keeps its wasm-side handle until Close so queries can run against it.
type Tree struct {
	Nodes  []Node
	Source []byte
	p      *Parser
	handle uint32
}

// Root returns the root node index (0).
func (t *Tree) Root() int { return 0 }

// Text returns the source of node i.
func (t *Tree) Text(i int) string { return string(t.Source[t.Nodes[i].Start:t.Nodes[i].End]) }

// ChildByField returns the first child of i with the given field name.
func (t *Tree) ChildByField(i int, field string) (int, bool) {
	for _, c := range t.Nodes[i].Children {
		if t.Nodes[c].Field == field {
			return c, true
		}
	}
	return -1, false
}

// Close frees the wasm-side tree. Nodes and Source stay usable.
func (t *Tree) Close(ctx context.Context) {
	if t.handle == 0 {
		return
	}
	t.p.mu.Lock()
	defer t.p.mu.Unlock()
	_, _ = t.p.call(ctx, "ks_tree_delete", uint64(t.handle))
	t.handle = 0
}

const treeRecord = 6 * 4

// Parse parses src. The returned Tree must be closed.
func (p *Parser) Parse(ctx context.Context, src []byte) (*Tree, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	srcPtr, err := p.alloc(ctx, src)
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, srcPtr)
	handle, err := p.call(ctx, "ks_parse", uint64(p.parser), uint64(srcPtr), uint64(len(src)))
	if err != nil {
		return nil, err
	}
	if handle == 0 {
		return nil, errors.New("treesitter: parse returned no tree")
	}
	t := &Tree{Source: append([]byte(nil), src...), p: p, handle: uint32(handle)}
	outPtr, err := p.alloc(ctx, make([]byte, 4))
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, outPtr)
	count, err := p.call(ctx, "ks_tree_dump", uint64(handle), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	buf, err := p.readU32(outPtr)
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, buf)
	raw, ok := p.mem.Read(buf, uint32(count)*treeRecord)
	if !ok {
		return nil, errors.New("treesitter: dump out of bounds")
	}
	t.Nodes = make([]Node, count)
	stack := []int{}
	for i := range t.Nodes {
		r := raw[i*treeRecord:]
		depth := int(binary.LittleEndian.Uint32(r[0:]))
		sym := binary.LittleEndian.Uint32(r[4:])
		field := binary.LittleEndian.Uint32(r[8:])
		flags := binary.LittleEndian.Uint32(r[20:])
		n := Node{
			Start: binary.LittleEndian.Uint32(r[12:]), End: binary.LittleEndian.Uint32(r[16:]),
			Named: flags&1 != 0, Missing: flags&2 != 0, Extra: flags&4 != 0, HasError: flags&8 != 0, IsError: flags&16 != 0,
			Parent: -1, depth: depth,
		}
		if int(sym) < len(p.symbols) {
			n.Type = p.symbols[sym]
		} else {
			n.Type = fmt.Sprintf("sym(%d)", sym)
		}
		if n.IsError {
			n.Type = "ERROR"
		}
		if int(field) < len(p.fields) {
			n.Field = p.fields[field]
		}
		for len(stack) > depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			n.Parent = stack[len(stack)-1]
			t.Nodes[n.Parent].Children = append(t.Nodes[n.Parent].Children, i)
		}
		t.Nodes[i] = n
		stack = append(stack, i)
	}
	return t, nil
}

// Query is a compiled tree-sitter query.
type Query struct {
	p        *Parser
	handle   uint32
	captures []string
}

// NewQuery compiles a query in tree-sitter's S-expression syntax.
func (p *Parser) NewQuery(ctx context.Context, src string) (*Query, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	srcPtr, err := p.alloc(ctx, []byte(src))
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, srcPtr)
	errPtr, err := p.alloc(ctx, make([]byte, 8))
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, errPtr)
	h, err := p.call(ctx, "ks_query_new", uint64(p.lang), uint64(srcPtr), uint64(len(src)), uint64(errPtr), uint64(errPtr+4))
	if err != nil {
		return nil, err
	}
	if h == 0 {
		off, _ := p.readU32(errPtr)
		typ, _ := p.readU32(errPtr + 4)
		return nil, fmt.Errorf("treesitter: query error %s at byte %d", queryErrorName(typ), off)
	}
	q := &Query{p: p, handle: uint32(h)}
	n, err := p.call(ctx, "ks_query_capture_count", h)
	if err != nil {
		return nil, err
	}
	lenPtr, err := p.alloc(ctx, make([]byte, 4))
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, lenPtr)
	for i := uint64(0); i < n; i++ {
		ptr, err := p.call(ctx, "ks_query_capture_name", h, i, uint64(lenPtr))
		if err != nil {
			return nil, err
		}
		l, _ := p.readU32(lenPtr)
		b, ok := p.mem.Read(uint32(ptr), l)
		if !ok {
			return nil, errors.New("treesitter: capture name out of bounds")
		}
		q.captures = append(q.captures, string(b))
	}
	return q, nil
}

func queryErrorName(t uint32) string {
	switch t {
	case 1:
		return "syntax"
	case 2:
		return "node type"
	case 3:
		return "field"
	case 4:
		return "capture"
	case 5:
		return "structure"
	case 6:
		return "language"
	}
	return fmt.Sprintf("error(%d)", t)
}

// Close frees the query.
func (q *Query) Close(ctx context.Context) {
	q.p.mu.Lock()
	defer q.p.mu.Unlock()
	_, _ = q.p.call(ctx, "ks_query_delete", uint64(q.handle))
}

// CaptureNames lists capture names by index.
func (q *Query) CaptureNames() []string { return q.captures }

// Capture is one captured node in a match.
type Capture struct {
	Name string
	Node int // index into Tree.Nodes
}

// Match is one pattern match.
type Match struct {
	Pattern  int
	Captures []Capture
}

const matchRecord = 5 * 4

// Exec runs q over t. Matches keep the query engine's order.
func (q *Query) Exec(ctx context.Context, t *Tree) ([]Match, error) {
	if t.handle == 0 {
		return nil, errors.New("treesitter: tree is closed")
	}
	p := q.p
	p.mu.Lock()
	defer p.mu.Unlock()
	outPtr, err := p.alloc(ctx, make([]byte, 4))
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, outPtr)
	count, err := p.call(ctx, "ks_query_exec", uint64(q.handle), uint64(t.handle), uint64(outPtr))
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	buf, err := p.readU32(outPtr)
	if err != nil {
		return nil, err
	}
	defer p.free(ctx, buf)
	raw, ok := p.mem.Read(buf, uint32(count)*matchRecord)
	if !ok {
		return nil, errors.New("treesitter: match dump out of bounds")
	}
	// (start, end, type) -> node index; preorder puts the outermost first, which is what captures want.
	type key struct {
		start, end uint32
		typ        string
	}
	index := make(map[key]int, len(t.Nodes))
	for i := len(t.Nodes) - 1; i >= 0; i-- {
		n := t.Nodes[i]
		index[key{n.Start, n.End, n.Type}] = i
	}
	var matches []Match
	for i := 0; i < int(count); i++ {
		r := raw[i*matchRecord:]
		first := binary.LittleEndian.Uint32(r[0:])
		if first == 0xFFFFFFFF { // match header
			matches = append(matches, Match{Pattern: int(binary.LittleEndian.Uint32(r[4:]))})
			continue
		}
		if len(matches) == 0 {
			return nil, errors.New("treesitter: capture before match header")
		}
		capIdx := int(binary.LittleEndian.Uint32(r[4:]))
		sym := binary.LittleEndian.Uint32(r[8:])
		start := binary.LittleEndian.Uint32(r[12:])
		end := binary.LittleEndian.Uint32(r[16:])
		typ := ""
		if int(sym) < len(p.symbols) {
			typ = p.symbols[sym]
		}
		node, ok := index[key{start, end, typ}]
		if !ok {
			return nil, fmt.Errorf("treesitter: capture %s@%d-%d not in tree", typ, start, end)
		}
		name := ""
		if capIdx < len(q.captures) {
			name = q.captures[capIdx]
		}
		cur := &matches[len(matches)-1]
		cur.Captures = append(cur.Captures, Capture{Name: name, Node: node})
	}
	return matches, nil
}
