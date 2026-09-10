package app

import (
	"context"
	"fmt"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// Rebuild replays the whole ledger into the projectors (projection reset
// = offset 0). The daemon and headless commands call it at startup: the
// daemon's projections live in memory only.
func Rebuild(ctx context.Context, ledger port.Ledger, codec *core.Codec, projectors ...port.Projector) (int64, error) {
	return Replay(ctx, ledger, codec, 0, projectors...)
}

// Replay projects records after seq and returns the last seq seen, so a
// long-running daemon can catch up on writes other processes (the CLI)
// made to the same ledger.
func Replay(ctx context.Context, ledger port.Ledger, codec *core.Codec, seq int64, projectors ...port.Projector) (int64, error) {
	const page = 500
	for {
		batch, err := ledger.ReadAll(ctx, seq+1, page)
		if err != nil {
			return seq, fmt.Errorf("rebuild: read from %d: %w", seq+1, err)
		}
		if len(batch) == 0 {
			return seq, nil
		}
		for _, e := range batch {
			ev, err := codec.Decode(e.Kind, e.V, e.Body)
			if err != nil {
				return seq, fmt.Errorf("rebuild: seq %d: %w", e.Seq, err)
			}
			for _, p := range projectors {
				if err := p.Handle(ctx, e, ev); err != nil {
					return seq, fmt.Errorf("rebuild: %s at seq %d: %w", p.Name(), e.Seq, err)
				}
			}
			seq = e.Seq
		}
		if len(batch) < page {
			return seq, nil
		}
	}
}
