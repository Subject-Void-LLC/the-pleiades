package policy_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

func TestMode_String(t *testing.T) {
	cases := map[policy.Mode]string{
		policy.ModeOverride:     "override",
		policy.ModeUnion:        "union",
		policy.ModeIntersection: "intersection",
		policy.Mode(99):         "unknown",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

func TestResolve_EmptyLayers(t *testing.T) {
	result := policy.Resolve(policy.ModeOverride, "base", nil, policy.Override[string])

	if result.Value != "base" {
		t.Errorf("Value = %q, want unchanged base %q", result.Value, "base")
	}
	if len(result.Layers) != 0 {
		t.Errorf("Layers = %v, want empty", result.Layers)
	}
	if result.Mode != policy.ModeOverride {
		t.Errorf("Mode = %v, want ModeOverride", result.Mode)
	}
}

func TestResolve_OverrideFoldsLeastToMostSpecific(t *testing.T) {
	layers := []policy.Layer[string]{
		{Name: "system", Value: "system-value"},
		{Name: "group:databases", Value: "group-value"},
		{Name: "device:web1", Value: "device-value"},
	}

	result := policy.Resolve(policy.ModeOverride, "default", layers, policy.Override[string])

	if result.Value != "device-value" {
		t.Errorf("Value = %q, want the most specific layer's value %q", result.Value, "device-value")
	}
	want := []string{"system", "group:databases", "device:web1"}
	if !reflect.DeepEqual(result.Layers, want) {
		t.Errorf("Layers = %v, want %v", result.Layers, want)
	}
}

func TestResolve_LayerWithEmptyNameIsPreserved(t *testing.T) {
	layers := []policy.Layer[int]{
		{Name: "", Value: 1},
		{Name: "specific", Value: 2},
	}
	result := policy.Resolve(policy.ModeOverride, 0, layers, policy.Override[int])

	if len(result.Layers) != 2 {
		t.Fatalf("Layers = %v, want 2 entries (including the empty-named one)", result.Layers)
	}
	if result.Layers[0] != "" {
		t.Errorf("Layers[0] = %q, want empty string preserved, not dropped", result.Layers[0])
	}
	if result.Value != 2 {
		t.Errorf("Value = %d, want 2", result.Value)
	}
}

// TestResolve_FieldLevelCombine proves a T with independently optional
// fields can mix Override semantics per field inside one combine function,
// exactly the shape internal/classification.Rule needs and the package doc
// comment describes.
func TestResolve_FieldLevelCombine(t *testing.T) {
	type partial struct {
		A *string
		B *string
	}
	ptr := func(s string) *string { return &s }

	combine := func(acc, next partial) partial {
		if next.A != nil {
			acc.A = next.A
		}
		if next.B != nil {
			acc.B = next.B
		}
		return acc
	}

	layers := []policy.Layer[partial]{
		{Name: "l1", Value: partial{A: ptr("a1")}},
		{Name: "l2", Value: partial{B: ptr("b2")}},
		{Name: "l3", Value: partial{A: ptr("a3")}},
	}

	result := policy.Resolve(policy.ModeOverride, partial{}, layers, combine)

	if result.Value.A == nil || *result.Value.A != "a3" {
		t.Errorf("A = %v, want \"a3\" (most specific layer that set it)", result.Value.A)
	}
	if result.Value.B == nil || *result.Value.B != "b2" {
		t.Errorf("B = %v, want \"b2\" (only layer that ever set it)", result.Value.B)
	}
}

// TestResolve_PrecedenceLock proves the package doc comment's own
// simulate-locked worked example: once a layer reaches a terminal state, no
// later, more specific layer can override it.
func TestResolve_PrecedenceLock(t *testing.T) {
	const (
		unlocked = iota
		locked
	)
	combine := func(acc, next int) int {
		if acc == locked {
			return acc
		}
		return next
	}

	layers := []policy.Layer[int]{
		{Name: "item", Value: locked},
		{Name: "runbook", Value: unlocked},
		{Name: "task", Value: unlocked},
	}

	result := policy.Resolve(policy.ModeOverride, unlocked, layers, combine)
	if result.Value != locked {
		t.Errorf("Value = %d, want locked (%d): a runbook/task layer must never override an item-level lock", result.Value, locked)
	}
}

func TestOverride(t *testing.T) {
	if got := policy.Override("acc", "next"); got != "next" {
		t.Errorf("Override(acc, next) = %q, want %q", got, "next")
	}
}

func TestUnionSlices(t *testing.T) {
	tests := []struct {
		name string
		acc  []string
		next []string
		want []string
	}{
		{"both empty", nil, nil, []string{}},
		{"disjoint", []string{"a"}, []string{"b"}, []string{"a", "b"}},
		{"overlapping dedups", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"next empty", []string{"a"}, nil, []string{"a"}},
		{"acc empty", nil, []string{"a"}, []string{"a"}},
		{"acc has an internal duplicate", []string{"a", "a", "b"}, nil, []string{"a", "b"}},
		{"next has an internal duplicate", nil, []string{"a", "a", "b"}, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.UnionSlices(tt.acc, tt.next)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnionSlices(%v, %v) = %v, want %v", tt.acc, tt.next, got, tt.want)
			}
		})
	}
}

