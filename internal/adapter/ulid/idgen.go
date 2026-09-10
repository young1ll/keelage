// Package ulid is the production port.IDGen.
package ulid

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/young1ll/keelage/internal/core"
)

// IDGen generates monotonic ULIDs.
type IDGen struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// New returns a generator.
func New() *IDGen { return &IDGen{entropy: ulid.Monotonic(rand.Reader, 0)} }

// New implements port.IDGen.
func (g *IDGen) New() core.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return core.ID(ulid.MustNew(ulid.Timestamp(time.Now()), g.entropy).String())
}
