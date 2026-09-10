package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/core/supply"
	"github.com/young1ll/keelage/internal/port"
)

// LoadManifest reads the sidecar; a missing file yields an empty manifest.
func LoadManifest(root, repo string) (*supply.Manifest, error) {
	m := &supply.Manifest{Version: supply.ManifestVersion, Repo: repo, Adapters: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(supply.ManifestPath)))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Adapters == nil {
		m.Adapters = map[string]string{}
	}
	return m, nil
}

// SaveManifest writes the sidecar.
func SaveManifest(root string, m *supply.Manifest, now time.Time) error {
	m.UpdatedAt = now
	p := filepath.Join(root, filepath.FromSlash(supply.ManifestPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// ManifestExists reports whether the repository was initialised.
func ManifestExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(supply.ManifestPath)))
	return err == nil
}

// AbsorbResult is one file's import outcome.
type AbsorbResult struct {
	Path     string
	Kind     string
	ObjectID string
	Action   string // imported | updated | unchanged | skipped
	Scope    string
}

// Importer absorbs a repository's rule files and ADRs into the ledger
// (`keelage init`, implementation plan §3.6 active mode). Files are the
// source when they change: a changed rule file supersedes its constraint,
// a changed ADR revises its decision.
type Importer struct {
	Pipeline    *Pipeline
	IDs         port.IDGen
	Constraints *ConstraintIndex
	Decisions   *DecisionIndex
	Root        string
	Repo        string
	Clock       port.Clock
	// OnlyFiles restricts the import to these rule files (no walk, no ADRs):
	// used for the global ~/.claude/CLAUDE.md.
	OnlyFiles []string
	// Scope overrides the derived scope (e.g. the personal layer).
	Scope *core.ScopeKey
}

var ruleFiles = []string{"CLAUDE.md", "AGENTS.md"}

var adrDirs = []string{"docs/adr", "doc/adr", "adr"}

