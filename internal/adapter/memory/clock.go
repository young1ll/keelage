package memory

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/young1ll/keelage/internal/core"
)

// Clock is a settable clock for tests.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock starts at t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now implements port.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set sets the clock.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// IDGen yields sequential, sortable IDs ("id-000001") for deterministic tests.
type IDGen struct {
	n      atomic.Int64
	Prefix string
}

// New implements port.IDGen.
func (g *IDGen) New() core.ID {
	p := g.Prefix
	if p == "" {
		p = "id"
	}
	return core.ID(fmt.Sprintf("%s-%06d", p, g.n.Add(1)))
}
