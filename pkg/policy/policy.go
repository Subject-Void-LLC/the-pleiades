// Package policy provides the Section 25 "hierarchical policy resolver"
// shared primitive (PLAN.md Section 25, "Shared Primitives"): one
// mechanism, reused by every subsystem that resolves a value across the
// System -> Inventory -> Group -> Device hierarchy (or an equivalent
// ordered chain, such as internal/classification's rule-tree path), instead
// of each subsystem writing its own walk-and-merge loop with its own
// merge semantics.
//
// Resolve itself is mechanism-only: it folds an ordered chain of Layers
// through a caller-supplied combine function and returns the result. It
// does not interpret Mode, and it does not itself implement Override,
// Union, or Intersection semantics for an arbitrary T; PLAN.md Section 25
// is explicit that a call site "states the mode explicitly," and that a
// single T can even mix modes per field (internal/classification.Rule is
// exactly this: Type/ConnectionMode/Onboard are Override, while a future
// Capabilities field, Phase 32 scope, would be Union). Override (this
// package's own combinator) is the one merge shape any T can implement
// generically; Union and Intersection are provided only for the slice
// shape most real call sites actually need (UnionSlices, IntersectSlices),
// since "union of two arbitrary structs" has no single generic meaning.
//
// A precedence lock (PLAN.md Section 9: an inventory item locked to
// simulate-locked cannot be overridden by any runbook or task, however
// specific) does not need a fourth Mode. It is an Override chain whose
// combine function inspects acc and refuses next once a terminal value is
// reached:
//
//	combine := func(acc, next LockState) LockState {
//	    if acc == Locked {
//	        return acc // once locked, no later (more specific) layer can override it
//	    }
//	    return next
//	}
//
// This is written down here, not built, because no phase consuming it
// exists yet; the shape above is what that phase should reach for instead
// of inventing a second resolver.
package policy

// Mode names which of the three Section 25 merge semantics a call site's
// combine function implements. Resolve never branches on Mode: it is
// carried on Result purely so a resolution's origin is self-documenting
// (for logs, audits, or a future debug endpoint) and so a call site states
// its contract in code, not only in PLAN.md's own table. A Resolve call
// with ModeUnion and a combine function that actually overrides is a
// caller bug Resolve cannot detect, the same way a Go function's doc
// comment claiming a contract does not make the compiler enforce it.
type Mode uint8

// The three Section 25 merge semantics.
const (
	// ModeOverride: a more specific layer replaces the accumulated value
	// outright (PLAN.md Section 31 execution environment precedence,
	// Section 20 allow-lists, internal/classification's Type/
	// ConnectionMode/Onboard fields).
	ModeOverride Mode = iota
	// ModeUnion: layers combine rather than replace (PLAN.md Section 33
	// notification inheritance).
	ModeUnion
	// ModeIntersection: a more specific layer may only narrow what a less
	// specific layer already permits, never widen it (PLAN.md Section
	// 8/Phase 32 capability and platform-target resolution).
	ModeIntersection
)

// String renders the mode for log lines and Result inspection.
func (m Mode) String() string {
	switch m {
	case ModeOverride:
		return "override"
	case ModeUnion:
		return "union"
	case ModeIntersection:
		return "intersection"
	default:
		return "unknown"
	}
}

// Layer is one named level in a resolution chain, ordered least to most
// specific by the caller (PLAN.md's own System -> Inventory -> Group ->
// Device chain, or internal/classification's root-to-leaf rule path). Name
// exists so a Result can report which levels actually contributed,
// matching Section 6d's own worked example of explaining a resolved value
// as "the base Linux rule plus the Debian rule plus the Ubuntu rule."
type Layer[T any] struct {
	Name  string
	Value T
}

