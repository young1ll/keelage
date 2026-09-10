package port

import (
	"context"
	"errors"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/core/supply"
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

// HookService answers canonical hook events within the hook budget; the
// caller's context carries the deadline. Errors and timeouts are fail-open
// on the tool side: the edit proceeds without context.
type HookService interface {
	Hook(ctx context.Context, ev supply.Event) (supply.Response, error)
}

// ContextQuery is the read side the local MCP tools use.
type ContextQuery interface {
	// WhatTouches resolves the constraints bound to an anchor; repo (the
	// repository id, "" if unknown) scopes path-level constraints.
	WhatTouches(ctx context.Context, anchor, repo string) (supply.Touches, error)
	Related(ctx context.Context, id string) (supply.Related, error)
}

// Commit is what the derivation step needs to know about one commit.
type Commit struct {
	SHA         string
	Parents     []string
	AuthorEmail string
	Message     string
	At          time.Time
}

// FileChange is one file touched by a commit. Status is git's letter:
// A added, M modified, D deleted, R renamed (OldPath set).
type FileChange struct {
	Path    string
	OldPath string
	Status  string
}

// Commits reads commit boundaries from the repository (git).
type Commits interface {
	Commit(ctx context.Context, ref string) (Commit, error)
	ChangedIn(ctx context.Context, sha string) ([]FileChange, error)
	// Content returns the file at a commit; ok is false when it does not exist there.
	Content(ctx context.Context, sha, path string) (content []byte, ok bool, err error)
}

// ErrUnauthenticated: no valid credential.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrForbidden: authenticated, but not allowed to do this.
var ErrForbidden = errors.New("forbidden")

// ErrDaemonKeyMismatch: a daemon id is already bound to another key.
var ErrDaemonKeyMismatch = errors.New("daemon id is registered with a different key")

// Principal is who a request acts as, after authentication.
type Principal struct {
	Org   string
	User  string
	Kind  string // human | agent | ci
	Owner string // responsible human (== User for humans)
}

// Actor is the core actor reference for the principal.
func (p Principal) Actor() core.ActorRef {
	if p.Kind == "human" {
		return core.ActorRef{Kind: core.ActorHuman, ID: core.ID(p.User)}
	}
	return core.ActorRef{Kind: core.ActorAgent, ID: core.ID(p.Kind + ":" + p.User), Owner: core.ID(p.Owner)}
}

// Authenticator resolves bearer tokens.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Principal, error)
}

// DaemonRegistry maps a daemon id to its public key and registering user.
type DaemonRegistry interface {
	RegisterDaemon(ctx context.Context, org, id, publicKey, user string) error
	DaemonKey(ctx context.Context, org, id string) (publicKey, user string, err error)
}

// OriginLedger is a ledger that can look up synced origins: the server seq
// of the record from (daemon, local seq), or ErrNotFound.
type OriginLedger interface {
	Ledger
	FindOrigin(ctx context.Context, daemonID string, localSeq int64) (int64, error)
}
