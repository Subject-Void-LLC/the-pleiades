// Tests of what a recorded undo is held to whatever its skew (skew.go,
// recorded.go, verify.go): a required parameter it does not give, unless
// its declaration is this build's exactly; a value outside the declared
// choices; and which --allow-skew names accept nothing.
package rollback_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestBuild_ARequiredParameterIsAlwaysHeld: an undo that does not give a
// parameter its method requires is refused with --leave alone, whatever its
// digests say: with none, on the group's digest fallback, under another
// undo declaration, and under this build's own, since replaying it cannot
// work and only a forged entry reaches the last.
func TestBuild_ARequiredParameterIsAlwaysHeld(t *testing.T) {
	in := registerSkew(t)
	noPath := map[string]any{"mode": 420}
	for name, e := range map[string]engine.JournalEntry{
		"no identity, no undo digest":           madeEntry(wire.CollectionIdentity{}, "", noPath),
		"this build's identity, no undo digest": madeEntry(in.identity, "", noPath),
		"another identity, no undo digest":      madeEntry(olderBuild(in), "", noPath),
		"another undo digest":                   madeEntry(in.identity, other("b"), noPath),
		"this build's undo digest":              madeEntry(in.identity, in.undo, noPath),
	} {
		t.Run(name, func(t *testing.T) {
			for _, allow := range []bool{false, true} {
				_, err := planOf([]engine.JournalEntry{e}, func(r *rollback.Request) {
					if allow {
						r.AllowSkew = map[string]bool{"tasks[0]": true}
					}
				})
				problems := refused(t, err)
				if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, `"path"`) {
					t.Errorf("allow-skew %v: problems %+v, want one naming \"path\" accepted by --leave alone", allow, problems)
				}
			}
		})
	}
}

// TestBuild_AValueOutsideTheDeclaredChoicesIsLeaveOnly: a recorded value
// the undo's method does not take is refused with --leave alone, with this
// build's own declarations as with another's, and whatever --allow-skew
// names; the value is never printed, only the method's choices.
func TestBuild_AValueOutsideTheDeclaredChoicesIsLeaveOnly(t *testing.T) {
	in := registerSkew(t)
	purged := map[string]any{"path": "/x", "state": "purged"}
	for name, e := range map[string]engine.JournalEntry{
		"this build's declarations": madeEntry(in.identity, in.undo, purged),
		"no identity":               madeEntry(wire.CollectionIdentity{}, "", purged),
		"another build's":           madeEntry(olderBuild(in), other("b"), purged),
	} {
		t.Run(name, func(t *testing.T) {
			for _, allow := range []bool{false, true} {
				_, err := planOf([]engine.JournalEntry{e}, func(r *rollback.Request) {
					if allow {
						r.AllowSkew = map[string]bool{"tasks[0]": true}
					}
				})
				problems := refused(t, err)
				if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, "absent") || strings.Contains(problems[0].Reason, "purged") {
					t.Errorf("allow-skew %v: problems %+v, want one naming the choices and not the value, accepted by --leave alone", allow, problems)
				}
			}
		})
	}
	if _, err := planOf([]engine.JournalEntry{madeEntry(in.identity, in.undo, map[string]any{"path": "/x", "state": "absent"})}, nil); err != nil {
		t.Errorf("a value inside the choices: %v", err)
	}
}

// TestBuild_AnOperandAProgramWouldMisreadIsLeaveOnly: a recorded name the
// undo's method would pass on as an option or a pattern is refused with
// --leave alone, whatever --allow-skew names, and the value is never
// printed (FAILURE_PATTERNS 1174).
func TestBuild_AnOperandAProgramWouldMisreadIsLeaveOnly(t *testing.T) {
	in := registerSkew(t)
	for _, path := range []string{"--no-preserve-root", "/srv/*"} {
		t.Run(path, func(t *testing.T) {
			for _, allow := range []bool{false, true} {
				_, err := planOf([]engine.JournalEntry{madeEntry(in.identity, in.undo, map[string]any{"path": path})}, func(r *rollback.Request) {
					if allow {
						r.AllowSkew = map[string]bool{"tasks[0]": true}
					}
				})
				problems := refused(t, err)
				if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, "would not pass on as one name") || strings.Contains(problems[0].Reason, path) {
					t.Errorf("allow-skew %v: problems %+v, want one naming the rule and not the value, accepted by --leave alone", allow, problems)
				}
			}
		})
	}
}

