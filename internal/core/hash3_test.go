package core

import "testing"

func TestHash3_Cmp(t *testing.T) {
	base := Hash3{"s", "b", "f"}
	cases := []struct {
		name string
		b    Hash3
		want HashChange
	}{
		{"same", Hash3{"s", "b", "f"}, HashNone},
		{"file only", Hash3{"s", "b", "f2"}, HashFile},
		{"body", Hash3{"s", "b2", "f2"}, HashBody},
		{"signature wins", Hash3{"s2", "b", "f"}, HashSignature},
		{"all", Hash3{"s2", "b2", "f2"}, HashSignature},
	}
	for _, c := range cases {
		if got := base.Cmp(c.b); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}