var adrTitle = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// Absorb imports rule files (root and nested CLAUDE.md, AGENTS.md) and
// ADRs. verify marks the imported objects verified right away
// ("imported-verified"); otherwise they wait in the inbox as generated.
func (im *Importer) Absorb(ctx context.Context, actor core.ActorRef, m *supply.Manifest, verify bool) ([]AbsorbResult, error) {
	var out []AbsorbResult
	if im.OnlyFiles != nil {
		for _, rel := range im.OnlyFiles {
			if _, err := os.Stat(filepath.Join(im.Root, filepath.FromSlash(rel))); err != nil {
				continue
			}
			r, err := im.absorbRule(ctx, actor, m, rel, verify)
			if err != nil {
				return out, err
			}
			out = append(out, r)
		}
		return out, nil
	}
	files, err := im.ruleFiles()
	if err != nil {
		return nil, err
	}
	for _, rel := range files {
		r, err := im.absorbRule(ctx, actor, m, rel, verify)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	adrs, err := im.adrFiles()
	if err != nil {
		return nil, err
	}
	for _, rel := range adrs {
		r, err := im.absorbADR(ctx, actor, m, rel, verify)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (im *Importer) ruleFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(im.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		name := d.Name()
		if d.IsDir() {
			if p != im.Root && (name == ".git" || name == "node_modules" || name == ".keelage" || strings.HasPrefix(name, ".") && name != ".claude") {
				return filepath.SkipDir
			}
			return nil
		}
		for _, rf := range ruleFiles {
			if name == rf {
				rel, _ := filepath.Rel(im.Root, p)
				files = append(files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (im *Importer) adrFiles() ([]string, error) {
	var files []string
	for _, dir := range adrDirs {
		entries, err := os.ReadDir(filepath.Join(im.Root, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.EqualFold(e.Name(), "README.md") || strings.HasPrefix(e.Name(), "template") {
				continue
			}
			files = append(files, dir+"/"+e.Name())
		}
		break // the first existing directory wins
	}
	sort.Strings(files)
	return files, nil
}

// ruleScope: the root file scopes the repository, a nested one its subtree.
func (im *Importer) ruleScope(rel string) core.ScopeKey {
	if im.Scope != nil {
		return *im.Scope
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." || dir == "" {
		return core.ScopeKey{Repo: im.Repo}
	}
	return core.ScopeKey{Repo: im.Repo, PathGlob: dir + "/**"}
}

func (im *Importer) absorbRule(ctx context.Context, actor core.ActorRef, m *supply.Manifest, rel string, verify bool) (AbsorbResult, error) {
	raw, err := os.ReadFile(filepath.Join(im.Root, filepath.FromSlash(rel)))
	if err != nil {
		return AbsorbResult{}, err
	}
	// our own managed block is not the user's rule
	content := string(raw)
	if stripped, ok := supply.RemoveBlock(content, supply.PointerBlockID); ok {
		content = stripped
	}
	content = strings.TrimSpace(content)
	res := AbsorbResult{Path: rel, Kind: "rule"}
	if content == "" {
		res.Action = "skipped"
		return res, nil
	}
	hash := realization.FileHash([]byte(content))
	scope := im.ruleScope(rel)
	res.Scope = scope.String()
	prev, had := m.FindImport(rel)
	if had && prev.Hash == hash {
		res.ObjectID, res.Action = prev.ObjectID, "unchanged"
		return res, nil
	}
	id := im.IDs.New()
	draft := harness.DraftConstraint{
		ID: id, Idem: "import:" + rel + ":" + hash, ConstraintKind: harness.KindRule, Scope: scope, Body: content, Authored: true,
		Explanation: "imported from " + rel,
	}
	if had {
		draft.Supersedes = core.ID(prev.ObjectID)
	}
	if _, err := im.Pipeline.Handle(ctx, actor, draft); err != nil {
		return res, fmt.Errorf("import %s: %w", rel, err)
	}
	if verify {
		if _, err := im.Pipeline.Handle(ctx, actor, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: id}}); err != nil {
			return res, err
		}
	}
	if had {
		if _, err := im.Pipeline.Handle(ctx, actor, harness.SupersedeConstraint{ConstraintCmd: harness.ConstraintCmd{ID: core.ID(prev.ObjectID)}, By: id}); err != nil {
			if _, rej := core.AsRejection(err); !rej {
				return res, err
			}
		}
		res.Action = "updated"
	} else {
		res.Action = "imported"
	}
	res.ObjectID = string(id)
	m.SetImport(supply.Import{Path: rel, Kind: "rule", ObjectID: string(id), Hash: hash, Scope: scope.String()})
	return res, nil
}

func (im *Importer) absorbADR(ctx context.Context, actor core.ActorRef, m *supply.Manifest, rel string, verify bool) (AbsorbResult, error) {
	raw, err := os.ReadFile(filepath.Join(im.Root, filepath.FromSlash(rel)))
	if err != nil {
		return AbsorbResult{}, err
	}
	body := strings.TrimSpace(string(raw))
	res := AbsorbResult{Path: rel, Kind: "adr", Scope: core.ScopeKey{Repo: im.Repo}.String()}
	if body == "" {
		res.Action = "skipped"
		return res, nil
	}
	title := strings.TrimSuffix(filepath.Base(rel), ".md")
	if mt := adrTitle.FindStringSubmatch(body); mt != nil {
		title = mt[1]
	}
	hash := realization.FileHash([]byte(body))
	prev, had := m.FindImport(rel)
	if had && prev.Hash == hash {
		res.ObjectID, res.Action = prev.ObjectID, "unchanged"
		return res, nil
	}
	if had {
		id := core.ID(prev.ObjectID)
		if _, err := im.Pipeline.Handle(ctx, actor, harness.ReviseDecision{DecisionCmd: harness.DecisionCmd{ID: id, Idem: "import:" + rel + ":" + hash}, Title: title, Body: body}); err != nil {
			return res, fmt.Errorf("revise %s: %w", rel, err)
		}
		if verify {
			if _, err := im.Pipeline.Handle(ctx, actor, harness.VerifyDecision{DecisionCmd: harness.DecisionCmd{ID: id}}); err != nil {
				return res, err
			}
		}
		res.ObjectID, res.Action = prev.ObjectID, "updated"
		m.SetImport(supply.Import{Path: rel, Kind: "adr", ObjectID: prev.ObjectID, Hash: hash, Scope: res.Scope})
		return res, nil
	}
	id := im.IDs.New()
	draft := harness.DraftDecision{DecisionCmd: harness.DecisionCmd{ID: id, Idem: "import:" + rel + ":" + hash}, Title: title, Body: body, Scope: core.ScopeKey{Repo: im.Repo}}
	if _, err := im.Pipeline.Handle(ctx, actor, draft); err != nil {
		return res, fmt.Errorf("import %s: %w", rel, err)
	}
	if verify {
		if _, err := im.Pipeline.Handle(ctx, actor, harness.VerifyDecision{DecisionCmd: harness.DecisionCmd{ID: id}}); err != nil {
			return res, err
		}
	}
	res.ObjectID, res.Action = string(id), "imported"
	m.SetImport(supply.Import{Path: rel, Kind: "adr", ObjectID: string(id), Hash: hash, Scope: res.Scope})
	return res, nil
}

// Renderer materialises the context for a tool into the repository
// (patterns §8): the renderer is pure; Apply compares with the manifest,
// leaves diverged files alone, and never touches text outside managed blocks.
type Renderer struct {
	Constraints *ConstraintIndex
	Decisions   *DecisionIndex
	Anchors     *AnchorIndex
	Repo        string
	Root        string
	Tool        string
	Version     string // adapter version recorded in the manifest
	Person      string // the user: personal-layer constraints render too
}

// Plan resolves the input and the file operations for the tool.
func (r *Renderer) Plan(m *supply.Manifest) (supply.RenderInput, []supply.FileOp) {
	own := m.ImportedObjectIDs()
	in := supply.RenderInput{Repo: r.Repo, Tool: r.Tool}
	for _, c := range r.Constraints.InForce(core.ScopeKey{Repo: r.Repo, Person: r.Person}) {
		if own[string(c.ID)] {
			continue // already lives in this repo's own file
		}
		in.Constraints = append(in.Constraints, constraintFact(c))
	}
	// path-scoped constraints of this repo (InForce(repo) excludes them: a path key is narrower)
	for _, c := range r.Constraints.All() {
		if own[string(c.ID)] || c.Scope.Repo != r.Repo || c.Scope.PathGlob == "" || (c.State != harness.StateVerified && c.State != harness.StateReview) {
			continue
		}
		in.Constraints = append(in.Constraints, constraintFact(c))
	}
	pathByID := map[string]string{}
	for _, i := range m.Imports {
		pathByID[i.ObjectID] = i.Path
	}
	for _, d := range r.Decisions.All() {
		if d.State == harness.DecisionStateRetired || (d.Scope.Repo != "" && d.Scope.Repo != r.Repo) {
			continue
		}
		in.Decisions = append(in.Decisions, supply.DecisionFact{ID: d.ID, Title: d.Title, State: string(d.State), Path: pathByID[string(d.ID)]})
	}
	for _, a := range r.Anchors.All() {
		switch a.State {
		case realization.StateStale, realization.StateReview, realization.StateUnrealized:
			in.Anchors = append(in.Anchors, supply.AnchorFact{Key: a.Key, State: string(a.State), MovedTo: a.MovedTo})
		}
	}
	return in, supply.RenderClaudeCode(in)
}

// ApplyResult is one file's outcome.
type ApplyResult struct {
	Path   string
	Action string // written | unchanged | diverged | skipped
}

// Apply writes the operations. A managed block whose current body differs
// from the manifest's hash was edited by the user: it is reported as
// diverged and left alone unless force.
func (r *Renderer) Apply(m *supply.Manifest, ops []supply.FileOp, force bool) ([]ApplyResult, error) {
	var out []ApplyResult
	for _, op := range ops {
		full := filepath.Join(r.Root, filepath.FromSlash(op.Path))
		if op.Managed {
			cur, err := os.ReadFile(full)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return out, err
			}
			hash, body, present := supply.FindBlock(string(cur), op.BlockID)
			if prev, ok := m.FindRendered(op.Path); ok && present && !force && (hash != supply.BlockHash(body) || prev.Hash != hash) {
				out = append(out, ApplyResult{Path: op.Path, Action: "diverged"})
				continue
			}
			if present && body == op.Content {
				out = append(out, ApplyResult{Path: op.Path, Action: "unchanged"})
				m.SetRendered(supply.RenderedFile{Path: op.Path, Tool: r.Tool, Hash: supply.BlockHash(op.Content), Managed: true, BlockID: op.BlockID})
				continue
			}
			next := supply.UpsertBlock(string(cur), op.BlockID, op.Content)
			if err := os.WriteFile(full, []byte(next), 0o644); err != nil {
				return out, err
			}
			m.SetRendered(supply.RenderedFile{Path: op.Path, Tool: r.Tool, Hash: supply.BlockHash(op.Content), Managed: true, BlockID: op.BlockID})
			out = append(out, ApplyResult{Path: op.Path, Action: "written"})
			continue
		}
		hash := realization.FileHash([]byte(op.Content))
		if cur, err := os.ReadFile(full); err == nil && string(cur) == op.Content {
			m.SetRendered(supply.RenderedFile{Path: op.Path, Tool: r.Tool, Hash: hash})
			out = append(out, ApplyResult{Path: op.Path, Action: "unchanged"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return out, err
		}
		if err := os.WriteFile(full, []byte(op.Content), 0o644); err != nil {
			return out, err
		}
		m.SetRendered(supply.RenderedFile{Path: op.Path, Tool: r.Tool, Hash: hash})
		out = append(out, ApplyResult{Path: op.Path, Action: "written"})
	}
	m.Adapters[r.Tool] = r.Version
	return out, nil
}

const gitignoreLine = ".keelage/context/"

// EnsureGitignore adds the generated-content directory to .gitignore once.
func EnsureGitignore(root string, m *supply.Manifest) (bool, error) {
	p := filepath.Join(root, ".gitignore")
	cur, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(cur), "\n") {
		if strings.TrimSpace(l) == gitignoreLine || strings.TrimSpace(l) == ".keelage/" {
			return false, nil
		}
	}
	next := string(cur)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += gitignoreLine + "\n"
	if err := os.WriteFile(p, []byte(next), 0o644); err != nil {
		return false, err
	}
	m.Gitignore = true
	return true, nil
}

// Uninit removes everything keelage put in the repository: managed blocks,
// rendered files, the .gitignore line it added, and the sidecar. The
// ledger keeps its records.
func Uninit(root string, m *supply.Manifest) ([]ApplyResult, error) {
	var out []ApplyResult
	for _, rf := range m.Rendered {
		full := filepath.Join(root, filepath.FromSlash(rf.Path))
		if rf.Managed {
			cur, err := os.ReadFile(full)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return out, err
			}
			next, removed := supply.RemoveBlock(string(cur), rf.BlockID)
			if !removed {
				continue
			}
			if strings.TrimSpace(next) == "" {
				if err := os.Remove(full); err != nil {
					return out, err
				}
			} else if err := os.WriteFile(full, []byte(next), 0o644); err != nil {
				return out, err
			}
			out = append(out, ApplyResult{Path: rf.Path, Action: "restored"})
			continue
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return out, err
		}
		out = append(out, ApplyResult{Path: rf.Path, Action: "removed"})
	}
	if m.Gitignore {
		p := filepath.Join(root, ".gitignore")
		if cur, err := os.ReadFile(p); err == nil {
			var kept []string
			for _, l := range strings.Split(strings.TrimRight(string(cur), "\n"), "\n") {
				if strings.TrimSpace(l) != gitignoreLine {
					kept = append(kept, l)
				}
			}
			if len(kept) == 0 || (len(kept) == 1 && kept[0] == "") {
				_ = os.Remove(p)
			} else if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
				return out, err
			}
			out = append(out, ApplyResult{Path: ".gitignore", Action: "restored"})
		}
	}
	if err := os.RemoveAll(filepath.Join(root, ".keelage")); err != nil {
		return out, err
	}
	out = append(out, ApplyResult{Path: ".keelage", Action: "removed"})
	return out, nil
}