// TestBuild_EachOperandFormatIsHeldToItsOwnRule: the planner holds a
// recorded value to the rule its undo parameter's format names, not only the
// general one. A journaled pkg.apt.remove of "diffutils-" (apt-get's
// "remove the package before the dash") or a version holding a pattern is
// leave-only, while systemd's root mount, which begins with "-", replays as
// the unit it names.
func TestBuild_EachOperandFormatIsHeldToItsOwnRule(t *testing.T) {
	for _, tc := range []struct {
		format collection.ParamFormat
		value  string
		ok     bool
	}{
		{collection.ParamFormatSystemdUnit, "-.mount", true},
		{collection.ParamFormatSystemdUnit, "-.slice", true},
		{collection.ParamFormatSystemdUnit, "-f", false},
		{collection.ParamFormatAptPackage, "diffutils-", false},
		{collection.ParamFormatAptPackage, "jq/bookworm", false},
		{collection.ParamFormatAptPackage, "g++", true},
		{collection.ParamFormatRPMPackage, "@core", false},
		{collection.ParamFormatPackageVersion, "1.*", false},
		{collection.ParamFormatMountField, "/srv/data", true},
		{collection.ParamFormatMountField, "/mnt/a b", false},
	} {
		t.Run(string(tc.format)+" "+tc.value, func(t *testing.T) {
			in := registerSkewFormat(t, tc.format)
			plan, err := planOf([]engine.JournalEntry{madeEntry(in.identity, in.undo, map[string]any{"path": tc.value})}, nil)
			if tc.ok {
				if err != nil || len(plan.Steps()) != 1 {
					t.Fatalf("Build = %v, %d steps; want the undo planned", err, len(plan.Steps()))
				}
				return
			}
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, "would not pass on as one name") {
				t.Errorf("problems %+v, want one naming the rule, accepted by --leave alone", problems)
			}
		})
	}
}

// TestVerify_ARunnerHoldsRequiredParametersAndChoicesWhateverIsAccepted:
// the Runner's re-check holds a step to the same two rules, SkewAccepted or
// not.
func TestVerify_ARunnerHoldsRequiredParametersAndChoicesWhateverIsAccepted(t *testing.T) {
	in := registerSkew(t)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(madeRunbook))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		s    rollback.Step
		want string
	}{
		"no path, no digests":              {madeStep(wire.CollectionIdentity{}, "", map[string]any{"mode": 420}), `"path"`},
		"no path, the group's digest only": {madeStep(in.identity, "", map[string]any{"mode": 420}), `"path"`},
		"a state outside the choices":      {madeStep(in.identity, in.undo, map[string]any{"path": "/x", "state": "purged"}), "absent"},
		"no path, this Runner's own undo":  {madeStep(in.identity, in.undo, map[string]any{"mode": 420}), `"path"`},
	} {
		t.Run(name, func(t *testing.T) {
			for _, accepted := range []bool{false, true} {
				s := tc.s
				s.SkewAccepted = accepted
				if err := rollback.Verify(dag, throughJSON(t, s)); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("SkewAccepted %v: Verify = %v, want a refusal containing %q", accepted, err, tc.want)
				}
			}
		})
	}
	// Every value as a dispatch delivers it, a number become a float64: one
	// that fits its declaration in every respect passes.
	if err := rollback.Verify(dag, throughJSON(t, madeStep(in.identity, in.undo, map[string]any{"path": "/x", "mode": 420, "state": "absent"}))); err != nil {
		t.Errorf("a step every value of which fits, through JSON: %v", err)
	}
}

