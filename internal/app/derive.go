package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/port"
)

// Derived is the outcome of deriving a Change from one commit.
type Derived struct {
	ChangeID    core.ID
	SHA         string
	Files       []string
	Anchors     []string // realization anchors whose signature or body changed (or file anchors for generic files)
	Constraints []core.ID
	Sessions    []string
	Settled     bool // outside the harness: no constraint touched
	Replayed    bool // this commit had already been derived
}

// Deriver turns a commit into a Change (spec §1 "Change 도출": the
// boundary is the commit, the impact is diff → anchors → constraints; a
// change that touches no constraint settles without judgment).
type Deriver struct {
	Pipeline    *Pipeline
	Commits     port.Commits
	Parser      port.SyntaxParser
	Constraints *ConstraintIndex
	Sessions    *SessionIndex
	Changes     *ChangeIndex
	IDs         port.IDGen
	Repo        string
	// SessionWindow bounds how far back sessions are bundled into the change.
	SessionWindow time.Duration
}

// Derive derives the Change for sha. Idempotent per commit.
func (d *Deriver) Derive(ctx context.Context, actor core.ActorRef, sha string) (Derived, error) {
	commit, err := d.Commits.Commit(ctx, sha)
	if err != nil {
		return Derived{}, err
	}
	changes, err := d.Commits.ChangedIn(ctx, commit.SHA)
	if err != nil {
		return Derived{}, err
	}
	out := Derived{SHA: commit.SHA}
	parent := ""
	if len(commit.Parents) > 0 {
		parent = commit.Parents[0]
	}
	anchorSet := map[string]bool{}
	for _, fc := range changes {
		out.Files = append(out.Files, fc.Path)
		anchors, err := d.changedAnchors(ctx, parent, commit.SHA, fc)
		if err != nil {
			return out, err
		}
		for _, a := range anchors {
			anchorSet[a] = true
		}
	}
	for a := range anchorSet {
		out.Anchors = append(out.Anchors, a)
	}
	sort.Strings(out.Anchors)

	// impact: constraints bound to the anchors or in force for the paths
	seen := map[core.ID]bool{}
	var impact []accountability.ImpactRef
	add := func(c ConstraintSummary) {
		if seen[c.ID] || (c.State != harness.StateVerified && c.State != harness.StateReview) {
			return
		}
		seen[c.ID] = true
		impact = append(impact, accountability.ImpactRef{Constraint: c.ID, Relation: accountability.RelReferences})
		out.Constraints = append(out.Constraints, c.ID)
	}
	for _, a := range out.Anchors {
		for _, c := range d.Constraints.BoundTo(a) {
			add(c)
		}
	}
	// scope-wide constraints (no anchors of their own) are impacted through
	// the paths; anchored ones only through their anchors (ADR 0013)
	for _, f := range out.Files {
		for _, c := range d.Constraints.InForce(core.ScopeKey{Repo: d.Repo, PathGlob: f}) {
			if len(c.Anchors) == 0 {
				add(c)
			}
		}
	}
	sort.Slice(impact, func(i, j int) bool { return impact[i].Constraint < impact[j].Constraint })
	sort.Slice(out.Constraints, func(i, j int) bool { return out.Constraints[i] < out.Constraints[j] })

	window := d.SessionWindow
	if window == 0 {
		window = 24 * time.Hour
	}
	if d.Sessions != nil {
		out.Sessions = d.Sessions.Touching(d.Repo, out.Files, commit.At.Add(-window))
	}
	mode := accountability.ModeManual
	if len(out.Sessions) > 0 {
		mode = accountability.ModeSession
	}

	// one Change per commit: the proposal ref is the commit
	if existing, ok := d.Changes.ByRef(commit.SHA); ok {
		out.ChangeID, out.Replayed, out.Settled = existing.ID, true, existing.State == accountability.StateSettled
		return out, nil
	}
	id := d.IDs.New()
	open := accountability.OpenChange{
		ChangeCmd:  accountability.ChangeCmd{ID: id, Idem: "derive:" + commit.SHA},
		ChangeKind: accountability.KindRealization, Mode: mode, Scope: core.ScopeKey{Repo: d.Repo}, NoIntent: true,
		Impact:   accountability.Impact{Constraints: impact, Anchors: out.Anchors},
		Proposal: &accountability.Proposal{Ref: commit.SHA, Actor: actor, At: commit.At},
		Sessions: out.Sessions,
	}
	res, err := d.Pipeline.Handle(ctx, actor, open)
	if err != nil {
		return out, fmt.Errorf("derive %s: %w", commit.SHA, err)
	}
	if res.Replayed {
		if existing, ok := d.Changes.ByRef(commit.SHA); ok {
			out.ChangeID, out.Replayed, out.Settled = existing.ID, true, existing.State == accountability.StateSettled
			return out, nil
		}
	}
	out.ChangeID = id
	if len(impact) == 0 {
		if _, err := d.Pipeline.Handle(ctx, actor, accountability.Settle{ChangeCmd: accountability.ChangeCmd{ID: id, Idem: "derive:settle:" + commit.SHA}}); err != nil {
			return out, fmt.Errorf("settle %s: %w", commit.SHA, err)
		}
		out.Settled = true
	}
	return out, nil
}

// changedAnchors lists the anchors a file change touches: for parsed
// languages the symbols whose signature or body changed, appeared or
// disappeared; otherwise the file anchor. A file-only hash change (a
// comment elsewhere) touches nothing.
func (d *Deriver) changedAnchors(ctx context.Context, parent, sha string, fc port.FileChange) ([]string, error) {
	fileAnchor := core.Anchor{Scheme: core.SchemeCode, Path: fc.Path}.String()
	if d.Parser == nil || !d.Parser.Supports(fc.Path) {
		return []string{fileAnchor}, nil
	}
	before := map[string]core.Hash3{}
	if parent != "" && fc.Status != "A" {
		oldPath := fc.Path
		if fc.OldPath != "" {
			oldPath = fc.OldPath
		}
		src, ok, err := d.Commits.Content(ctx, parent, oldPath)
		if err != nil {
			return nil, err
		}
		if ok {
			if before, err = d.symbolHashes(ctx, fc.Path, src); err != nil {
				return nil, err
			}
		}
	}
	after := map[string]core.Hash3{}
	if fc.Status != "D" {
		src, ok, err := d.Commits.Content(ctx, sha, fc.Path)
		if err != nil {
			return nil, err
		}
		if ok {
			if after, err = d.symbolHashes(ctx, fc.Path, src); err != nil {
				return nil, err
			}
		}
	}
	var out []string
	for a, h := range before {
		if nh, ok := after[a]; !ok || h.Cmp(nh) >= core.HashBody {
			out = append(out, a)
		}
	}
	for a := range after {
		if _, ok := before[a]; !ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 && (fc.Status == "A" || fc.Status == "D") {
		out = append(out, fileAnchor)
	}
	sort.Strings(out)
	return out, nil
}

func (d *Deriver) symbolHashes(ctx context.Context, path string, src []byte) (map[string]core.Hash3, error) {
	syn, matches, err := d.Parser.Parse(ctx, path, src)
	if err != nil {
		return nil, fmt.Errorf("derive: parse %s: %w", path, err)
	}
	file := realization.FileHash(src)
	out := map[string]core.Hash3{}
	for _, s := range realization.ExtractSymbols(syn, matches) {
		out[core.Anchor{Scheme: core.SchemeCode, Path: path, Symbol: s.Name}.String()] = realization.Hash3Of(s, file)
	}
	return out, nil
}
