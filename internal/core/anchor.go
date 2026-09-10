package core

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// AnchorScheme is the realization kind an anchor points at (spec §3.2).
type AnchorScheme string

const (
	SchemeCode     AnchorScheme = "code"     // code://<path>#<symbol>
	SchemeRoute    AnchorScheme = "route"    // route://<METHOD> <path>
	SchemeUI       AnchorScheme = "ui"       // ui://<Component>
	SchemeSchema   AnchorScheme = "schema"   // schema://<Type>
	SchemeInfra    AnchorScheme = "infra"    // infra://<tool>:<resource>
	SchemeDoc      AnchorScheme = "doc"      // doc://<doc_id>
	SchemeContract AnchorScheme = "contract" // contract://<group>/<key>
)

// Anchor is a parsed realization anchor. Persistent objects point at anchors,
// never the other way round. Construct only through ParseAnchor; compare with
// Equal (canonical string).
type Anchor struct {
	Scheme AnchorScheme `json:"scheme"`
	Path   string       `json:"path"`
	Symbol string       `json:"symbol,omitempty"`
}

// ParseAnchor parses and validates the canonical string form.
func ParseAnchor(s string) (Anchor, error) {
	schemeStr, rest, ok := strings.Cut(s, "://")
	if !ok || rest == "" {
		return Anchor{}, fmt.Errorf("anchor: %q is not <scheme>://<path>", s)
	}
	if strings.ContainsAny(rest, "\n\r\t") {
		return Anchor{}, fmt.Errorf("anchor: %q contains control characters", s)
	}
	a := Anchor{Scheme: AnchorScheme(schemeStr)}
	switch a.Scheme {
	case SchemeCode:
		p, sym, _ := strings.Cut(rest, "#")
		if err := validateRepoPath(p); err != nil {
			return Anchor{}, fmt.Errorf("anchor: %w", err)
		}
		if strings.Contains(sym, "#") || strings.ContainsAny(sym, " ") {
			return Anchor{}, fmt.Errorf("anchor: bad symbol %q", sym)
		}
		a.Path, a.Symbol = p, sym
	case SchemeRoute:
		method, p, ok := strings.Cut(rest, " ")
		if !ok || method == "" || method != strings.ToUpper(method) || !strings.HasPrefix(p, "/") {
			return Anchor{}, fmt.Errorf("anchor: route must be \"<METHOD> /path\", got %q", rest)
		}
		a.Path = method + " " + p
	case SchemeUI, SchemeSchema, SchemeInfra, SchemeDoc, SchemeContract:
		if strings.ContainsAny(rest, " #") {
			return Anchor{}, fmt.Errorf("anchor: %q contains a reserved character", rest)
		}
		a.Path = rest
	default:
		return Anchor{}, fmt.Errorf("anchor: unknown scheme %q", schemeStr)
	}
	return a, nil
}

func validateRepoPath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must be repo-relative", p)
	}
	if path.Clean(p) != p {
		return fmt.Errorf("path %q is not clean", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q escapes the repo", p)
		}
	}
	return nil
}

// String is the canonical form.
func (a Anchor) String() string {
	s := string(a.Scheme) + "://" + a.Path
	if a.Symbol != "" {
		s += "#" + a.Symbol
	}
	return s
}

// Equal compares canonical forms.
func (a Anchor) Equal(b Anchor) bool { return a.String() == b.String() }

// IsFile reports whether this is a whole-file code anchor (no symbol).
func (a Anchor) IsFile() bool { return a.Scheme == SchemeCode && a.Symbol == "" }
