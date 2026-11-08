// Tests for the Runner's re-check of a recorded step's declarations
// (verify.go over skew.go): a step recorded under declarations other than
// the Runner's runs only when the plan accepted the skew by name, and
// never when a value it carries does not fit the Runner's own.
package rollback_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// madeRunbook ran file.rbmake at tasks[0].
const madeRunbook = "id: made\ntasks:\n  - name: make it\n    file.rbmake:\n      path: /x\n"

// madeStep is the recorded undo of tasks[0] as a dispatch carries it.
func madeStep(id wire.CollectionIdentity, undoDigest string, params map[string]any) rollback.Step {
	return rollback.Step{Node: "tasks[0]", Emitter: "file.rbmake", FQCN: "file.rbremove", Params: params, Source: rollback.SourceRecorded,
		Collection: id, UndoDigest: undoDigest}
}

func TestVerify_ARunnerRefusesAForeignUndoUnlessTheSkewWasAccepted(t *testing.T) {
	in := registerSkew(t)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(madeRunbook))
	if err != nil {
		t.Fatal(err)
	}

	// The Runner's own declarations: no skew, nothing to accept.
	same := madeStep(in.identity, in.undo, map[string]any{"path": "/x"})
	if err := rollback.Verify(dag, throughJSON(t, same)); err != nil {
		t.Errorf("a step recorded under this Runner's declarations: %v", err)
	}
	if note := rollback.SkewNote(same); note != "" {
		t.Errorf("a step with no skew has a note: %s", note)
	}

	foreign := madeStep(olderBuild(in), other("b"), map[string]any{"path": "/x"})
	err = rollback.Verify(dag, throughJSON(t, foreign))
	if err == nil || !strings.Contains(err.Error(), other("b")) || !strings.Contains(err.Error(), in.undo) || !strings.Contains(err.Error(), "allow_skew") {
		t.Errorf("a foreign undo digest, not accepted: %v, want a refusal naming both digests and allow_skew", err)
	}
	foreign.SkewAccepted = true
	if err := rollback.Verify(dag, throughJSON(t, foreign)); err != nil {
		t.Errorf("a foreign undo digest, accepted: %v", err)
	}
	if note := rollback.SkewNote(foreign); !strings.Contains(note, other("b")) || !strings.Contains(note, in.undo) || !strings.Contains(note, strconv.Quote("0.3.9")) {
		t.Errorf("the note of an accepted skew does not name what it accepted: %q", note)
	}

	// No undo digest recorded: the group's digest decides.
	groupOnly := madeStep(olderBuild(in), "", map[string]any{"path": "/x"})
	if err := rollback.Verify(dag, groupOnly); err == nil {
		t.Error("a foreign group digest and no undo digest was not refused")
	}
	// A moved group alone is no refusal: the undo digest is this Runner's.
	moved := in.identity
	moved.Group = "net"
	if err := rollback.Verify(dag, madeStep(moved, in.undo, map[string]any{"path": "/x"})); err != nil {
		t.Errorf("a group a later release routes elsewhere: %v", err)
	}
	// No identity and no undo digest (an older Controller, or an entry
	// written before either): the structural check alone, as before.
	if err := rollback.Verify(dag, madeStep(wire.CollectionIdentity{}, "", map[string]any{"path": "/x"})); err != nil {
		t.Errorf("a step with no identity: %v", err)
	}

	// What SkewAccepted never relaxes.
	short := in.identity
	short.Manifests = short.Manifests[:len(short.Manifests)-1]
	for name, tc := range map[string]struct {
		s    rollback.Step
		want string
	}{
		"a malformed identity":       {madeStep(short, in.undo, map[string]any{"path": "/x"}), "no build writes"},
		"a program's identity":       {madeStep(wire.CollectionIdentity{Group: "file", Program: true, Manifests: other("c")}, in.undo, map[string]any{"path": "/x"}), "external program"},
		"a value of another type":    {madeStep(olderBuild(in), other("b"), map[string]any{"path": "/x", "mode": "0644"}), "declares it int"},
		"a required key not carried": {madeStep(olderBuild(in), other("b"), map[string]any{"mode": 420}), `requires "path"`},
	} {
		t.Run(name, func(t *testing.T) {
			s := tc.s
			s.SkewAccepted = true
			if err := rollback.Verify(dag, throughJSON(t, s)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Verify = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}
