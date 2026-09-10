package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/port"
)

// ErrProjection wraps a projector failure that happened after a successful
// append: the command took effect; the read model needs a rebuild.
var ErrProjection = errors.New("app: projection failed")

// ErrUnknownCommand: no aggregate is registered for the command kind.
var ErrUnknownCommand = errors.New("app: unknown command")

// ContextSource resolves the external facts a decision needs (effective
// autonomy, cap, constraint snapshot, required judges) from projections.
// The pipeline fills Now, Actor and Policy itself.
type ContextSource interface {
	Build(ctx context.Context, cmd core.Command) (core.DecideContext, error)
}

// Result is what a handled command produced.
type Result struct {
	Stream  string
	Version int64
	Range   port.Range
	Events  []core.Event
	// Replayed is true when the command's idempotency key had already been
	// applied: nothing new was written and Version is the earlier result.
	Replayed bool
}

// Options configures a Pipeline.
type Options struct {
	Ledger     port.Ledger
	Codec      *core.Codec
	Clock      port.Clock
	Context    ContextSource
	Policy     core.Policy
	Projectors []port.Projector
}

type handlerFn func(ctx context.Context, actor core.ActorRef, cmd core.Command) (Result, error)

// Pipeline is the command pipeline (architecture-patterns-v0 §3):
//
//	1 authorize  2 load  3 context  4 decide  5 append  6 publish  7 respond
type Pipeline struct {
	o        Options
	handlers map[string]handlerFn
}

// New builds a pipeline. Aggregates are wired with Register.
func New(o Options) *Pipeline {
	if o.Policy == (core.Policy{}) {
		o.Policy = core.DefaultPolicy()
	}
	return &Pipeline{o: o, handlers: map[string]handlerFn{}}
}

// Register wires an aggregate type for the given command kinds. zero
// returns the empty aggregate for a stream (e.g. NewScope(key)).
func Register[T core.Root[T]](p *Pipeline, zero func(stream string) T, kinds ...string) {
	for _, k := range kinds {
		if _, dup := p.handlers[k]; dup {
			panic("app: command kind registered twice: " + k)
		}
		p.handlers[k] = func(ctx context.Context, actor core.ActorRef, cmd core.Command) (Result, error) {
			return handle(ctx, p, zero, actor, cmd)
		}
	}
}

// Handle runs one command through the pipeline.
func (p *Pipeline) Handle(ctx context.Context, actor core.ActorRef, cmd core.Command) (Result, error) {
	h, ok := p.handlers[cmd.Kind()]
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownCommand, cmd.Kind())
	}
	return h(ctx, actor, cmd)
}

