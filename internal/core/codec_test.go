package core

import (
	"strings"
	"testing"
)

type thingV1 struct {
	Name string `json:"name"`
}

func (thingV1) Kind() string { return "Thing" }
func (thingV1) Version() int { return 1 }

type thingV2 struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func (thingV2) Kind() string { return "Thing" }
func (thingV2) Version() int { return 2 }

func TestCodec_RoundTripAndUpcast(t *testing.T) {
	c := NewCodec()
	c.Register(thingV1{})
	c.Register(thingV2{})
	c.Upcast("Thing", 1, func(e Event) Event {
		old := e.(thingV1)
		return thingV2{Name: old.Name, Title: strings.ToUpper(old.Name)}
	})

	body, err := c.Encode(thingV1{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Decode("Thing", 1, body)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := e.(thingV2)
	if !ok || got.Title != "X" || got.Version() != 2 {
		t.Fatalf("upcast: %#v", e)
	}
	if c.Latest("Thing") != 2 {
		t.Error("latest")
	}
	if _, err := c.Decode("Nope", 1, body); err == nil {
		t.Error("unknown kind must fail")
	}
	if _, err := c.Encode(thingV1{}); err != nil {
		t.Error("registered v1 must encode")
	}
	// Rejected is registered by default.
	if c.Latest("Rejected") != 1 {
		t.Error("Rejected must be pre-registered")
	}
}

func TestCodec_MissingUpcasterFails(t *testing.T) {
	c := NewCodec()
	c.Register(thingV1{})
	c.Register(thingV2{})
	if _, err := c.Decode("Thing", 1, []byte(`{"name":"x"}`)); err == nil {
		t.Fatal("missing upcaster must be an error, never a silent misread")
	}
}

func TestCodec_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration must panic")
		}
	}()
	c := NewCodec()
	c.Register(thingV1{})
	c.Register(thingV1{})
}