// Result is a resolved value together with the chain that produced it.
type Result[T any] struct {
	// Value is the final, folded result.
	Value T
	// Mode is carried for reporting only; see the package doc comment.
	Mode Mode
	// Layers lists the Name of every layer Resolve consulted, in the order
	// consulted (least specific first). A caller checking whether any
	// layer matched at all should check len(Layers) == 0 rather than
	// adding a separate Matched field, so there is exactly one place this
	// can drift from what was actually folded.
	Layers []string
}

// Resolve folds layers left to right (least specific first) through
// combine, starting from base, and returns the final value plus a record
// of which layers were consulted. combine receives the value accumulated
// so far and the next layer's Value, and returns the new accumulated
// value; it is what actually implements Override, Union, Intersection, a
// precedence lock, or any per-field mixture of them for the caller's own
// T. An empty layers returns base unchanged with an empty Layers list, not
// an error: "nothing configured at any level" is a normal outcome for most
// Section 25 call sites (system-level policy resolving with no group or
// device override present), distinct from internal/classification's own,
// stricter "no rule matched this path at all" error, which that package
// raises itself by inspecting the returned Result.
func Resolve[T any](mode Mode, base T, layers []Layer[T], combine func(acc, next T) T) Result[T] {
	acc := base
	names := make([]string, 0, len(layers))
	for _, l := range layers {
		acc = combine(acc, l.Value)
		names = append(names, l.Name)
	}
	return Result[T]{Value: acc, Mode: mode, Layers: names}
}

// Override is the ready-made combine function for the common case where T
// is a plain value (not a struct with independently optional fields) and a
// more specific configured layer simply replaces the previous one outright
// (PLAN.md Section 25's own Override example: "a pod specification
// override is merged over a default specification"). It is not appropriate
// for a T with optional/partial fields, where only the fields actually set
// at a layer should replace the accumulated value and the rest should
// continue inheriting; such a T needs its own combine function (see
// internal/classification.combineRule for a worked field-level example).
func Override[T any](_, next T) T { return next }

// UnionSlices is the ready-made combine function for the common case where
// T is a slice and a more specific layer's elements combine with, rather
// than replace, everything accumulated so far (PLAN.md Section 25's own
// Union example: "bindings inherit as a union across definition, content
// source, and organization"). Duplicates (by ==) are dropped so resolving
// the same layer's contribution twice, or two layers naming the same
// element, does not grow the result unboundedly.
func UnionSlices[T comparable](acc, next []T) []T {
	seen := make(map[T]struct{}, len(acc)+len(next))
	result := make([]T, 0, len(acc)+len(next))
	for _, v := range acc {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	for _, v := range next {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

// IntersectSlices is the ready-made combine function for the common case
// where T is a slice and a more specific layer may only narrow what a less
// specific layer already permits, never widen it (PLAN.md Section 8/Phase
// 32's capability and platform-target resolution). The first layer folded
// establishes the ceiling; every layer after it can only remove elements,
// never add one the ceiling did not already contain. Passing a nil acc as
// base means "no constraint yet," so the first real layer becomes the
// ceiling rather than immediately intersecting against nothing and
// producing an empty result.
//
// A non-nil but empty acc is deliberately NOT treated as "no constraint
// yet," unlike nil: it means a prior layer already narrowed the result to
// nothing, and that must stay sticky for the rest of the fold. Resolve
// calls combine once per layer, threading the previous result back in as
// the next acc, so a genuinely-narrowed-to-empty result and an
// unset-so-far result must be distinguishable, or a later layer could
// revive a value an earlier layer already excluded, which is exactly the
// non-monotonic widening this function exists to prevent. See
// TestIntersectSlices_EmptyResultStaysStickyAcrossFold for the concrete
// three-layer case this distinction protects.
func IntersectSlices[T comparable](acc, next []T) []T {
	if acc == nil {
		return append([]T(nil), next...)
	}
	allowed := make(map[T]struct{}, len(next))
	for _, v := range next {
		allowed[v] = struct{}{}
	}
	result := make([]T, 0, len(acc))
	for _, v := range acc {
		if _, ok := allowed[v]; ok {
			result = append(result, v)
		}
	}
	return result
}
