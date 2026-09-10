package core

import (
	"fmt"
	"strconv"
)

// Level is an autonomy level for a scope (spec §4.2).
//
//	L0  human judges everything; agents only propose
//	L1  harness judges, human confirms before settle
//	L2  harness settles, human reviews a sample
//	L3  harness settles, human handles exceptions only
type Level int

const (
	L0 Level = iota
	L1
	L2
	L3
)

// MaxLevel is the highest autonomy level.
const MaxLevel = L3

// Valid reports whether l is one of L0..L3.
func (l Level) Valid() bool { return l >= L0 && l <= MaxLevel }

func (l Level) String() string { return "L" + strconv.Itoa(int(l)) }

// ParseLevel parses "L0".."L3".
func ParseLevel(s string) (Level, error) {
	if len(s) == 2 && s[0] == 'L' && s[1] >= '0' && s[1] <= '3' {
		return Level(s[1] - '0'), nil
	}
	return 0, fmt.Errorf("level: invalid %q", s)
}

// MarshalText encodes the level as "Ln".
func (l Level) MarshalText() ([]byte, error) {
	if !l.Valid() {
		return nil, fmt.Errorf("level: invalid %d", int(l))
	}
	return []byte(l.String()), nil
}

// UnmarshalText decodes "Ln".
func (l *Level) UnmarshalText(b []byte) error {
	v, err := ParseLevel(string(b))
	if err != nil {
		return err
	}
	*l = v
	return nil
}

// Trigger is a cause of an autonomy transition.
type Trigger string

const (
	// TriggerPromote is a human-approved promotion (a harness Change).
	TriggerPromote Trigger = "promote"
	// TriggerRegress is one observed regression: automatic one-step demotion.
	TriggerRegress Trigger = "regress"
)

// Transitions is the autonomy transition table (spec §4.2): promotion is one
// step up, regression is one step down, never outside L0..L3. Re-promotion
// after a demotion needs the outcome history to be re-accumulated; that
// precondition is checked by the Scope aggregate, not by the table.
var Transitions = map[Level]map[Trigger]Level{
	L0: {TriggerPromote: L1, TriggerRegress: L0},
	L1: {TriggerPromote: L2, TriggerRegress: L0},
	L2: {TriggerPromote: L3, TriggerRegress: L1},
	L3: {TriggerPromote: L3, TriggerRegress: L2},
}

// Transition returns the level reached from `from` on trigger t. ok is false
// for an unknown level or trigger.
func Transition(from Level, t Trigger) (to Level, ok bool) {
	row, ok := Transitions[from]
	if !ok {
		return from, false
	}
	to, ok = row[t]
	return to, ok
}
