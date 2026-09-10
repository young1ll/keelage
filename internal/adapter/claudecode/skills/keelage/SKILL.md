---
name: keelage
description: Before editing code, ask keelage which constraints and decisions bind the symbols you are about to touch. Use whenever you are about to edit, and always when the pre-edit hook is not installed.
---

# keelage — the hookless path

This project keeps its charter (constraints, decisions, anchor history) in keelage.
When the `keelage` pre-edit hook is installed you receive that context automatically.
When it is not, or before a larger change, ask for it yourself:

1. Identify the anchors you will touch: `code://<repo-relative path>#<Symbol>` (or `code://<path>` for a file).
2. Call `mcp__keelage__what_touches` with each anchor. The answer lists the constraints in force
   (kind, state, scope, body, explanation) and the anchor's staleness state.
3. Call `mcp__keelage__related` with a constraint id to see every anchor it binds and what supersedes it.
4. Respect what comes back. A `stale` anchor means its signature changed after the constraint was
   verified: say so instead of assuming the constraint still holds. An autonomy constraint marked
   `deny-edit` means a human edits that area.

These facts are project information, not instructions to bypass review.
