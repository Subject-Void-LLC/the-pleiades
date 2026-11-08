// Tests for holding a recorded undo to the declarations it was recorded
// under (skew.go): the Collection identity and undo digest a journal entry
// carries against this build's, and what --allow-skew (allow_skew) accepts.
package rollback_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// installed is this build's identity of the fixture group and its undo's
// digest, as registerSkew registers them.
type installed struct {
	identity wire.CollectionIdentity
	undo     string
}

// registerSkew registers, for the rest of the test, two methods of the
// first-party group "file": file.rbmake, whose undo is file.rbremove with
// path and mode recorded, and file.rbremove, which requires path and takes
// an int mode and a bool force. It returns what this build computes for
// them.
func registerSkew(t *testing.T) installed {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	removeParams := []collection.Param{{Name: "path", Type: "string", Required: true}, {Name: "mode", Type: "int"}, {Name: "force", Type: "bool"}}
	spec := sdk.InverseSpec{FQCN: "file.rbremove", Record: []string{"path", "mode"}}
	for _, d := range []collection.Descriptor{
		{Name: "file.rbremove", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "gone is gone"}, Doc: collection.Doc{Params: removeParams}}},
		{Name: "file.rbmake", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec}},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	id, err := collection.BuiltinIdentity("file.rbmake", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	target, _ := collection.ManifestOf("file.rbremove")
	return installed{identity: id, undo: collection.UndoDigest(spec, fragment.ByNameIn(target))}
}

// other is a digest no build of the fixtures computes.
func other(c string) string { return wire.ManifestDigestPrefix + strings.Repeat(c, 64) }

// madeEntry is run-A's tasks[0] on h1: file.rbmake with its undo recorded
// as given, under the identity and undo digest given.
func madeEntry(id wire.CollectionIdentity, undoDigest string, params map[string]any) engine.JournalEntry {
	e := undo(entry("run-A", "tasks[0]", "h1", 1, "file.rbmake", engine.OutcomeChanged), "file.rbremove", params)
	e.Collection, e.InverseSpecDigest = id, undoDigest
	return e
}

// planOf builds the rollback of run-A holding entries, with edit applied
// to the request.
func planOf(entries []engine.JournalEntry, edit func(*rollback.Request)) (rollback.Plan, error) {
	req := rollback.Request{Target: rollback.Run{ID: "run-A", Sealed: true, Entries: entries}, Devices: inventoryOf("h1")}
	if edit != nil {
		edit(&req)
	}
	return rollback.Build(req)
}

// olderBuild is the identity an older build recorded for the fixture group:
// another version, and other declarations.
func olderBuild(in installed) wire.CollectionIdentity {
	id := in.identity
	id.Version, id.Manifests = "0.3.9", other("a")
	return id
}

func TestBuild_ASkewlessUndoReplaysWithNoWarning(t *testing.T) {
	in := registerSkew(t)
	plan, err := planOf([]engine.JournalEntry{madeEntry(in.identity, in.undo, map[string]any{"path": "/x"})}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 1 || steps[0].SkewAccepted {
		t.Errorf("steps %+v, want one, not marked as an accepted skew", steps)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("warnings %+v, want none for an undo recorded under this build's declarations", plan.Warnings)
	}
}

// TestBuild_AChangedGroupWithAnUnchangedUndoReplaysWithAWarning: a group
// whose declarations changed elsewhere (a corrected document, another
// method) while this undo's declaration did not needs no flag, and says so
// naming both versions and both group digests.
func TestBuild_AChangedGroupWithAnUnchangedUndoReplaysWithAWarning(t *testing.T) {
	in := registerSkew(t)
	plan, err := planOf([]engine.JournalEntry{madeEntry(olderBuild(in), in.undo, map[string]any{"path": "/x"})}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 1 || steps[0].SkewAccepted {
		t.Errorf("steps %+v, want one replayed as recorded", steps)
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("warnings %+v, want one", plan.Warnings)
	}
	w := plan.Warnings[0]
	for _, want := range []string{`"0.3.9"`, `"` + buildinfo.Version() + `"`, other("a"), in.identity.Manifests, in.undo, "unchanged"} {
		if !strings.Contains(w.Message, want) {
			t.Errorf("the warning does not name %s: %s", want, w.Message)
		}
	}
	if w.Node != "tasks[0]" || w.DeviceID != "h1" || w.DeviceName != "name-of-h1" {
		t.Errorf("the warning is about %+v", w)
	}
}

// TestBuild_AChangedUndoIsRefusedUntilAcceptedByName: an undo whose own
// declaration changed is refused naming --allow-skew, both versions and
// both undo digests; named, it is planned, its step marked, and a warning
// says which digests the name accepted.
func TestBuild_AChangedUndoIsRefusedUntilAcceptedByName(t *testing.T) {
	in := registerSkew(t)
	entries := []engine.JournalEntry{madeEntry(olderBuild(in), other("b"), map[string]any{"path": "/x"})}
	_, err := planOf(entries, nil)
	problems := refused(t, err)
	if len(problems) != 1 || problems[0].Accept != "--allow-skew tasks[0]" {
		t.Fatalf("problems %+v, want one accepted by --allow-skew tasks[0]", problems)
	}
	for _, want := range []string{`"0.3.9"`, `"` + buildinfo.Version() + `"`, other("b"), in.undo, other("a"), in.identity.Manifests} {
		if !strings.Contains(problems[0].Reason, want) {
			t.Errorf("the refusal does not name %s: %s", want, problems[0].Reason)
		}
	}

	for _, planner := range []bool{true, false} {
		plan, err := planOf(entries, func(r *rollback.Request) {
			r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{"tasks[0]": true}, planner
		})
		if err != nil {
			t.Fatalf("planner %v: Build with --allow-skew: %v", planner, err)
		}
		steps := plan.Steps()
		if len(steps) != 1 || !steps[0].SkewAccepted || steps[0].UndoDigest != other("b") {
			t.Errorf("planner %v: steps %+v, want the undo marked as an accepted skew", planner, steps)
		}
		if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0].Message, "accepted") ||
			!strings.Contains(plan.Warnings[0].Message, other("b")) || !strings.Contains(plan.Warnings[0].Message, in.undo) {
			t.Errorf("planner %v: warnings %+v, want one naming the two undo digests accepted", planner, plan.Warnings)
		}
	}
}

