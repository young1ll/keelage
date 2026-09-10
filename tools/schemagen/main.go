// Command schemagen publishes JSON Schema for the persistent objects, the
// ledger envelope and every registered event, generated from the Go structs
// in internal/core (the source of truth). Output is deterministic; CI fails
// when spec/schema differs from a fresh run.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
)

const baseID = "https://github.com/young1ll/keelage/spec/schema/"

func main() {
	out := flag.String("out", "spec/schema", "output directory")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func reflector() (*jsonschema.Reflector, error) {
	r := &jsonschema.Reflector{
		FieldNameTag: "json",
		Mapper: func(t reflect.Type) *jsonschema.Schema {
			switch t {
			case reflect.TypeOf(core.Level(0)):
				return &jsonschema.Schema{Type: "string", Enum: []any{"L0", "L1", "L2", "L3"}, Description: "Autonomy level."}
			case reflect.TypeOf(core.Hash(nil)):
				return &jsonschema.Schema{Type: "string", Pattern: "^[0-9a-f]{64}$", Description: "SHA-256, hex."}
			case reflect.TypeOf(json.RawMessage(nil)):
				return &jsonschema.Schema{Type: "object", Description: "Event body: JSON of the event kind's schema (spec/schema/events)."}
			case reflect.TypeOf(time.Duration(0)):
				return &jsonschema.Schema{Type: "integer", Description: "Duration in nanoseconds."}
			}
			return nil
		},
	}
	if err := r.AddGoComments("github.com/young1ll/keelage", "./internal/core"); err != nil {
		return nil, fmt.Errorf("go comments: %w", err)
	}
	return r, nil
}

type target struct {
	name string
	v    any
}

func run(out string) error {
	r, err := reflector()
	if err != nil {
		return err
	}
	objects := []target{
		{"event", core.Envelope{}},
		{"actor", core.ActorRef{}},
		{"scope-key", core.ScopeKey{}},
		{"anchor", core.Anchor{}},
		{"hash3", core.Hash3{}},
		{"rejected", core.Rejected{}},
		{"constraint", harness.Constraint{}},
		{"scope", harness.Scope{}},
		{"change", accountability.Change{}},
		{"judgment", accountability.Judgment{}},
		{"outcome", accountability.Outcome{}},
		{"gate", accountability.Gate{}},
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, "events"), 0o755); err != nil {
		return err
	}
	for _, o := range objects {
		if err := write(r, filepath.Join(out, o.name+".json"), o.name, o.v); err != nil {
			return err
		}
	}
	index := map[string]any{}
	for _, proto := range app.NewCodec().Prototypes() {
		name := fmt.Sprintf("%s.v%d", proto.Kind(), proto.Version())
		if err := write(r, filepath.Join(out, "events", name+".json"), "events/"+name, proto); err != nil {
			return err
		}
		index[proto.Kind()] = proto.Version()
	}
	b, _ := json.MarshalIndent(map[string]any{"latest": index}, "", "  ")
	return os.WriteFile(filepath.Join(out, "events", "index.json"), append(b, '\n'), 0o644)
}

func write(r *jsonschema.Reflector, path, name string, v any) error {
	s := r.Reflect(v)
	s.ID = jsonschema.ID(baseID + name + ".json")
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
