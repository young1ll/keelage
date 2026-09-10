package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

type codecKey struct {
	kind string
	v    int
}

// Upcaster converts an event of one version to the next.
type Upcaster func(Event) Event

// Codec maps (kind, version) to concrete event types and applies upcasters
// so callers always see the latest version. Each bounded context registers
// its events; the registry itself is standard-library only.
type Codec struct {
	types     map[codecKey]reflect.Type
	latest    map[string]int
	upcasters map[codecKey]Upcaster
}

// NewCodec returns an empty codec with the core Rejected event registered.
func NewCodec() *Codec {
	c := &Codec{
		types:     map[codecKey]reflect.Type{},
		latest:    map[string]int{},
		upcasters: map[codecKey]Upcaster{},
	}
	c.Register(Rejected{})
	return c
}

// Register records the concrete type of proto (a struct value, not a
// pointer) under its Kind and Version. Registering the same (kind, version)
// twice panics: it is a programming error.
func (c *Codec) Register(proto Event) {
	t := reflect.TypeOf(proto)
	if t.Kind() == reflect.Pointer {
		panic("codec: register with a value, not a pointer: " + t.String())
	}
	k := codecKey{proto.Kind(), proto.Version()}
	if _, dup := c.types[k]; dup {
		panic(fmt.Sprintf("codec: %s v%d already registered", k.kind, k.v))
	}
	c.types[k] = t
	if k.v > c.latest[k.kind] {
		c.latest[k.kind] = k.v
	}
}

// Upcast registers fn to convert kind from version `from` to `from+1`.
func (c *Codec) Upcast(kind string, from int, fn Upcaster) {
	c.upcasters[codecKey{kind, from}] = fn
}

// Latest returns the newest registered version of kind (0 if unknown).
func (c *Codec) Latest(kind string) int { return c.latest[kind] }

// Kinds lists registered kinds, sorted.
func (c *Codec) Kinds() []string {
	out := make([]string, 0, len(c.latest))
	for k := range c.latest {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Encode serialises the event body as JSON.
func (c *Codec) Encode(e Event) ([]byte, error) {
	if _, ok := c.types[codecKey{e.Kind(), e.Version()}]; !ok {
		return nil, fmt.Errorf("codec: %s v%d not registered", e.Kind(), e.Version())
	}
	return json.Marshal(e)
}

// Decode deserialises body into the registered type for (kind, v) and
// upcasts it to the latest version. Missing upcasters are an error: an old
// event must never be silently misread.
func (c *Codec) Decode(kind string, v int, body []byte) (Event, error) {
	t, ok := c.types[codecKey{kind, v}]
	if !ok {
		return nil, fmt.Errorf("codec: %s v%d not registered", kind, v)
	}
	ptr := reflect.New(t)
	if err := json.Unmarshal(body, ptr.Interface()); err != nil {
		return nil, fmt.Errorf("codec: decode %s v%d: %w", kind, v, err)
	}
	e, ok := ptr.Elem().Interface().(Event)
	if !ok {
		return nil, fmt.Errorf("codec: %s does not implement Event by value", t)
	}
	for e.Version() < c.latest[kind] {
		up, ok := c.upcasters[codecKey{kind, e.Version()}]
		if !ok {
			return nil, fmt.Errorf("codec: no upcaster for %s v%d", kind, e.Version())
		}
		next := up(e)
		if next.Kind() != kind || next.Version() != e.Version()+1 {
			return nil, fmt.Errorf("codec: upcaster for %s v%d returned %s v%d", kind, e.Version(), next.Kind(), next.Version())
		}
		e = next
	}
	return e, nil
}

// Prototypes returns a zero value for every registered (kind, version),
// sorted by kind then version. Used by the schema generator.
func (c *Codec) Prototypes() []Event {
	keys := make([]codecKey, 0, len(c.types))
	for k := range c.types {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].v < keys[j].v
	})
	out := make([]Event, 0, len(keys))
	for _, k := range keys {
		out = append(out, reflect.New(c.types[k]).Elem().Interface().(Event))
	}
	return out
}