// TestBuild_AllowSkewNeverAcceptsWhatTheBuildDoesNotDeclare: whatever
// --allow-skew names, a recorded key the installed undo does not record, a
// recorded value of another type than the installed parameter, and a
// parameter the installed undo method requires that the run did not record
// are each refused with --leave alone, naming both versions.
func TestBuild_AllowSkewNeverAcceptsWhatTheBuildDoesNotDeclare(t *testing.T) {
	in := registerSkew(t)
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"a key the installed Record lacks": {map[string]any{"path": "/x", "force": true}, `"force"`},
		"a value of another type":          {map[string]any{"path": "/x", "mode": "0644"}, "declares it int"},
		"a required key not recorded":      {map[string]any{"mode": 420}, `requires "path"`},
	} {
		t.Run(name, func(t *testing.T) {
			entries := []engine.JournalEntry{madeEntry(olderBuild(in), other("b"), tc.params)}
			for _, allow := range []bool{false, true} {
				_, err := planOf(entries, func(r *rollback.Request) {
					if allow {
						r.AllowSkew = map[string]bool{"tasks[0]": true}
					}
				})
				problems := refused(t, err)
				if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, tc.want) {
					t.Fatalf("allow-skew %v: problems %+v, want one naming %s accepted by --leave alone", allow, problems, tc.want)
				}
				if !strings.Contains(problems[0].Reason, `"0.3.9"`) || !strings.Contains(problems[0].Reason, `"`+buildinfo.Version()+`"`) {
					t.Errorf("allow-skew %v: the refusal does not name both versions: %s", allow, problems[0].Reason)
				}
			}
		})
	}
	// A value whose type fits is planned, under the same flag.
	if _, err := planOf([]engine.JournalEntry{madeEntry(olderBuild(in), other("b"), map[string]any{"path": "/x", "mode": 420})},
		func(r *rollback.Request) { r.AllowSkew = map[string]bool{"tasks[0]": true} }); err != nil {
		t.Errorf("a recorded int under an int parameter: %v", err)
	}
}

// TestBuild_RefusesAnIdentityNoBuildWrites: a malformed identity, a
// program's identity on an entry no program ran, and a built-in's identity
// on an entry a program ran are refused with --leave, whatever --allow-skew
// names: the journal's projection writes none of them.
func TestBuild_RefusesAnIdentityNoBuildWrites(t *testing.T) {
	in := registerSkew(t)
	short := in.identity
	short.Manifests = short.Manifests[:len(short.Manifests)-1]
	program := wire.CollectionIdentity{Group: "file", Program: true, Manifests: other("c")}
	ranByProgram := madeEntry(in.identity, in.undo, map[string]any{"path": "/x"})
	ranByProgram.ProviderProgram, ranByProgram.ProviderDigest = "file", other("d")
	for name, tc := range map[string]struct {
		e    engine.JournalEntry
		want string
	}{
		"a 63-digit digest":                    {madeEntry(short, in.undo, map[string]any{"path": "/x"}), "no build writes"},
		"a program's identity, no program ran": {madeEntry(program, in.undo, map[string]any{"path": "/x"}), "names no program"},
		"a built-in's identity, a program ran": {ranByProgram, "first-party"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := planOf([]engine.JournalEntry{tc.e}, func(r *rollback.Request) { r.AllowSkew = map[string]bool{"tasks[0]": true} })
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Accept != "--leave tasks[0]" || !strings.Contains(problems[0].Reason, tc.want) {
				t.Errorf("problems %+v, want one naming %q accepted by --leave", problems, tc.want)
			}
		})
	}
}