// TestBuild_TheGroupFallbackComparesTheGroupTheEntryRecorded: an entry
// that recorded an identity and no undo digest is compared with the digest
// of the group it recorded while this build holds that group, never with
// the group this build routes the method to, which a later release may
// change; only when this build holds none of the recorded group's methods
// is the routed group's digest compared.
func TestBuild_TheGroupFallbackComparesTheGroupTheEntryRecorded(t *testing.T) {
	in := registerSkew(t)
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "exec.rbother", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
		Reversibility: collection.Reversibility{Notes: "a fixture"}}}); err != nil {
		t.Fatal(err)
	}
	execDigest, err := collection.GroupDigest("exec")
	if err != nil {
		t.Fatal(err)
	}
	recordedUnderExec := wire.CollectionIdentity{Group: "exec", Version: "0.3.9", Manifests: execDigest}
	plan, err := planOf([]engine.JournalEntry{madeEntry(recordedUnderExec, "", map[string]any{"path": "/x"})}, nil)
	if err != nil {
		t.Fatalf("an entry recorded under a group this build holds at the digest it recorded: %v", err)
	}
	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0].Message, `"exec"`) {
		t.Errorf("warnings %+v, want the group this build routes the method to named", plan.Warnings)
	}
	// The recorded group's declarations changed: a skew.
	changed := recordedUnderExec
	changed.Manifests = other("c")
	if _, err := planOf([]engine.JournalEntry{madeEntry(changed, "", map[string]any{"path": "/x"})}, nil); err == nil {
		t.Error("an entry recorded under the exec group at other declarations was planned")
	}
	// A group this build holds no method of: the routed group decides.
	absent := wire.CollectionIdentity{Group: "net", Version: "0.3.9", Manifests: in.identity.Manifests}
	if _, err := planOf([]engine.JournalEntry{madeEntry(absent, "", map[string]any{"path": "/x"})}, nil); err != nil {
		t.Errorf("an entry naming a group this build lacks, at the routed group's digest: %v", err)
	}
}

// TestBuild_AllowSkewNamingANodeWithNothingToReplayIsRefused: on the CLI a
// node named to both --leave and --allow-skew is refused, since nothing of
// it is replayed; and on either tier a node whose entries changed nothing,
// or failed with an effect nobody knows, is refused, since neither the
// planner nor a Runner replays anything of it.
func TestBuild_AllowSkewNamingANodeWithNothingToReplayIsRefused(t *testing.T) {
	in := registerSkew(t)
	made := madeEntry(in.identity, in.undo, map[string]any{"path": "/x"})
	read := entry("run-A", "tasks[1]", "h1", 2, "file.rbmake", engine.OutcomeRan)
	failed := entry("run-A", "tasks[2]", "h1", 3, "file.rbmake", engine.OutcomeFailed)
	failed.FailureStage = engine.FailureStageAction
	entries := []engine.JournalEntry{made, read, failed}

	_, err := planOf(entries, func(r *rollback.Request) {
		r.Leave, r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{"tasks[0]": true}, map[string]bool{"tasks[0]": true}, true
		r.AllowUnknown = map[string]bool{"tasks[2]": true}
	})
	if problems := refused(t, err); len(problems) != 1 || problems[0].Accept != "" || !strings.Contains(problems[0].Reason, "drop --allow-skew tasks[0]") {
		t.Errorf("the CLI, --leave and --allow-skew on one node: problems %+v, want one saying to drop the skew name", problems)
	}
	plan, err := planOf(entries, func(r *rollback.Request) {
		r.Leave, r.AllowSkew = map[string]bool{"tasks[0]": true}, map[string]bool{"tasks[0]": true}
		r.AllowUnknown = map[string]bool{"tasks[2]": true}
	})
	if err != nil || len(plan.Left) != 1 {
		t.Errorf("the Controller, leave and allow_skew on one node: %+v, %v, want the change left", plan.Left, err)
	}

	for _, planner := range []bool{true, false} {
		for _, node := range []string{"tasks[1]", "tasks[2]"} {
			_, err := planOf(entries, func(r *rollback.Request) {
				r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{node: true}, planner
				r.AllowUnknown = map[string]bool{"tasks[2]": true}
			})
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Node != node || problems[0].Accept != "" || !strings.Contains(problems[0].Reason, "changed nothing") {
				t.Errorf("planner %v, %s: problems %+v, want one for it that nothing accepts", planner, node, problems)
			}
		}
	}
}

// TestBuild_AllowSkewOnANodeWithNoDigestsSaysNothingWasCompared: on the
// Controller, a node allow_skew names whose entry recorded neither an
// identity nor an undo digest is marked, and its warning says that nothing
// was compared rather than that its declarations matched.
func TestBuild_AllowSkewOnANodeWithNoDigestsSaysNothingWasCompared(t *testing.T) {
	registerSkew(t)
	plan, err := planOf([]engine.JournalEntry{madeEntry(wire.CollectionIdentity{}, "", map[string]any{"path": "/x"})},
		func(r *rollback.Request) { r.AllowSkew = map[string]bool{"tasks[0]": true} })
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for _, w := range plan.Warnings {
		said = append(said, w.Message)
	}
	joined := strings.Join(said, "\n")
	if !strings.Contains(joined, "nothing was compared") || strings.Contains(joined, "matches") {
		t.Errorf("warnings %q, want one saying nothing was compared, and none that the declarations match", said)
	}
}
