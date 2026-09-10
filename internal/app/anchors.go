package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/port"
)

// AnchorSummary is the current state of one recorded anchor.
type AnchorSummary struct {
	Key       string
	Anchor    core.Anchor
	Hash      core.Hash3
	State     realization.AnchorState
	Ref       string
	MovedTo   string
	UpdatedAt time.Time
}

// AnchorIndex projects anchor events (anchor_history's current row).
type AnchorIndex struct {
	mu      sync.RWMutex
	entries map[string]AnchorSummary
}

// NewAnchorIndex returns an empty index.
func NewAnchorIndex() *AnchorIndex { return &AnchorIndex{entries: map[string]AnchorSummary{}} }

// Name implements port.Projector.
func (a *AnchorIndex) Name() string { return "anchor_current" }

// Handle implements port.Projector.
func (a *AnchorIndex) Handle(_ context.Context, _ core.Envelope, ev core.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch v := ev.(type) {
	case realization.AnchorRecorded:
		an, _ := core.ParseAnchor(v.Key)
		a.entries[v.Key] = AnchorSummary{Key: v.Key, Anchor: an, Hash: v.Hash, State: realization.StateRecorded, Ref: v.Ref, UpdatedAt: v.At}
	case realization.AnchorVerified:
		e := a.entries[v.Key]
		e.Hash, e.State, e.Ref, e.UpdatedAt = v.Hash, realization.StateVerified, v.Ref, v.At
		a.entries[v.Key] = e
	case realization.AnchorStaled:
		e := a.entries[v.Key]
		e.State, e.UpdatedAt = v.State, v.At
		a.entries[v.Key] = e
	case realization.AnchorMoved:
		e := a.entries[v.Key]
		e.MovedTo, e.UpdatedAt = v.To, v.At
		a.entries[v.Key] = e
	}
	return nil
}

// Get returns one anchor.
func (a *AnchorIndex) Get(key string) (AnchorSummary, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.entries[key]
	return e, ok
}

// All lists anchors sorted by key.
func (a *AnchorIndex) All() []AnchorSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AnchorSummary, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ErrUnsupportedScheme: only code:// anchors resolve in v0.
var ErrUnsupportedScheme = errors.New("resolver: only code:// anchors resolve in v0")

// Resolved is the current state of an anchor in the working tree.
type Resolved struct {
	Anchor   core.Anchor
	Hash     core.Hash3
	Exists   bool
	Language string // "typescript", "tsx" or "generic"
	// Degraded is true when a symbol anchor was hashed at file level because
	// the language has no resolver (spec: 해석 실패 시 경로 수준으로 강등).
	Degraded bool
}

type parsedFile struct {
	mtime   time.Time
	size    int64
	hash    string
	symbols []realization.Symbol
	lang    string
}

// Resolver hashes anchors against files under Root, parsing lazily and
// caching by (path, mtime, size). No index is persisted.
type Resolver struct {
	Root   string
	Parser port.SyntaxParser
	mu     sync.Mutex
	cache  map[string]parsedFile
}

// NewResolver returns a resolver for the repository at root.
func NewResolver(root string, parser port.SyntaxParser) *Resolver {
	return &Resolver{Root: root, Parser: parser, cache: map[string]parsedFile{}}
}

func (r *Resolver) load(ctx context.Context, rel string) (parsedFile, bool, error) {
	full := filepath.Join(r.Root, filepath.FromSlash(rel))
	fi, err := os.Stat(full)
	if errors.Is(err, os.ErrNotExist) {
		return parsedFile{}, false, nil
	}
	if err != nil {
		return parsedFile{}, false, err
	}
	if fi.IsDir() {
		return parsedFile{}, false, nil
	}
	r.mu.Lock()
	c, ok := r.cache[rel]
	r.mu.Unlock()
	if ok && c.mtime.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c, true, nil
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return parsedFile{}, false, err
	}
	pf := parsedFile{mtime: fi.ModTime(), size: fi.Size(), hash: realization.FileHash(src), lang: "generic"}
	if r.Parser != nil && r.Parser.Supports(rel) {
		syn, matches, err := r.Parser.Parse(ctx, rel, src)
		if err != nil {
			return parsedFile{}, false, fmt.Errorf("resolver: parse %s: %w", rel, err)
		}
		pf.symbols = realization.ExtractSymbols(syn, matches)
		pf.lang = r.Parser.LanguageName(rel)
	}
	r.mu.Lock()
	r.cache[rel] = pf
	r.mu.Unlock()
	return pf, true, nil
}