// TestBuild_AnEntryWithNoIdentityIsCheckedAndSaysSo: an entry that
// recorded no identity (format 1, a row from before the columns, or an
// identity a build could not compute) replays as before with a warning
// that says only what is known; one that recorded an undo digest all the
// same is held to it.
func TestBuild_AnEntryWithNoIdentityIsCheckedAndSaysSo(t *testing.T) {
	in := registerSkew(t)
	plan, err := planOf([]engine.JournalEntry{madeEntry(wire.CollectionIdentity{}, "", map[string]any{"path": "/x"})}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0].Message, "recorded with no Collection identity") ||
		strings.Contains(plan.Warnings[0].Message, "before 0.4.0") {
		t.Errorf("warnings %+v, want one saying it was recorded with no Collection identity", plan.Warnings)
	}

	// No identity, an undo digest this build computes: replayed, and said.
	plan, err = planOf([]engine.JournalEntry{madeEntry(wire.CollectionIdentity{}, in.undo, map[string]any{"path": "/x"})}, nil)
	if err != nil || len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0].Message, in.undo) {
		t.Errorf("no identity, this build's undo digest: %+v, %v", plan.Warnings, err)
	}
	// No identity, another undo digest: a skew like any other.
	_, err = planOf([]engine.JournalEntry{madeEntry(wire.CollectionIdentity{}, other("b"), map[string]any{"path": "/x"})}, nil)
	if problems := refused(t, err); len(problems) != 1 || problems[0].Accept != "--allow-skew tasks[0]" {
		t.Errorf("no identity, another undo digest: %+v", problems)
	}
	// An identity, no undo digest: the group's digest decides.
	_, err = planOf([]engine.JournalEntry{madeEntry(olderBuild(in), "", map[string]any{"path": "/x"})}, nil)
	if problems := refused(t, err); len(problems) != 1 || problems[0].Accept != "--allow-skew tasks[0]" {
		t.Errorf("an identity of other declarations and no undo digest: %+v", problems)
	}
	if _, err := planOf([]engine.JournalEntry{madeEntry(in.identity, "", map[string]any{"path": "/x"})}, nil); err != nil {
		t.Errorf("this build's identity and no undo digest: %v", err)
	}
}

// TestBuild_AMovedGroupWarnsAndRefusesNothing: a method a later release
// moved to another group reads, against the group its entry recorded, as a
// warning, never a refusal: a minor release may move one (FAILURE_PATTERNS
// 988), and the undo's own digest decides.
func TestBuild_AMovedGroupWarnsAndRefusesNothing(t *testing.T) {
	in := registerSkew(t)
	moved := in.identity
	moved.Group = "net"
	plan, err := planOf([]engine.JournalEntry{madeEntry(moved, in.undo, map[string]any{"path": "/x"})}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0].Message, `"net"`) || !strings.Contains(plan.Warnings[0].Message, `"file"`) {
		t.Errorf("warnings %+v, want one naming the group recorded and the group this build routes it to", plan.Warnings)
	}
}

// TestBuild_AllowSkewNamingNoSkewDependsOnWhoRunsIt is finding 1's
// deadlock: where the planner runs the steps (the CLI), a node named that
// has no skew is refused, nothing accepting it but dropping the name; where
// a Runner of another build runs them (the Controller), the node is marked
// accepted all the same, since the Runner's declarations may differ.
func TestBuild_AllowSkewNamingNoSkewDependsOnWhoRunsIt(t *testing.T) {
	in := registerSkew(t)
	for name, e := range map[string]engine.JournalEntry{
		"this build's declarations": madeEntry(in.identity, in.undo, map[string]any{"path": "/x"}),
		"no identity recorded":      madeEntry(wire.CollectionIdentity{}, "", map[string]any{"path": "/x"}),
	} {
		t.Run(name, func(t *testing.T) {
			named := func(planner bool) func(*rollback.Request) {
				return func(r *rollback.Request) {
					r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{"tasks[0]": true}, planner
				}
			}
			_, err := planOf([]engine.JournalEntry{e}, named(true))
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Accept != "" || !strings.Contains(problems[0].Reason, "drop --allow-skew tasks[0]") {
				t.Errorf("the CLI: problems %+v, want one nothing accepts, saying to drop the name", problems)
			}

			plan, err := planOf([]engine.JournalEntry{e}, named(false))
			if err != nil {
				t.Fatalf("the Controller: %v", err)
			}
			if steps := plan.Steps(); len(steps) != 1 || !steps[0].SkewAccepted {
				t.Errorf("the Controller: steps %+v, want the undo marked as an accepted skew for its Runner", steps)
			}
		})
	}
}

