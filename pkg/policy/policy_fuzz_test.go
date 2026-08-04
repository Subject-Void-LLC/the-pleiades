package policy_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/policy"
)

// FuzzResolve drives Resolve with an arbitrary number of layers (one per
// input byte) through both Override and UnionSlices combine functions,
// proving neither panics regardless of layer count or content, including
// zero layers and layers with empty or repeated names.
func FuzzResolve(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Add([]byte{1, 2, 3})
	f.Add([]byte{0, 0, 0})

	f.Fuzz(func(t *testing.T, raw []byte) {
		scalarLayers := make([]policy.Layer[int], len(raw))
		sliceLayers := make([]policy.Layer[[]int], len(raw))
		for i, b := range raw {
			name := string(rune(b))
			scalarLayers[i] = policy.Layer[int]{Name: name, Value: int(b)}
			sliceLayers[i] = policy.Layer[[]int]{Name: name, Value: []int{int(b)}}
		}

		overrideResult := policy.Resolve(policy.ModeOverride, 0, scalarLayers, policy.Override[int])
		if len(overrideResult.Layers) != len(raw) {
			t.Fatalf("Override: Layers has %d entries, want %d", len(overrideResult.Layers), len(raw))
		}

		unionResult := policy.Resolve(policy.ModeUnion, nil, sliceLayers, policy.UnionSlices[int])
		if len(unionResult.Layers) != len(raw) {
			t.Fatalf("Union: Layers has %d entries, want %d", len(unionResult.Layers), len(raw))
		}

		intersectResult := policy.Resolve(policy.ModeIntersection, nil, sliceLayers, policy.IntersectSlices[int])
		if len(intersectResult.Layers) != len(raw) {
			t.Fatalf("Intersect: Layers has %d entries, want %d", len(intersectResult.Layers), len(raw))
		}
	})
}