// Resolve computes the current Hash3 of a code anchor.
func (r *Resolver) Resolve(ctx context.Context, a core.Anchor) (Resolved, error) {
	if a.Scheme != core.SchemeCode {
		return Resolved{Anchor: a}, ErrUnsupportedScheme
	}
	pf, exists, err := r.load(ctx, a.Path)
	if err != nil || !exists {
		return Resolved{Anchor: a, Exists: false}, err
	}
	res := Resolved{Anchor: a, Exists: true, Language: pf.lang}
	if pf.lang == "generic" {
		res.Hash = core.Hash3{Signature: pf.hash, Body: pf.hash, File: pf.hash}
		res.Degraded = a.Symbol != ""
		return res, nil
	}
	if a.Symbol == "" {
		res.Hash = fileSurfaceHash(pf)
		return res, nil
	}
	sym, ok := realization.FindSymbol(pf.symbols, a.Symbol)
	if !ok {
		res.Exists = false
		return res, nil
	}
	res.Hash = realization.Hash3Of(sym, pf.hash)
	return res, nil
}

// fileSurfaceHash is the Hash3 of a whole-file anchor in a parsed language:
// signature = every symbol's signature, body = every symbol's body.
func fileSurfaceHash(pf parsedFile) core.Hash3 {
	var sigs, bodies string
	for _, s := range pf.symbols {
		sigs += s.Name + "|" + s.Signature + "\n"
		bodies += s.Name + "|" + s.Body + "\n"
	}
	return core.Hash3{
		Signature: realization.FileHash([]byte(sigs)),
		Body:      realization.FileHash([]byte(bodies)),
		File:      pf.hash,
	}
}

// Snapshot returns every symbol anchor (and its Hash3) in the given files,
// for move detection. Files without a parser contribute nothing.
func (r *Resolver) Snapshot(ctx context.Context, files []string) (map[string]core.Hash3, error) {
	out := map[string]core.Hash3{}
	for _, f := range files {
		pf, exists, err := r.load(ctx, f)
		if err != nil {
			return nil, err
		}
		if !exists || pf.lang == "generic" {
			continue
		}
		for _, s := range pf.symbols {
			a := core.Anchor{Scheme: core.SchemeCode, Path: f, Symbol: s.Name}
			out[a.String()] = realization.Hash3Of(s, pf.hash)
		}
	}
	return out, nil
}

// VerifyResult is one anchor's verify outcome.
type VerifyResult struct {
	Key      string
	Before   realization.AnchorState
	After    realization.AnchorState
	Change   core.HashChange
	Exists   bool
	Language string
	Degraded bool
	Skipped  string // reason, when not verified
}

// VerifyReport is the outcome of one verify run.
type VerifyReport struct {
	Ref     string
	Results []VerifyResult
	Moves   []realization.Move
}

// Count returns how many anchors ended in state s.
func (r VerifyReport) Count(s realization.AnchorState) int {
	n := 0
	for _, x := range r.Results {
		if x.After == s {
			n++
		}
	}
	return n
}

// Verifier runs `verify`: resolve every recorded anchor (or those in the
// changed files), feed the Anchor aggregate, and detect moves. Idempotent
// per (ref, anchor), so a re-run at the same commit writes nothing.
type Verifier struct {
	Pipeline *Pipeline
	Anchors  *AnchorIndex
	Resolver *Resolver
}

