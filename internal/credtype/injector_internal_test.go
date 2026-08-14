package credtype

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestEveryTargetIsWrappedInTheSecretTrackingDecorator is the structural
// half of the Decorator's guarantee.
//
// The behavioural half (injector_test.go's
// TestInjectRegistersEveryRenderedSecret) proves the wrapping works for the
// five Targets that exist today. This one proves it will keep working for a
// sixth: whoever adds a Target registers it and does nothing else, and the
// registration alone is what puts its output into the masking set. If a
// future change ever builds the target list without wrapping, this fails
// before that target's own tests get a chance to pass without it.
func TestEveryTargetIsWrappedInTheSecretTrackingDecorator(t *testing.T) {
	t.Parallel()

	in, err := NewInjector(render.New())
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}

	if len(in.targets) != len(targets.All()) {
		t.Fatalf("the injector holds %d targets and %d are registered", len(in.targets), len(targets.All()))
	}
	for _, target := range in.targets {
		wrapped, ok := target.(secretTracking)
		if !ok {
			t.Errorf("the %q target is not wrapped in the secret-tracking decorator", target.Key())
			continue
		}
		if wrapped.literals == nil {
			t.Errorf("the %q target is wrapped with no masking set to register into", target.Key())
		}
	}
}

// TestTargetsRunFilesBeforeValues pins the ordering the reserved filename
// namespace depends on.
//
// It is a real constraint rather than a tidiness one: an env template
// writing {{ tower.filename.cert }} renders against a namespace that does
// not exist until the file target has decided where the cert file goes, so
// a values-phase target running first turns a working credential type into
// an undefined-variable failure at launch.
func TestTargetsRunFilesBeforeValues(t *testing.T) {
	t.Parallel()

	ordered := orderedTargets()
	if len(ordered) == 0 {
		t.Fatal("no targets are registered")
	}

	sawValues := false
	for _, target := range ordered {
		switch target.Phase() {
		case PhaseValues:
			sawValues = true
		case PhaseFiles:
			if sawValues {
				t.Errorf("the %q target runs in the files phase after a values-phase target", target.Key())
			}
		}
	}

	// Both phases must actually be represented, or the assertion above
	// passes vacuously.
	if !sawValues || ordered[0].Phase() != PhaseFiles {
		t.Errorf("the target list does not cover both phases: %v", keysOf(ordered))
	}
}

// TestOrderedTargetsIsDeterministic covers why the sort exists: a
// validation failure that moves between runs is one nobody can write a test
// for.
func TestOrderedTargetsIsDeterministic(t *testing.T) {
	t.Parallel()

	first := keysOf(orderedTargets())
	for i := 0; i < 16; i++ {
		got := keysOf(orderedTargets())
		if len(got) != len(first) {
			t.Fatalf("orderedTargets() returned %d targets, then %d", len(first), len(got))
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("orderedTargets() = %v, then %v", first, got)
			}
		}
	}
}

// keysOf names a target list for a failure message.
func keysOf(list []Target) []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Key())
	}
	return out
}
