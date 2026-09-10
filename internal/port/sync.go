package port

import (
	"context"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
)

// Sync protocol objects (architecture-patterns-v0 §9, spec §4.4). The
// daemon pushes signed records as proposals; the server verifies the
// daemon's signature, drops duplicates by origin, re-checks stream
// continuity and either appends (keeping the original signature) or
// records a Rejected event. Pull hands back the team layer: constraint,
// decision, scope and anchor streams.

// PushRequest is POST /sync/push.
type PushRequest struct {
	DaemonID string          `json:"daemon_id"`
	Events   []core.Envelope `json:"events"`
}

// PushAccepted is one accepted record: its local seq and the server seq.
type PushAccepted struct {
	LocalSeq  int64 `json:"local_seq"`
	Seq       int64 `json:"seq"`
	Duplicate bool  `json:"duplicate,omitempty"`
}

// PushRejected is one refused record with the reason (also in the ledger
// as a Rejected event unless the failure was authentication).
type PushRejected struct {
	LocalSeq int64  `json:"local_seq"`
	Code     string `json:"code"`
	Reason   string `json:"reason"`
}

// PushResponse lists what happened to each pushed record.
type PushResponse struct {
	Accepted []PushAccepted `json:"accepted"`
	Rejected []PushRejected `json:"rejected"`
	Head     int64          `json:"head"`
}

// PullResponse is GET /sync/pull: team-layer records after the cursor.
// Cursor is the last server seq scanned (not returned): the client stores
// it and asks again from there.
type PullResponse struct {
	Cursor int64           `json:"cursor"`
	Events []core.Envelope `json:"events"`
	More   bool            `json:"more,omitempty"`
}

// GateRequest is POST /gates.
type GateRequest struct {
	Scope          core.ScopeKey `json:"scope"`
	Anchors        []string      `json:"anchors,omitempty"`
	Action         string        `json:"action,omitempty"`
	ProposalRef    string        `json:"proposal_ref"`
	RequestedLevel core.Level    `json:"requested_level"`
}

// GateResolution is POST /gates/{id}/resolve.
type GateResolution struct {
	Decision accountability.GateDecision `json:"decision"`
	Reason   string                      `json:"reason"`
}

// DeviceStart is the first step of the device login (RFC 8628 / GitHub).
type DeviceStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DevicePoll is one poll of the device login: pending until the user
// approves, then the issued keelage token.
type DevicePoll struct {
	Status   string `json:"status"` // pending | slow_down | ok | denied | expired
	Interval int    `json:"interval,omitempty"`
	Token    string `json:"token,omitempty"`
	User     string `json:"user,omitempty"`
	Org      string `json:"org,omitempty"`
}

// TeamService is the server side of the team API, after authentication.
type TeamService interface {
	Push(ctx context.Context, p Principal, req PushRequest) (PushResponse, error)
	Pull(ctx context.Context, p Principal, since int64, limit int) (PullResponse, error)
	RegisterDaemon(ctx context.Context, p Principal, id, publicKey string) error
	RequestGate(ctx context.Context, p Principal, req GateRequest) (accountability.Gate, error)
	ResolveGate(ctx context.Context, p Principal, id core.ID, res GateResolution) (accountability.Gate, error)
	ListGates(ctx context.Context, p Principal, state accountability.GateState) ([]accountability.Gate, error)
}

// TeamClient is the daemon side of the same API (the token travels with
// the client).
type TeamClient interface {
	Push(ctx context.Context, req PushRequest) (PushResponse, error)
	Pull(ctx context.Context, since int64, limit int) (PullResponse, error)
	RegisterDaemon(ctx context.Context, id, publicKey string) error
	RequestGate(ctx context.Context, req GateRequest) (accountability.Gate, error)
	ResolveGate(ctx context.Context, id core.ID, res GateResolution) (accountability.Gate, error)
	ListGates(ctx context.Context, state accountability.GateState) ([]accountability.Gate, error)
}

// SignatureVerifier checks a record signature against a textual public key.
type SignatureVerifier interface {
	Verify(publicKey string, hash, sig []byte) bool
	// ValidKey reports whether the textual key parses.
	ValidKey(publicKey string) bool
}

// TokenIssuer mints bearer tokens (the server after a device login).
type TokenIssuer interface {
	IssueToken(ctx context.Context, org, user, kind, owner, label string, ttl time.Duration) (string, error)
}

// Membership answers whether a user belongs to an org.
type Membership interface {
	IsMember(ctx context.Context, org, user string) (bool, error)
}

// OAuthDevice is the identity provider's device flow (GitHub in v0).
type OAuthDevice interface {
	// Start asks the provider for device and user codes.
	Start(ctx context.Context) (DeviceStart, error)
	// Poll asks whether the user approved; on success it returns the
	// provider's login for the user. status follows DevicePoll.Status.
	Poll(ctx context.Context, deviceCode string) (status string, login string, interval int, err error)
}