// Record baselines an anchor at its current hash.
func (v *Verifier) Record(ctx context.Context, actor core.ActorRef, key, ref string) (Resolved, error) {
	a, err := core.ParseAnchor(key)
	if err != nil {
		return Resolved{}, err
	}
	res, err := v.Resolver.Resolve(ctx, a)
	if err != nil {
		return res, err
	}
	if !res.Exists {
		return res, fmt.Errorf("record %s: not found in the working tree", key)
	}
	cmd := realization.RecordAnchor{AnchorCmd: realization.AnchorCmd{Key: a.String(), Idem: idemFor("record", ref, a.String(), observation(res))}, Hash: res.Hash, Ref: ref}
	_, err = v.Pipeline.Handle(ctx, actor, cmd)
	return res, err
}

// Verify checks anchors. files == nil means all recorded anchors; otherwise
// only anchors in those files (plus move detection across them).
func (v *Verifier) Verify(ctx context.Context, actor core.ActorRef, ref string, files []string) (VerifyReport, error) {
	report := VerifyReport{Ref: ref}
	inFiles := map[string]bool{}
	for _, f := range files {
		inFiles[filepath.ToSlash(f)] = true
	}
	before := map[string]core.Hash3{}
	for _, an := range v.Anchors.All() {
		if an.Anchor.Scheme != core.SchemeCode {
			report.Results = append(report.Results, VerifyResult{Key: an.Key, Before: an.State, After: an.State, Skipped: "scheme not resolvable in v0"})
			continue
		}
		if files != nil && !inFiles[an.Anchor.Path] {
			continue
		}
		if an.Anchor.Symbol != "" {
			before[an.Key] = an.Hash
		}
		res, err := v.Resolver.Resolve(ctx, an.Anchor)
		if err != nil {
			return report, err
		}
		// Idempotent per (ref, anchor, observation): a CI re-run at the same
		// commit writes nothing, while a new edit in the working tree at the
		// same HEAD is a new observation and is recorded.
		cmd := realization.VerifyAnchor{
			AnchorCmd: realization.AnchorCmd{Key: an.Key, Idem: idemFor("verify", ref, an.Key, observation(res))},
			Current:   res.Hash, Exists: res.Exists, Ref: ref,
		}
		if _, err := v.Pipeline.Handle(ctx, actor, cmd); err != nil {
			return report, err
		}
		after, _ := v.Anchors.Get(an.Key)
		r := VerifyResult{Key: an.Key, Before: an.State, After: after.State, Exists: res.Exists, Language: res.Language, Degraded: res.Degraded}
		if res.Exists {
			r.Change = an.Hash.Cmp(res.Hash)
		}
		report.Results = append(report.Results, r)
	}
	if files != nil && len(before) > 0 {
		after, err := v.Resolver.Snapshot(ctx, files)
		if err != nil {
			return report, err
		}
		for _, m := range realization.DetectMoves(before, after) {
			h := after[m.To.String()]
			cmd := realization.MoveAnchor{AnchorCmd: realization.AnchorCmd{Key: m.From.String(), Idem: idemFor("move", ref, m.From.String(), m.To.String())}, To: m.To.String(), Hash: h, Ref: ref}
			if _, err := v.Pipeline.Handle(ctx, actor, cmd); err != nil {
				return report, err
			}
			report.Moves = append(report.Moves, m)
		}
	}
	return report, nil
}

func idemFor(op, ref, key, observed string) string {
	if ref == "" {
		return ""
	}
	return op + ":" + ref + ":" + key + ":" + observed
}

func observation(r Resolved) string {
	if !r.Exists {
		return "missing"
	}
	return r.Hash.Signature[:min(12, len(r.Hash.Signature))] + "." + r.Hash.Body[:min(12, len(r.Hash.Body))] + "." + r.Hash.File[:min(12, len(r.Hash.File))]
}
