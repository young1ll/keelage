// Package core is the domain layer: aggregates, value objects, scope
// resolution and autonomy transitions.
//
// Rules (architecture-patterns-v0 §1–§2, enforced by depguard):
//   - Imports only the standard library and this package tree.
//   - Aggregates are two pure functions: Decide(state, cmd, ctx) -> events
//     and Apply(state, event) -> state. No IO, no repository calls.
//   - Bounded contexts are sub-packages (harness, accountability,
//     realization, supply, work). They share only the ID value objects
//     defined here; struct sharing across contexts is forbidden.
package core
