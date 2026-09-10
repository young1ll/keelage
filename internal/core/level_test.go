package core

import "testing"

// 자율 전이표 전수 검사 (spec §4.2).
func TestTransitions_Exhaustive(t *testing.T) {
	want := map[Level]map[Trigger]Level{
		L0: {TriggerPromote: L1, TriggerRegress: L0},
		L1: {TriggerPromote: L2, TriggerRegress: L0},
		L2: {TriggerPromote: L3, TriggerRegress: L1},
		L3: {TriggerPromote: L3, TriggerRegress: L2},
	}
	for from := L0; from <= MaxLevel; from++ {
		for _, tr := range []Trigger{TriggerPromote, TriggerRegress} {
			got, ok := Transition(from, tr)
			if !ok {
				t.Fatalf("%s %s: missing", from, tr)
			}
			if got != want[from][tr] {
				t.Errorf("%s %s: got %s want %s", from, tr, got, want[from][tr])
			}
			if !got.Valid() {
				t.Errorf("%s %s: invalid result %d", from, tr, got)
			}
			if tr == TriggerRegress && got > from {
				t.Errorf("regression must never raise: %s -> %s", from, got)
			}
			if tr == TriggerPromote && got < from {
				t.Errorf("promotion must never lower: %s -> %s", from, got)
			}
			if tr == TriggerPromote && from < MaxLevel && got != from+1 {
				t.Errorf("promotion is exactly one step: %s -> %s", from, got)
			}
			if tr == TriggerRegress && from > L0 && got != from-1 {
				t.Errorf("regression is exactly one step: %s -> %s", from, got)
			}
		}
	}
	if _, ok := Transition(Level(9), TriggerPromote); ok {
		t.Error("unknown level must not transition")
	}
	if _, ok := Transition(L1, Trigger("x")); ok {
		t.Error("unknown trigger must not transition")
	}
}

func TestLevel_Text(t *testing.T) {
	for l := L0; l <= MaxLevel; l++ {
		b, err := l.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var back Level
		if err := back.UnmarshalText(b); err != nil || back != l {
			t.Errorf("round trip %s: %v %s", l, err, back)
		}
	}
	if _, err := ParseLevel("L4"); err == nil {
		t.Error("L4 must be invalid")
	}
	if _, err := Level(7).MarshalText(); err == nil {
		t.Error("invalid level must not marshal")
	}
}