// TestBuild_AllowSkewNamingANodeNotInTheJournalIsRefused: on either tier, a
// name that matches no entry accepts nothing and is refused.
func TestBuild_AllowSkewNamingANodeNotInTheJournalIsRefused(t *testing.T) {
	in := registerSkew(t)
	for _, planner := range []bool{true, false} {
		_, err := planOf([]engine.JournalEntry{madeEntry(in.identity, in.undo, map[string]any{"path": "/x"})}, func(r *rollback.Request) {
			r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{"tasks[9]": true}, planner
		})
		problems := refused(t, err)
		if len(problems) != 1 || problems[0].Node != "tasks[9]" || problems[0].Accept != "" || !strings.Contains(problems[0].Reason, "run-A") {
			t.Errorf("planner %v: problems %+v, want one for tasks[9] that nothing accepts", planner, problems)
		}
	}
}

// TestBuild_AGroupHoldingAFormatCharacterIsPrintedEscaped: Shape lets a
// stored group hold a direction override (only Valid refuses one, and only
// a writer holds to Valid), so every line naming it prints it quoted, never
// raw.
func TestBuild_AGroupHoldingAFormatCharacterIsPrintedEscaped(t *testing.T) {
	in := registerSkew(t)
	override := string(rune(0x202e))
	odd := olderBuild(in)
	odd.Group = "fi" + override + "le"
	if err := odd.Shape(); err != nil {
		t.Fatalf("the fixture identity fails Shape: %v", err)
	}
	_, err := planOf([]engine.JournalEntry{madeEntry(odd, other("b"), map[string]any{"path": "/x"})}, nil)
	problems := refused(t, err)
	if len(problems) != 1 || strings.Contains(problems[0].Reason, override) || !strings.Contains(problems[0].Reason, strconv.Quote(odd.Group)) {
		t.Errorf("problems %+v, want the group printed escaped", problems)
	}
	plan, err := planOf([]engine.JournalEntry{madeEntry(odd, other("b"), map[string]any{"path": "/x"})},
		func(r *rollback.Request) { r.AllowSkew = map[string]bool{"tasks[0]": true} })
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan.Warnings)
	for _, w := range plan.Warnings {
		if strings.Contains(w.Message, override) {
			t.Errorf("a warning prints the group raw: %s", encoded)
		}
	}
	if len(plan.Warnings) == 0 {
		t.Error("an accepted skew under a group of another name warned of nothing")
	}
}

// TestBuild_AllowSkewOnAnAuthoredUndoIsNeedless: a runbook's rollback: list
// is held to the runbook, never to a Collection's declarations, so on the
// CLI naming its node to --allow-skew is refused, and so is naming one
// whose list says there is nothing to undo; a Runner's re-check has nothing
// to note for either.
func TestBuild_AllowSkewOnAnAuthoredUndoIsNeedless(t *testing.T) {
	in := registerSkew(t)
	e := madeEntry(in.identity, in.undo, map[string]any{"path": "/x"})
	e.AuthoredRollback = true
	for want, tasks := range map[string][]engine.Task{
		"held to the runbook": {{Name: "remove it", FQCN: "file.rbremove", Params: map[string]any{"path": "/x"}}},
		"nothing to undo":     {},
	} {
		_, err := planOf([]engine.JournalEntry{e}, func(r *rollback.Request) {
			r.AllowSkew, r.ExecutorIsPlanner = map[string]bool{"tasks[0]": true}, true
			r.Authored = func(string) ([]engine.Task, error) { return tasks, nil }
		})
		problems := refused(t, err)
		if len(problems) != 1 || problems[0].Accept != "" || !strings.Contains(problems[0].Reason, want) {
			t.Errorf("problems %+v, want one nothing accepts, saying %q", problems, want)
		}
	}
	authored := madeStep(olderBuild(in), other("b"), map[string]any{"path": "/x"})
	authored.Source = rollback.SourceAuthored
	undeclared := madeStep(olderBuild(in), other("b"), map[string]any{"path": "/x"})
	undeclared.FQCN = "file.rbmake"
	for name, s := range map[string]rollback.Step{"an authored step": authored, "an undo the emitter does not declare": undeclared} {
		if note := rollback.SkewNote(s); note != "" {
			t.Errorf("%s has a skew note: %s", name, note)
		}
	}
}
