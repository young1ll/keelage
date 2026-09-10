package app

import (
	"context"
	"fmt"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// appendRejected records a refused command or synced record on the
// Rejected stream (ADR 0010: conflicts are kept, never dropped) and hands
// the record to the projectors.
func appendRejected(ctx context.Context, ledger port.Ledger, codec *core.Codec, projectors []port.Projector, now time.Time, ev core.Rejected) error {
	body, err := codec.Encode(ev)
	if err != nil {
		return err
	}
	e := core.Envelope{
		Kind: ev.Kind(), V: ev.Version(), TS: now, Actor: ev.Actor,
		Meta: core.EventMeta{Refs: []string{ev.Target}}, Body: body,
	}
	rng, err := ledger.Append(ctx, core.RejectedStream, port.AnyVersion, []core.Envelope{e})
	if err != nil {
		return fmt.Errorf("app: record rejection: %w", err)
	}
	written, err := ledger.Read(ctx, core.RejectedStream, rng.FromVer)
	if err != nil || len(written) == 0 {
		return nil
	}
	for _, p := range projectors {
		_ = p.Handle(ctx, written[0], ev)
	}
	return nil
}
