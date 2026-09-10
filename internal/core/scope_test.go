package core

import (
	"reflect"
	"testing"
)

func TestScopeKey_Canonical(t *testing.T) {
	k := ScopeKey{Org: "acme", Product: "pay", Repo: "acme/api", PathGlob: "src/billing/*"}
	s := k.String()
	if s != "org=acme|product=pay|team=|repo=acme/api|path=src/billing/*|person=" {
		t.Fatalf("canonical: %s", s)
	}
	back, err := ParseScopeKey(s)
	if err != nil || back != k {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if _, err := ParseScopeKey("org=a|product=b"); err == nil {
		t.Error("short form must fail")
	}
	if err := (ScopeKey{PathGlob: "x/*"}).Validate(); err == nil {
		t.Error("path glob without repo must fail")
	}
	if err := (ScopeKey{Repo: "r", PathGlob: "["}).Validate(); err == nil {
		t.Error("bad glob must fail")
	}
	if err := (ScopeKey{Org: "a|b"}).Validate(); err == nil {
		t.Error("separator in value must fail")
	}
}

func TestScopeKey_Contains(t *testing.T) {
	cases := []struct {
		name   string
		k, tgt ScopeKey
		want   bool
	}{
		{"global contains anything", ScopeKey{}, ScopeKey{Org: "a", Team: "t"}, true},
		{"org matches", ScopeKey{Org: "a"}, ScopeKey{Org: "a", Team: "t"}, true},
		{"org differs", ScopeKey{Org: "a"}, ScopeKey{Org: "b"}, false},
		{"narrower does not contain wider", ScopeKey{Org: "a", Team: "t"}, ScopeKey{Org: "a"}, false},
		{"product and team orthogonal", ScopeKey{Product: "p"}, ScopeKey{Team: "t"}, false},
		{"glob matches path", ScopeKey{Repo: "r", PathGlob: "src/*.ts"}, ScopeKey{Repo: "r", PathGlob: "src/a.ts"}, true},
		{"glob equal", ScopeKey{Repo: "r", PathGlob: "src/*.ts"}, ScopeKey{Repo: "r", PathGlob: "src/*.ts"}, true},
		{"glob no match", ScopeKey{Repo: "r", PathGlob: "src/*.ts"}, ScopeKey{Repo: "r", PathGlob: "lib/a.ts"}, false},
		{"glob needs target path", ScopeKey{Repo: "r", PathGlob: "src/*"}, ScopeKey{Repo: "r"}, false},
		{"person", ScopeKey{Person: "me"}, ScopeKey{Person: "me", Repo: "r"}, true},
	}
	for _, c := range cases {
		if got := c.k.Contains(c.tgt); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// 범위 해석: 제약은 넓은 우선, 컨텍스트는 좁은 우선 (ADR 0006).
func TestResolve(t *testing.T) {
	global := ScopeKey{}
	product := ScopeKey{Product: "pay"}
	team := ScopeKey{Team: "core"}
	repo := ScopeKey{Team: "core", Repo: "acme/api"}
	pathK := ScopeKey{Team: "core", Repo: "acme/api", PathGlob: "src/billing/*"}
	other := ScopeKey{Team: "web"}
	me := ScopeKey{Person: "me"}
	target := ScopeKey{Product: "pay", Team: "core", Repo: "acme/api", PathGlob: "src/billing/fee.ts", Person: "me"}
	keys := []ScopeKey{pathK, other, team, global, repo, product, me, team}

	cases := []struct {
		kind ObjectKind
		want []ScopeKey
	}{
		{KindConstraint, []ScopeKey{global, product, team, me, repo, pathK}},
		{KindContext, []ScopeKey{pathK, repo, product, team, me, global}},
	}
	for _, c := range cases {
		got := Resolve(keys, target, c.kind)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("kind %d:\n got  %v\n want %v", c.kind, got, c.want)
		}
	}
	if got := Resolve(nil, target, KindConstraint); len(got) != 0 {
		t.Errorf("no keys: %v", got)
	}
}