func TestUnionSlices_ViaResolve(t *testing.T) {
	layers := []policy.Layer[[]string]{
		{Name: "base", Value: []string{"AptCapable"}},
		{Name: "more-specific", Value: []string{"SnapCapable", "AptCapable"}},
	}
	result := policy.Resolve(policy.ModeUnion, nil, layers, policy.UnionSlices[string])
	want := []string{"AptCapable", "SnapCapable"}
	if !reflect.DeepEqual(result.Value, want) {
		t.Errorf("Value = %v, want %v", result.Value, want)
	}
}

func TestIntersectSlices(t *testing.T) {
	tests := []struct {
		name string
		acc  []string
		next []string
		want []string
	}{
		{"nil acc establishes ceiling", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"narrows", []string{"a", "b", "c"}, []string{"b", "c"}, []string{"b", "c"}},
		{"narrows to empty", []string{"a", "b"}, []string{"c"}, []string{}},
		{"cannot widen", []string{"a"}, []string{"a", "b"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.IntersectSlices(tt.acc, tt.next)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IntersectSlices(%v, %v) = %v, want %v", tt.acc, tt.next, got, tt.want)
			}
		})
	}
}

// TestIntersectSlices_EmptyResultStaysStickyAcrossFold is the regression
// test for the exact distinction IntersectSlices's own doc comment now
// calls out: a nil accumulator means "no constraint yet" (the first real
// layer becomes the ceiling), but a non-nil empty accumulator means a
// prior layer already narrowed the result to nothing, which must stay
// empty for the rest of the fold rather than being revived by a later,
// disjoint layer. This is the difference between IntersectSlices treating
// "empty" the same as "nil" (a real, adversarially-found candidate bug:
// checking len(acc) == 0 instead of acc == nil would let this test fail)
// and the actual, monotonic-narrowing-preserving behavior.
func TestIntersectSlices_EmptyResultStaysStickyAcrossFold(t *testing.T) {
	layers := []policy.Layer[[]string]{
		{Name: "manifest", Value: []string{"ios-15", "ios-16"}},
		{Name: "disjoint-override", Value: []string{"ios-99"}}, // narrows the ceiling to nothing
		{Name: "later-layer", Value: []string{"ios-17"}},       // must NOT revive a value, even though it's disjoint from the ceiling too
	}
	result := policy.Resolve(policy.ModeIntersection, nil, layers, policy.IntersectSlices[string])
	if len(result.Value) != 0 {
		t.Errorf("Value = %v, want empty: once intersection narrows to nothing, a later layer must never revive a value", result.Value)
	}
}

func TestIntersectSlices_NonNilEmptyBaseIsNotTreatedAsUnset(t *testing.T) {
	// A non-nil empty base is itself already "narrowed to nothing" (e.g. it
	// came from a prior IntersectSlices call, or a caller's own
	// make([]string, 0)), not "no constraint yet" (which only a nil base
	// means). The first layer must not be allowed to become a fresh
	// ceiling in this case.
	base := []string{}
	got := policy.IntersectSlices(base, []string{"a", "b"})
	if len(got) != 0 {
		t.Errorf("IntersectSlices(non-nil empty, [a b]) = %v, want empty (non-nil empty is an established empty ceiling, not \"unset\")", got)
	}
}

func TestIntersectSlices_ViaResolve(t *testing.T) {
	// A Collection manifest's platform target (the ceiling) narrowed by a
	// runbook-level requirement, matching Section 8/Phase 32's own worked
	// example: a runbook may narrow, never widen, what the manifest permits.
	layers := []policy.Layer[[]string]{
		{Name: "manifest", Value: []string{"ios-15", "ios-16", "ios-17"}},
		{Name: "runbook", Value: []string{"ios-17"}},
	}
	result := policy.Resolve(policy.ModeIntersection, nil, layers, policy.IntersectSlices[string])
	want := []string{"ios-17"}
	if !reflect.DeepEqual(result.Value, want) {
		t.Errorf("Value = %v, want %v", result.Value, want)
	}
}
