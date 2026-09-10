package app

import (
	"context"
	"sync"

	"github.com/young1ll/keelage/internal/core"
)

// Cursor is a projector that remembers the highest seq it saw, so a
// runtime that projects synchronously through the pipeline and also
// catches up from the ledger never applies a record twice.
type Cursor struct {
	mu  sync.Mutex
	seq int64
}

// Name implements port.Projector.
func (*Cursor) Name() string { return "cursor" }

// Handle implements port.Projector.
func (c *Cursor) Handle(_ context.Context, e core.Envelope, _ core.Event) error {
	c.mu.Lock()
	if e.Seq > c.seq {
		c.seq = e.Seq
	}
	c.mu.Unlock()
	return nil
}

// Seq is the last projected seq.
func (c *Cursor) Seq() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}
