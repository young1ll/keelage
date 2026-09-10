package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// HookCommandPrefix identifies keelage's own hook handlers in settings.json.
const HookCommandPrefix = "keelage hook claude-code"

// MergeHooks adds keelage's hook handlers to a settings.json document,
// keeping every other key and every existing handler. Idempotent: our
// handlers are matched by command prefix, so re-running never duplicates
// them. The result keeps the document's other fields verbatim.
func MergeHooks(settings []byte) ([]byte, bool, error) {
	doc, err := parseDoc(settings)
	if err != nil {
		return nil, false, err
	}
	var ours struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(hooksSettings), &ours); err != nil {
		return nil, false, fmt.Errorf("claude-code: embedded hooks.json: %w", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for _, event := range sortedKeys(ours.Hooks) {
		groups := ours.Hooks[event]
		existing, _ := hooks[event].([]any)
		if hasOurHandler(existing) {
			continue
		}
		for _, g := range groups {
			existing = append(existing, g)
		}
		hooks[event] = existing
		changed = true
	}
	if !changed {
		return settings, false, nil
	}
	doc["hooks"] = hooks
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// RemoveHooks strips keelage's handlers from a settings.json document and
// leaves everything else as it was. Events left with no handlers are
// dropped; an empty "hooks" object is dropped too.
func RemoveHooks(settings []byte) ([]byte, bool, error) {
	doc, err := parseDoc(settings)
	if err != nil {
		return nil, false, err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		return settings, false, nil
	}
	changed := false
	for event, v := range hooks {
		groups, _ := v.([]any)
		var kept []any
		for _, g := range groups {
			group, _ := g.(map[string]any)
			handlers, _ := group["hooks"].([]any)
			var keptHandlers []any
			for _, h := range handlers {
				if isOurs(h) {
					changed = true
					continue
				}
				keptHandlers = append(keptHandlers, h)
			}
			if len(keptHandlers) == 0 && len(handlers) > 0 {
				continue
			}
			if len(handlers) > 0 {
				group["hooks"] = keptHandlers
			}
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if !changed {
		return settings, false, nil
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		doc["hooks"] = hooks
	}
	if len(doc) == 0 {
		return []byte("{}\n"), true, nil
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// HasHooks reports whether keelage's handlers are present.
func HasHooks(settings []byte) bool {
	doc, err := parseDoc(settings)
	if err != nil {
		return false
	}
	hooks, _ := doc["hooks"].(map[string]any)
	for _, v := range hooks {
		groups, _ := v.([]any)
		if hasOurHandler(groups) {
			return true
		}
	}
	return false
}

func parseDoc(settings []byte) (map[string]any, error) {
	doc := map[string]any{}
	if strings.TrimSpace(string(settings)) == "" {
		return doc, nil
	}
	if err := json.Unmarshal(settings, &doc); err != nil {
		return nil, errors.New("claude-code: settings.json is not a JSON object: " + err.Error())
	}
	return doc, nil
}

func isOurs(h any) bool {
	m, _ := h.(map[string]any)
	cmd, _ := m["command"].(string)
	return strings.HasPrefix(strings.TrimSpace(cmd), HookCommandPrefix)
}

func hasOurHandler(groups []any) bool {
	for _, g := range groups {
		group, _ := g.(map[string]any)
		handlers, _ := group["hooks"].([]any)
		for _, h := range handlers {
			if isOurs(h) {
				return true
			}
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
