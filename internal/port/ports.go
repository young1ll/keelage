package port

import (
	"context"
	"time"

	"github.com/young1ll/keelage/internal/core"
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
