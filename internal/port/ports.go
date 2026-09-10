package port

import (
	"context"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
)

// Clock supplies time so decisions are deterministic in tests.
type Clock interface {
	Now() time.Time
}

// IDGen supplies sortable IDs (ULID in production).
type IDGen interface {
	New() core.ID
}

// Signer signs envelope hashes with the daemon's (or actor's) key.
type Signer interface {
	Sign(hash []byte) ([]byte, error)
	// Fingerprint identifies the key, for ActorRef.Key.
	Fingerprint() string
}

// Projector keeps a read model up to date. Synchronous projectors run in
// the pipeline right after Append; asynchronous ones catch up via ReadAll.
type Projector interface {
	Name() string
	Handle(ctx context.Context, e core.Envelope, ev core.Event) error
}

// SyntaxParser turns a source file into a language-neutral syntax tree and
// the symbol matches of the language's symbols query (tree-sitter today).
type SyntaxParser interface {
	// Supports reports whether the file's language has a parser.
	Supports(file string) bool
	// LanguageName names the grammar used for the file ("" if unsupported).
	LanguageName(file string) string
	Parse(ctx context.Context, file string, src []byte) (realization.Syntax, []realization.SymbolMatch, error)
}
