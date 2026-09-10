package core

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// ScopeKey is a coordinate in the scope lattice (ADR 0006):
// org × product × team × (repo, path glob) × person. Product and team are
// orthogonal axes, not a tree. An unset axis means "any".
type ScopeKey struct {
	Org      string `json:"org,omitempty"`
	Product  string `json:"product,omitempty"`
	Team     string `json:"team,omitempty"`
	Repo     string `json:"repo,omitempty"`
	PathGlob string `json:"path,omitempty"`
	Person   string `json:"person,omitempty"`
}

// ObjectKind selects the resolution order: constraints are wide-first (a
// wider scope binds narrower ones), context is narrow-first (a narrower scope
// specialises wider ones).
type ObjectKind int

const (
	KindConstraint ObjectKind = iota + 1
	KindContext
)

const scopeSep = "|"

// String is the canonical form, always listing all six axes:
// "org=a|product=b|team=|repo=|path=|person=".
func (k ScopeKey) String() string {
	return "org=" + k.Org + scopeSep + "product=" + k.Product + scopeSep + "team=" + k.Team +
		scopeSep + "repo=" + k.Repo + scopeSep + "path=" + k.PathGlob + scopeSep + "person=" + k.Person
}

// ParseScopeKey parses the canonical form produced by String.
func ParseScopeKey(s string) (ScopeKey, error) {
	parts := strings.Split(s, scopeSep)
	if len(parts) != 6 {
		return ScopeKey{}, fmt.Errorf("scope: expected 6 axes, got %d", len(parts))
	}
	var k ScopeKey
	for i, want := range []string{"org", "product", "team", "repo", "path", "person"} {
		name, val, ok := strings.Cut(parts[i], "=")
		if !ok || name != want {
			return ScopeKey{}, fmt.Errorf("scope: axis %d must be %q", i, want)
		}
		switch want {
		case "org":
			k.Org = val
		case "product":
			k.Product = val
		case "team":
			k.Team = val
		case "repo":
			k.Repo = val
		case "path":
			k.PathGlob = val
		case "person":
			k.Person = val
		}
	}
	return k, k.Validate()
}

// IsZero reports whether no axis is set (the global scope).
func (k ScopeKey) IsZero() bool { return k == ScopeKey{} }

// Validate checks axis values: no separator characters, a path glob needs a
// repo, and the glob must be syntactically valid.
func (k ScopeKey) Validate() error {
	for _, v := range []string{k.Org, k.Product, k.Team, k.Repo, k.PathGlob, k.Person} {
		if strings.ContainsAny(v, scopeSep+"=\n") {
			return fmt.Errorf("scope: axis value %q contains a reserved character", v)
		}
	}
	if k.PathGlob != "" {
		if k.Repo == "" {
			return errors.New("scope: path glob requires repo")
		}
		if _, err := path.Match(k.PathGlob, ""); err != nil {
			return fmt.Errorf("scope: bad path glob %q: %w", k.PathGlob, err)
		}
	}
	return nil
}

// Width is the number of unset axes: 6 is the global scope, 0 the narrowest.
func (k ScopeKey) Width() int {
	w := 0
	for _, v := range []string{k.Org, k.Product, k.Team, k.Repo, k.PathGlob, k.Person} {
		if v == "" {
			w++
		}
	}
	return w
}

// Contains reports whether k applies to target: every axis set on k must be
// matched by target. A path glob on k matches target's path (treated as a
// concrete path) with path.Match, or is equal to it.
func (k ScopeKey) Contains(target ScopeKey) bool {
	if k.Org != "" && k.Org != target.Org {
		return false
	}
	if k.Product != "" && k.Product != target.Product {
		return false
	}
	if k.Team != "" && k.Team != target.Team {
		return false
	}
	if k.Repo != "" && k.Repo != target.Repo {
		return false
	}
	if k.Person != "" && k.Person != target.Person {
		return false
	}
	if k.PathGlob != "" {
		if target.PathGlob == "" || k.PathGlob == target.PathGlob {
			return target.PathGlob != ""
		}
		ok, err := path.Match(k.PathGlob, target.PathGlob)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// Resolve returns the keys that apply to target, ordered for kind:
// constraints wide→narrow, context narrow→wide. Ties (same width) are broken
// by the canonical string, which puts product before team. Duplicates are
// removed. Pure function.
func Resolve(keys []ScopeKey, target ScopeKey, kind ObjectKind) []ScopeKey {
	seen := make(map[string]struct{}, len(keys))
	out := make([]ScopeKey, 0, len(keys))
	for _, k := range keys {
		if !k.Contains(target) {
			continue
		}
		s := k.String()
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, k)
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := out[i].Width(), out[j].Width()
		if wi != wj {
			if kind == KindContext {
				return wi < wj
			}
			return wi > wj
		}
		return out[i].String() < out[j].String()
	})
	return out
}
