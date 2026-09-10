package supply

import "time"

// ManifestPath is the sidecar's location inside a repository.
const ManifestPath = ".keelage/manifest.json"

// ManifestVersion is the current sidecar schema.
const ManifestVersion = 1

// Import records a file absorbed into the ledger: which object it became
// and the file hash at that time, so a later `init` knows what changed.
type Import struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"` // rule | adr
	ObjectID string `json:"object_id"`
	Hash     string `json:"hash"`
	Scope    string `json:"scope"`
}

// RenderedFile records a file (or managed block) keelage wrote and the
// hash it had, so divergence (a user edit inside our output) is detected.
type RenderedFile struct {
	Path    string `json:"path"`
	Tool    string `json:"tool"`
	Hash    string `json:"hash"`
	Managed bool   `json:"managed,omitempty"`
	BlockID string `json:"block_id,omitempty"`
}

// Manifest is the repository sidecar (implementation plan §3.2).
type Manifest struct {
	Version   int               `json:"version"`
	Repo      string            `json:"repo"`
	Adapters  map[string]string `json:"adapters,omitempty"` // tool -> adapter version
	Imports   []Import          `json:"imports,omitempty"`
	Rendered  []RenderedFile    `json:"rendered,omitempty"`
	Gitignore bool              `json:"gitignore_added,omitempty"` // we added .keelage/context/ to .gitignore
	UpdatedAt time.Time         `json:"updated_at"`
}

// FindImport returns the import record for path.
func (m *Manifest) FindImport(path string) (Import, bool) {
	for _, i := range m.Imports {
		if i.Path == path {
			return i, true
		}
	}
	return Import{}, false
}

// SetImport upserts an import record.
func (m *Manifest) SetImport(i Import) {
	for k, x := range m.Imports {
		if x.Path == i.Path {
			m.Imports[k] = i
			return
		}
	}
	m.Imports = append(m.Imports, i)
}

// FindRendered returns the rendered record for path.
func (m *Manifest) FindRendered(path string) (RenderedFile, bool) {
	for _, r := range m.Rendered {
		if r.Path == path {
			return r, true
		}
	}
	return RenderedFile{}, false
}

// SetRendered upserts a rendered record.
func (m *Manifest) SetRendered(r RenderedFile) {
	for k, x := range m.Rendered {
		if x.Path == r.Path {
			m.Rendered[k] = r
			return
		}
	}
	m.Rendered = append(m.Rendered, r)
}

// ImportedObjectIDs lists the objects that came from this repository's own
// files (they are not rendered back into it).
func (m *Manifest) ImportedObjectIDs() map[string]bool {
	out := map[string]bool{}
	for _, i := range m.Imports {
		out[i.ObjectID] = true
	}
	return out
}
