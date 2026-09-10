package supply

import (
	"regexp"
	"strings"
)

// TurnClass is the fallback turn classification (implementation plan §3.4):
// revert = an edit was undone (git checkout/restore/reset/revert/stash),
// redirect = a negation or replacement phrase followed by more editing,
// accept = everything else. Recall may be low; the "same anchor three
// times" promotion rule absorbs misses (spec §1).
type TurnClass string

const (
	TurnAccept   TurnClass = "accept"
	TurnRedirect TurnClass = "redirect"
	TurnRevert   TurnClass = "revert"
)

// redirectPatterns are matched against the *next* prompt after a turn that
// edited files. They are deliberately few and literal; the signal recorded
// with the turn is the pattern name, never the prompt text.
var redirectPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"ko-negation", regexp.MustCompile(`(^|\s)(아니|아냐|아니야|아니요|아니오)([\s,.!]|$)`)},
	{"ko-instead", regexp.MustCompile(`(말고|대신|그게 아니라|그거 말고|하지 ?마|하지 ?말고|되돌려|롤백|원래대로)`)},
	{"en-negation", regexp.MustCompile(`(?i)^\s*(no|nope|not that|wrong|don'?t)[\s,.!]`)},
	{"en-instead", regexp.MustCompile(`(?i)\b(instead|rather than|actually,? no|undo that|revert that|roll ?back|not what i)\b`)},
}

var (
	revertCommands = regexp.MustCompile(`(^|[;&|]\s*)git\s+(checkout\s+(--|\S+\s+--|[^-]\S*\s*$)|restore\b|reset\b|revert\b)`)
	stashCommand   = regexp.MustCompile(`(^|[;&|]\s*)git\s+stash\b\s*(\S*)`)
	stashReadOnly  = map[string]bool{"list": true, "show": true, "pop": true, "apply": true, "branch": true}
)

// ClassifyPrompt reports whether a prompt reads as a redirection of the
// previous turn and which pattern said so. Only meaningful when the
// previous turn edited files.
func ClassifyPrompt(prompt string) (redirect bool, signal string) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return false, ""
	}
	for _, rp := range redirectPatterns {
		if rp.re.MatchString(p) {
			return true, rp.name
		}
	}
	return false, ""
}

// IsRevertCommand reports whether a shell command undoes edits.
func IsRevertCommand(command string) bool {
	c := strings.TrimSpace(command)
	if revertCommands.MatchString(c) {
		return true
	}
	if m := stashCommand.FindStringSubmatch(c); m != nil {
		return !stashReadOnly[m[2]]
	}
	return false
}
