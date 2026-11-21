// Tests of facttest itself: what Observe attributes and refuses, and what
// Audit fails and notes.
package facttest

import (
	"context"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// thisPackage is this package's import path, as Audit is given it.
const thisPackage = "github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"

// emitParams is the facts a probe method emits: each key with its value.
type emitParams map[string]any

// probeInvoke stands in for a method's Invoke: it hands each fact in
// params["emit"] to Observe, as a fake RunbookContext's EmitFact would.
func probeInvoke(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	emit, _ := params["emit"].(emitParams)
	for _, key := range sortedKeys(emit) {
		Observe(key, emit[key])
	}
	return collection.Result{}, nil
}

// dispatchInvoke stands in for a generic method (svc.start) that runs
// another method's Invoke: what it emits is the outer method's.
func dispatchInvoke(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	inner, _ := collection.Lookup("facts.facttest_probe")
	return inner.Invoke(ctx, rc, device, params)
}

// silentInvoke stands in for a fact-declaring method no test makes emit.
func silentInvoke(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
	return collection.Result{}, nil
}

// register registers a read-only built-in named name, run by invoke and
// declaring specs, for the rest of t.
func register(t *testing.T, name string, invoke collection.Method, specs ...collection.FactSpec) {
	t.Helper()
	err := collection.Register(collection.Descriptor{Name: name, Invoke: invoke, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, NoCheckReason: "a test fixture",
		Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"}, Facts: specs}})
	if err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
}

// fixture registers the probe methods every test here uses, with an empty
// ledger, and restores both when t ends.
func fixture(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(resetLedger)
	resetLedger()
	register(t, "facts.facttest_probe", probeInvoke,
		collection.FactSpec{Key: "facttest.name", Kind: collection.FactStable, Type: collection.FactString, Description: "a name"},
		collection.FactSpec{Key: "facttest.count", Kind: collection.FactVolatile, Type: collection.FactNumber, Description: "a count"},
		collection.FactSpec{Key: "facttest.item.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "an item"})
	register(t, "facts.facttest_dispatch", dispatchInvoke,
		collection.FactSpec{Key: "facttest.name", Kind: collection.FactStable, Type: collection.FactString, Description: "a name"})
	register(t, "facts.facttest_silent", silentInvoke,
		collection.FactSpec{Key: "facttest.silent", Kind: collection.FactStable, Type: collection.FactString, Description: "never emitted"})
}

// resetLedger empties what Observe recorded.
func resetLedger() {
	ledger.Lock()
	defer ledger.Unlock()
	ledger.emitted = map[string]map[string]bool{}
	ledger.problems = nil
	ledger.seen = map[string]int{}
	ledger.unattributed = 0
	ledger.unattributedKeys = nil
}

// run invokes method with the facts emit.
func run(t *testing.T, method string, emit emitParams) {
	t.Helper()
	d, ok := collection.Lookup(method)
	if !ok {
		t.Fatalf("%s is not registered", method)
	}
	if _, err := d.Invoke(context.Background(), nil, nil, map[string]any{"emit": emit}); err != nil {
		t.Fatal(err)
	}
}

// audit is Audit over this package with no exemption, keeping only the
// lines about the probe methods, since other tests' methods are not these.
func audit(opts Options, partial string) (failures, notes []string) {
	return Audit(thisPackage, opts, partial)
}

// hasLine reports whether one of lines holds every one of parts.
func hasLine(lines []string, parts ...string) bool {
	return slices.ContainsFunc(lines, func(l string) bool {
		for _, p := range parts {
			if !strings.Contains(l, p) {
				return false
			}
		}
		return true
	})
}

func TestObserve_HoldsEachFactToTheEmittingMethodsDeclaration(t *testing.T) {
	fixture(t)
	run(t, "facts.facttest_probe", emitParams{
		"facttest.name":     "web1",
		"facttest.count":    3,
		"facttest.item.a":   map[string]any{"version": "1"},
		"facttest.unknown":  "x",
		"facttest.item.b":   "not an object",
		"facttest.item.bad": nil,
	})
	failures, notes := audit(Options{Exempt: map[string]string{"facts.facttest_silent": "a test fixture", "facts.facttest_dispatch": "a test fixture"}}, "")
	for _, want := range [][]string{
		{"facts.facttest_probe", `"facttest.unknown"`, "does not declare", "TestObserve_HoldsEachFactToTheEmittingMethodsDeclaration"},
		{"facts.facttest_probe", `"facttest.item.b"`, "a string, and the method declares it object"},
		{"facts.facttest_probe", `"facttest.item.bad"`, "null"},
	} {
		if !hasLine(failures, want...) {
			t.Errorf("no failure holds %q; failures: %q", want, failures)
		}
	}
	if len(failures) != 3 {
		t.Errorf("failures = %q, want exactly the three above", failures)
	}
	if hasLine(notes, "facts.facttest_probe", "declares") {
		t.Errorf("a note names a declared fact as unemitted, and every one was emitted: %q", notes)
	}
}

func TestObserve_AttributesAFactToTheOutermostMethod(t *testing.T) {
	fixture(t)
	run(t, "facts.facttest_dispatch", emitParams{"facttest.name": "web1", "facttest.count": 1})
	failures, _ := audit(Options{Exempt: map[string]string{"facts.facttest_silent": "a test fixture", "facts.facttest_probe": "a test fixture"}}, "")
	// The dispatcher declares only facttest.name, so its inner method's
	// facttest.count is refused as the dispatcher's, as the engine would.
	if !hasLine(failures, "facts.facttest_dispatch", `"facttest.count"`, "does not declare") || len(failures) != 1 {
		t.Errorf("failures = %q, want facttest.count refused as the dispatcher's alone", failures)
	}
}

func TestObserve_CountsAFactNoMethodEmitted(t *testing.T) {
	fixture(t)
	Observe("facttest.name", "from a helper")
	_, notes := audit(Options{Exempt: map[string]string{"facts.facttest_silent": "x", "facts.facttest_probe": "x", "facts.facttest_dispatch": "x"}}, "")
	if !hasLine(notes, "1 facts were emitted with no registered built-in", "facttest.name") {
		t.Errorf("notes = %q, want the unattributed fact counted", notes)
	}
}

func TestAudit_FailsAMethodNoTestMadeEmit(t *testing.T) {
	fixture(t)
	run(t, "facts.facttest_probe", emitParams{"facttest.name": "web1"})
	run(t, "facts.facttest_dispatch", emitParams{"facttest.name": "web1"})
	failures, notes := audit(Options{}, "")
	if !hasLine(failures, "facts.facttest_silent", "no test of "+thisPackage+" made it emit") || len(failures) != 1 {
		t.Errorf("failures = %q, want facts.facttest_silent named as undriven alone", failures)
	}
	for _, want := range []string{`"facttest.count"`, `"facttest.item."*`} {
		if !hasLine(notes, "facts.facttest_probe declares "+want, "which no test here emitted") {
			t.Errorf("notes = %q, want %s named as never emitted", notes, want)
		}
	}
}

func TestAudit_APartialOrUnavailableRunNotesWhatItCouldNotJudge(t *testing.T) {
	fixture(t)
	for _, tc := range []struct {
		opts    Options
		partial string
		why     string
	}{
		{Options{}, "-run chose only some tests", "-run chose only some tests"},
		{Options{Unavailable: "no /bin/sh"}, "", "no /bin/sh"},
	} {
		failures, notes := audit(tc.opts, tc.partial)
		if len(failures) != 0 || !hasLine(notes, "facts.facttest_silent was not driven", tc.why) {
			t.Errorf("%q: failures %q, notes %q; want no failure and a note naming why", tc.why, failures, notes)
		}
	}
}

func TestAudit_AnExemptionThatNoLongerHoldsFails(t *testing.T) {
	fixture(t)
	run(t, "facts.facttest_probe", emitParams{"facttest.name": "web1"})
	failures, _ := audit(Options{Exempt: map[string]string{
		"facts.facttest_probe":    "it needs a real device",
		"facts.facttest_silent":   "it needs a real device",
		"facts.facttest_dispatch": "it needs a real device",
		"facts.facttest_gone":     "it was removed",
	}}, "")
	if !hasLine(failures, "Exempt names facts.facttest_probe", "made it emit") {
		t.Errorf("failures = %q, want the driven method's exemption refused", failures)
	}
	if !hasLine(failures, "Exempt names facts.facttest_gone", "not a fact-declaring built-in") {
		t.Errorf("failures = %q, want the vanished method's exemption refused", failures)
	}
	if len(failures) != 2 {
		t.Errorf("failures = %q, want exactly those two", failures)
	}
}

func TestTypeProblem_IsTheStoresRule(t *testing.T) {
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	for _, tc := range []struct {
		want  collection.FactType
		value any
		ok    bool
	}{
		{collection.FactString, "x", true},
		{collection.FactString, 1, false},
		{collection.FactInteger, 3, true},
		{collection.FactInteger, 3.0, true}, // encodes as 3
		{collection.FactInteger, 3.5, false},
		{collection.FactInteger, huge, true},
		{collection.FactNumber, 3, true},
		{collection.FactNumber, 3.5, true},
		{collection.FactBoolean, false, true},
		{collection.FactList, []string{"a"}, true},
		{collection.FactList, []string(nil), false}, // encodes as null
		{collection.FactObject, map[string]int{"a": 1}, true},
		{collection.FactObject, struct{ A int }{1}, true},
		{collection.FactObject, nil, false},
		{collection.FactString, func() {}, false},
	} {
		if got := TypeProblem(tc.want, tc.value) == ""; got != tc.ok {
			t.Errorf("TypeProblem(%s, %#v) passes = %v, want %v (%s)", tc.want, tc.value, got, tc.ok, TypeProblem(tc.want, tc.value))
		}
	}
}

func TestPackageOf_ReadsTheImportPath(t *testing.T) {
	for in, want := range map[string]string{
		"example.com/x/apt.Install":                      "example.com/x/apt",
		"example.com/x/apt.init.func1":                   "example.com/x/apt",
		"example.com/x/apt.(*T).invoke-fm":               "example.com/x/apt",
		"example.com/x/apt.F[go.shape.struct { a/b.c }]": "example.com/x/apt",
		"main.main": "main",
	} {
		if got := PackageOf(in); got != want {
			t.Errorf("PackageOf(%q) = %q, want %q", in, got, want)
		}
	}
	if name, ok := testName("example.com/x/apt_test.TestInstall.func2"); !ok || name != "TestInstall" {
		t.Errorf("testName = %q, %v", name, ok)
	}
	if _, ok := testName("example.com/x/apt.Install"); ok {
		t.Error("a method's own function was read as a test")
	}
}

func TestBuiltins_FindsTheRegisteredMethodsByGroup(t *testing.T) {
	fixture(t)
	names := []string{}
	for _, d := range Builtins() {
		names = append(names, d.Name)
	}
	for _, want := range []string{"facts.facttest_dispatch", "facts.facttest_probe", "facts.facttest_silent"} {
		if !slices.Contains(names, want) {
			t.Errorf("Builtins() = %q, missing %s", names, want)
		}
	}
	if got := DeclaringMethods(thisPackage); len(got) != 3 {
		t.Errorf("DeclaringMethods(this package) = %v, want the three probes", got)
	}
}