func handle[T core.Root[T]](ctx context.Context, p *Pipeline, zero func(string) T, actor core.ActorRef, cmd core.Command) (Result, error) {
	stream := cmd.Stream()
	now := p.o.Clock.Now()

	// 1 authorize (coarse; fine-grained rules live in Decide)
	if err := actor.Validate(); err != nil {
		rej := core.Reject("unauthenticated", "%v", err)
		return Result{Stream: stream}, p.recordRejection(ctx, stream, actor, cmd, now, rej)
	}

	// idempotent replay: a retried command (hook, sync) returns the earlier
	// result before any rule runs, so "already exists" never masks a retry.
	if key := cmd.IdempotencyKey(); key != "" {
		prev, err := p.o.Ledger.Lookup(ctx, stream, key)
		switch {
		case err == nil:
			return Result{Stream: stream, Version: prev.Ver, Replayed: true}, nil
		case !errors.Is(err, port.ErrNotFound):
			return Result{}, fmt.Errorf("app: idempotency lookup: %w", err)
		}
	}

	// 2 load
	envs, err := p.o.Ledger.Read(ctx, stream, 0)
	if err != nil {
		return Result{}, fmt.Errorf("app: load %s: %w", stream, err)
	}
	agg := zero(stream)
	var version int64
	for _, e := range envs {
		ev, err := p.o.Codec.Decode(e.Kind, e.V, e.Body)
		if err != nil {
			return Result{}, fmt.Errorf("app: replay %s seq %d: %w", stream, e.Seq, err)
		}
		agg = agg.Apply(ev)
		version = e.Ver
	}

	// 3 context
	dctx, err := p.o.Context.Build(ctx, cmd)
	if err != nil {
		return Result{}, fmt.Errorf("app: context for %s: %w", cmd.Kind(), err)
	}
	dctx.Now, dctx.Actor = now, actor
	if dctx.Policy == (core.Policy{}) {
		dctx.Policy = p.o.Policy
	}

	// 4 decide
	events, err := agg.Decide(cmd, dctx)
	if err != nil {
		if _, isRej := core.AsRejection(err); isRej {
			return Result{Stream: stream, Version: version}, p.recordRejection(ctx, stream, actor, cmd, now, err)
		}
		return Result{}, fmt.Errorf("app: decide %s: %w", cmd.Kind(), err)
	}
	if len(events) == 0 {
		return Result{Stream: stream, Version: version}, nil
	}

	// 5 append
	refs := []string{stream}
	if a, ok := cmd.(core.Anchored); ok {
		refs = append(refs, a.TouchedAnchors()...)
	}
	batch := make([]core.Envelope, 0, len(events))
	for i, ev := range events {
		body, err := p.o.Codec.Encode(ev)
		if err != nil {
			return Result{}, fmt.Errorf("app: encode %s: %w", ev.Kind(), err)
		}
		e := core.Envelope{
			Kind: ev.Kind(), V: ev.Version(), TS: now, Actor: actor,
			Meta: core.EventMeta{Refs: refs, ConstraintsHash: dctx.ConstraintsHash, Autonomy: dctx.Autonomy},
			Body: body,
		}
		if i == 0 {
			e.Idem = cmd.IdempotencyKey()
		}
		batch = append(batch, e)
	}
	rng, err := p.o.Ledger.Append(ctx, stream, version, batch)
	switch {
	case errors.Is(err, port.ErrDuplicateIdempotencyKey):
		prev, lerr := p.o.Ledger.Lookup(ctx, stream, cmd.IdempotencyKey())
		if lerr != nil {
			return Result{}, fmt.Errorf("app: idempotent replay lookup: %w", lerr)
		}
		return Result{Stream: stream, Version: prev.Ver, Replayed: true}, nil
	case err != nil:
		return Result{}, fmt.Errorf("app: append %s: %w", stream, err)
	}

	// 6 publish (synchronous projections)
	res := Result{Stream: stream, Version: rng.ToVer, Range: rng, Events: events}
	written, err := p.o.Ledger.Read(ctx, stream, rng.FromVer)
	if err != nil {
		return res, fmt.Errorf("%w: reread: %w", ErrProjection, err)
	}
	if perr := p.publish(ctx, written, events); perr != nil {
		return res, perr
	}

	// 7 respond
	return res, nil
}

func (p *Pipeline) publish(ctx context.Context, envs []core.Envelope, events []core.Event) error {
	var errs []error
	for i, e := range envs {
		if i >= len(events) {
			break
		}
		for _, pr := range p.o.Projectors {
			if err := pr.Handle(ctx, e, events[i]); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", pr.Name(), err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrProjection, errors.Join(errs...))
	}
	return nil
}

// recordRejection appends a Rejected event (never a silent failure) and
// returns the original rejection.
func (p *Pipeline) recordRejection(ctx context.Context, target string, actor core.ActorRef, cmd core.Command, now time.Time, rej error) error {
	r, _ := core.AsRejection(rej)
	ev := core.Rejected{Command: cmd.Kind(), Target: target, Code: r.Code, Reason: r.Reason, Actor: actor}
	body, err := p.o.Codec.Encode(ev)
	if err != nil {
		return errors.Join(rej, err)
	}
	e := core.Envelope{
		Kind: ev.Kind(), V: ev.Version(), TS: now, Actor: actor,
		Meta: core.EventMeta{Refs: []string{target}}, Body: body,
	}
	if _, err := p.o.Ledger.Append(ctx, core.RejectedStream, port.AnyVersion, []core.Envelope{e}); err != nil {
		return errors.Join(rej, fmt.Errorf("app: record rejection: %w", err))
	}
	written, err := p.o.Ledger.Read(ctx, core.RejectedStream, 0)
	if err == nil && len(written) > 0 {
		_ = p.publish(ctx, written[len(written)-1:], []core.Event{ev})
	}
	return rej
}
