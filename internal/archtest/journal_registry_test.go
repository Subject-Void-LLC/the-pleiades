// This file is the second half of Phase 40's build-order step 4: the
// registry-driven assertion that every string the run journal's
// projection can put into StatKeys, ParamKeys or InverseParamKeys is
// either a pkg/sdk stat constant or a name some registered FQCN's own
// Doc declares.
//
// journal_test.go's two rules are claims about a struct. This one is a
// claim about a run, and it needs a run because the guarantee it covers
// is not a property of engine.JournalEntry's type at all: StatKeys and a
// hypothetical StatValues are both []string, and only the intersection
// against the registry decides which one a build actually produces.
//
// # Why it lives in internal/archtest rather than beside the projection
//
// internal/engine blank-imports no Collection package, deliberately: a
// Collection may import only pkg/, and the engine dispatches through
// pkg/collection.Lookup rather than through a compile-time table. So
// inside internal/engine's own tests the registry is empty, every FQCN
// resolves to an engine builtin or to the sentinel, and the entire
// Doc.Returns/Doc.Params intersection is unreachable. FuzzProjectLevel
// covers the projection from that side, against the engine's own action
// table. This covers the side that only a package holding the whole
// catalog can see, which is what internal/archtest already is.
//
// # What is real here and what stands in
//
// Real: the registry (every Collection package's init has run), the
// Builder, the Executor, the projection, the Journal port, and the two
// pkg/sdk record writers. The FQCN each task names is a real registered
// one, so resolveFQCN takes its collection.Lookup branch and the key
// rules intersect against that method's real Doc.
//
// Standing in: the ActionExecutor, which supplies the stats a
// device-backed method would have produced. That substitution is the
// point rather than a compromise. Running 81 real Collection methods
// would need 81 real devices, and what is under test is not what a
// method returns, it is what the projection does with whatever comes
// back. RULE 0's own gate for the end-to-end claim is Section 9's
// release-gate runbook against real sshd, which reads the stored bytes
// back through a path that bypasses the writer entirely; this rule is
// the one that covers the whole catalog at once, which no single runbook
// can.
//
// # What it does not cover
//
// FQCNs the registry does not hold. The engine dispatches seven names
// itself (three builtins and the four transport bindings), and those
// carry no Descriptor and therefore no Doc at all, so their declared
// stat keys come from a static table inside internal/engine and are
// pinned there instead: TestJournalNamesEveryTransportStat against a
// real transport run, and FuzzProjectLevel against arbitrary input.
package archtest

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // triggers every Collection package's init(), which is what makes this registry driven
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// journalProbeValue is the value planted under every param key and every
// stat key this file feeds the projection.
//
// One value, used everywhere, so a single search over the marshaled
// entry answers "did any value at all survive". It is deliberately not
// a plausible-looking secret: a sentinel that could never be a key name,
// a method name or a digest means a hit is a leak rather than a
// coincidence worth arguing about.
const journalProbeValue = "PROBE-VALUE-NO-ENTRY-MAY-EVER-CARRY-THIS"

// journalUndeclaredKeys are key names no method's Doc declares, fed in
// beside the declared ones so every assertion below is two-sided: the
// declared names must survive AND these must not.
//
// The first is not invented. internal/catalog/http/request.go builds its
// headers stat keyed by the device's own response header names, lower
// cased, so "set-cookie" is a key a real device really can write into a
// real stats map. The second is a name nothing could produce by
// accident. TestEveryProjectedKeyNameIsRegistryDeclared asserts neither
// is declared anywhere before it relies on them, so a catalog method
// that legitimately grows a "set-cookie" param fails loudly here instead
// of quietly turning this rule into a tautology.
var journalUndeclaredKeys = []string{"set-cookie", "x-archtest-undeclared-key"}

// journalStatCollector is a minimal sdk.RunbookContext that keeps what a
// method writes, so this file can build a stats map by calling
// sdk.RecordInverse and sdk.RecordDiff rather than by hand-writing the
// maps they produce.
//
// Going through the real writers is not convenience. The two keys inside
// an inverse record ("fqcn" and "params") are bare string literals in
// pkg/sdk with no exported constant, so internal/engine holds a second
// copy of them to read the record back. Nothing links the two copies. By
// producing the record through sdk.RecordInverse, this test fails the
// moment those literals and the engine's copy of them disagree: the
// projection would stop finding the name, resolve the empty string, and
// record the FQCNUnregistered sentinel instead of the method.
type journalStatCollector struct {
	stats map[string]interface{}
}

// InjectSecrets implements sdk.RunbookContext. It returns nothing: this
// collector never runs a real method, so there is no credential to hand
// one.
func (c *journalStatCollector) InjectSecrets() map[string]string { return nil }

// SetStat implements sdk.RunbookContext by keeping the value.
func (c *journalStatCollector) SetStat(key string, value interface{}) error {
	c.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext. Facts do not reach the run
// journal at all (a fact is a durable device observation, recorded by a
// different subsystem), so this keeps nothing.
func (c *journalStatCollector) EmitFact(string, interface{}) error { return nil }

// journalProbeActions is an engine.ActionExecutor that reports one fixed
// stats map for whatever task it is handed. See this file's own doc
// comment for why the action is the one thing here that stands in.
type journalProbeActions struct {
	stats map[string]interface{}
}

// Execute implements engine.ActionExecutor, always reporting a change so
// the projection takes its OutcomeChanged branch, which is the branch a
// method emitting an inverse would really have taken.
func (a journalProbeActions) Execute(context.Context, *engine.Task, inventory.InventoryItem) (engine.ActionResult, error) {
	return engine.ActionResult{Changed: true, Stats: a.stats}, nil
}

// journalProbeResolver is an engine.TargetResolver that answers every
// non-empty target with one active device, and an empty target with
// none.
//
// Both halves are needed and neither is decoration. Almost every probe
// task below names no target and runs controller-side, which is the
// cheapest shape that still reaches the projection. One does not:
// net.netconf.config declares a param called "target", and
// engine.TaskTarget reads exactly that key to decide where a task fans
// out, so planting a probe value under it sends the run looking for a
// device by that name. Resolving one keeps that method on the same code
// path as the rest instead of failing it at the resolve_target stage
// with no stats to assert over, and it costs one stub. The alternative,
// special-casing the literal "target" here, would put a third copy of
// the engine's own routing key in the tree.
type journalProbeResolver struct{}

// Resolve implements engine.TargetResolver.
func (journalProbeResolver) Resolve(target string) []inventory.InventoryItem {
	if target == "" {
		return nil
	}
	return []inventory.InventoryItem{&inventorytest.Stub{
		StubID:    "journal-probe-device",
		StubName:  "journal-probe-device",
		StubState: inventory.StateActive,
	}}
}

// journalCaptureSink is an engine.Journal that keeps every entry it is
// handed.
type journalCaptureSink struct {
	entries []engine.JournalEntry
}

// Record implements engine.Journal.
func (s *journalCaptureSink) Record(_ context.Context, entries []engine.JournalEntry) error {
	s.entries = append(s.entries, entries...)
	return nil
}

// journalDeclaredNames returns the sorted, de-duplicated names a
// method's Doc declares, for params and for returns.
//
// De-duplication matters because a Doc is hand-written data: two entries
// naming the same key would otherwise make the expected vector longer
// than the one the projection produces, and the failure would read as a
// projection bug rather than as the documentation bug it is.
func journalDeclaredNames(doc collection.Doc) (params, returns []string) {
	params = journalSortedSet(func(add func(string)) {
		for _, p := range doc.Params {
			add(p.Name)
		}
	})
	returns = journalSortedSet(func(add func(string)) {
		for _, r := range doc.Returns {
			add(r.Name)
		}
	})
	return params, returns
}

// journalSortedSet collects whatever emit hands it into a sorted set.
func journalSortedSet(emit func(add func(string))) []string {
	seen := map[string]bool{}
	var out []string
	emit(func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	})
	sort.Strings(out)
	return out
}

// journalProbeParams builds the task params for one method: every name
// its Doc declares, plus the undeclared ones, each carrying the probe
// value.
//
// A method that declares a param named "target" therefore sends the run
// looking for a device by that name, which is what journalProbeResolver
// is there to answer.
func journalProbeParams(declared []string) map[string]interface{} {
	params := map[string]interface{}{}
	for _, name := range declared {
		params[name] = journalProbeValue
	}
	for _, name := range journalUndeclaredKeys {
		params[name] = journalProbeValue
	}
	return params
}

// journalProbeStats builds the stats one method's stand-in action
// reports: every name its Doc.Returns declares, the undeclared ones, and
// the two pkg/sdk records written through pkg/sdk's own writers.
//
// The inverse names the same method as the task, so the projection's
// inverse resolution and its inverse param intersection are exercised
// against a real Doc too, rather than against whichever method happened
// to be convenient.
func journalProbeStats(t *testing.T, fqcn string, declaredReturns, declaredParams []string) map[string]interface{} {
	t.Helper()

	collector := &journalStatCollector{stats: map[string]interface{}{}}
	for _, name := range declaredReturns {
		collector.stats[name] = journalProbeValue
	}
	for _, name := range journalUndeclaredKeys {
		collector.stats[name] = journalProbeValue
	}

	// A diff carries the device content the entry may report only as a
	// bool, so it is built from the probe value on both sides.
	if err := sdk.RecordDiff(collector, sdk.Diff{
		Before: map[string]interface{}{"content": journalProbeValue},
		After:  map[string]interface{}{"content": journalProbeValue},
	}); err != nil {
		t.Fatalf("recording the probe diff for %q: %v", fqcn, err)
	}

	inverseParams := map[string]interface{}{}
	for _, name := range declaredParams {
		inverseParams[name] = journalProbeValue
	}
	for _, name := range journalUndeclaredKeys {
		inverseParams[name] = journalProbeValue
	}
	if err := sdk.RecordInverse(collector, sdk.Inverse{FQCN: fqcn, Params: inverseParams}); err != nil {
		t.Fatalf("recording the probe inverse for %q: %v", fqcn, err)
	}

	return collector.stats
}

// journalProbeRunbook renders the one-task runbook that drives fqcn
// through the real Builder.
//
// It is marshaled rather than assembled with string concatenation
// because a param key here is catalog data: a name carrying a quote or a
// backslash would produce a runbook that fails to compile and a failure
// message about JSON rather than about the rule.
func journalProbeRunbook(t *testing.T, fqcn string, params map[string]interface{}) []byte {
	t.Helper()

	payload, err := json.Marshal(map[string]interface{}{
		"id": "journal-registry-probe",
		"tasks": []map[string]interface{}{{
			"name":   "probe",
			"fqcn":   fqcn,
			"params": params,
		}},
	})
	if err != nil {
		t.Fatalf("marshaling the probe runbook for %q: %v", fqcn, err)
	}
	return payload
}

// journalRunProbe compiles and runs the one-task probe runbook for fqcn
// and returns the single entry it journaled.
func journalRunProbe(t *testing.T, fqcn string, params, stats map[string]interface{}) engine.JournalEntry {
	t.Helper()

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := engine.NewBuilder(eval).Build(journalProbeRunbook(t, fqcn, params))
	if err != nil {
		t.Fatalf("compiling the probe runbook for %q: %v", fqcn, err)
	}

	sink := &journalCaptureSink{}
	x := engine.NewExecutor(journalProbeResolver{}, journalProbeActions{stats: stats},
		lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithJournal(sink))

	if _, err := x.Run(context.Background(), dag); err != nil {
		t.Fatalf("running the probe runbook for %q: %v", fqcn, err)
	}
	if len(sink.entries) != 1 {
		t.Fatalf("the probe runbook for %q journaled %d entries, want the one task's own", fqcn, len(sink.entries))
	}
	return sink.entries[0]
}

// journalAdmissibleNames is every string the projection is allowed to
// put into StatKeys, ParamKeys or InverseParamKeys for a registered
// FQCN: a name some Doc in the registry declares, or one of the two
// pkg/sdk stat constants.
//
// It is the union across the WHOLE registry rather than per method on
// purpose. Build-order step 4 states the rule that way, and the wider
// set is the weaker claim, so a rule phrased against it cannot pass by
// accident of a narrow expectation. The per-method assertion in the test
// below is the strict one; this is the invariant that survives if that
// expectation is ever loosened.
func journalAdmissibleNames(t *testing.T) map[string]bool {
	t.Helper()

	admissible := map[string]bool{sdk.StatInverse: true, sdk.StatDiff: true}
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Fatalf("catalog entry %q is not registered, so this sweep would examine a partial registry", cfg.Name)
		}
		params, returns := journalDeclaredNames(desc.Manifest.Doc)
		for _, name := range append(params, returns...) {
			admissible[name] = true
		}
	}
	return admissible
}

// TestEveryProjectedKeyNameIsRegistryDeclared drives the real
// engine.Executor over every registered FQCN and asserts that the key
// names reaching a journal entry are exactly the ones that method's own
// Doc declares, plus the two pkg/sdk stat constants, and that no value
// reaches one at all.
//
// The assertion is two-sided for the reason FAILURE_PATTERNS.md #120
// records: a projection that dropped every key would satisfy "no
// undeclared key survives" perfectly and be useless, and that is exactly
// the shape over-masking took the last time it shipped green. So each
// method's declared names must be present, the undeclared ones must be
// absent and counted, and the probe value must appear nowhere in the
// marshaled entry.
//
// A note on what the counts mean here. UndeclaredStatCount and
// UndeclaredParamCount are asserted as exactly len(journalUndeclaredKeys)
// because this test feeds a known set. That is not a claim that a real
// run's counts are small: a real task naming a target contributes one
// undeclared param all by itself, since "target" is the engine's own
// routing key and no method's Doc declares it.
func TestEveryProjectedKeyNameIsRegistryDeclared(t *testing.T) {
	if len(catalogdata.Collections) == 0 {
		t.Fatal("the catalog is empty, so this sweep would examine nothing")
	}

	admissible := journalAdmissibleNames(t)

	// The undeclared probe keys have to really be undeclared, or every
	// "must not survive" assertion below silently becomes a tautology.
	for _, key := range journalUndeclaredKeys {
		if admissible[key] {
			t.Fatalf("%q is declared by some method's Doc, so it cannot serve as this file's undeclared probe key", key)
		}
	}

	// Proof that the sweep really exercised the intersection rather than
	// only methods with empty Docs, counted rather than assumed.
	methodsWithReturns, methodsWithParams, inversesResolved := 0, 0, 0

	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Errorf("catalog entry %q is not registered", cfg.Name)
			continue
		}

		declaredParams, declaredReturns := journalDeclaredNames(desc.Manifest.Doc)
		if len(declaredReturns) > 0 {
			methodsWithReturns++
		}
		if len(declaredParams) > 0 {
			methodsWithParams++
		}

		params := journalProbeParams(declaredParams)
		stats := journalProbeStats(t, desc.Name, declaredReturns, declaredParams)
		entry := journalRunProbe(t, desc.Name, params, stats)

		// The name is the descriptor's own, resolved rather than copied.
		if entry.FQCN != desc.Name || entry.FQCNUnresolved {
			t.Errorf("%s journaled FQCN %q (unresolved %v), want the registered name resolved",
				desc.Name, entry.FQCN, entry.FQCNUnresolved)
		}
		if entry.InverseFQCN != desc.Name || entry.InverseFQCNUnresolved {
			t.Errorf("%s journaled InverseFQCN %q (unresolved %v), want the same registered name. "+
				"A sentinel here usually means pkg/sdk renamed a key inside the inverse record and "+
				"internal/engine's second copy of that literal was not updated with it",
				desc.Name, entry.InverseFQCN, entry.InverseFQCNUnresolved)
		} else {
			inversesResolved++
		}
		if !entry.DiffRecorded {
			t.Errorf("%s journaled DiffRecorded false, want the presence of the recorded diff", desc.Name)
		}

		// Exactly the declared names, plus the two pkg/sdk constants for
		// the stats, and nothing else.
		journalAssertKeys(t, desc.Name, "StatKeys", entry.StatKeys,
			journalWithSDKStatKeys(declaredReturns))
		journalAssertKeys(t, desc.Name, "ParamKeys", entry.ParamKeys, declaredParams)
		journalAssertKeys(t, desc.Name, "InverseParamKeys", entry.InverseParamKeys, declaredParams)

		journalAssertCount(t, desc.Name, "UndeclaredStatCount", entry.UndeclaredStatCount)
		journalAssertCount(t, desc.Name, "UndeclaredParamCount", entry.UndeclaredParamCount)
		journalAssertCount(t, desc.Name, "UndeclaredInverseParamCount", entry.UndeclaredInverseParamCount)

		// The rule as build-order step 4 states it, applied to whatever
		// this entry really carries rather than to what it was expected to.
		for _, group := range [][]string{entry.StatKeys, entry.ParamKeys, entry.InverseParamKeys} {
			for _, name := range group {
				if !admissible[name] {
					t.Errorf("%s journaled the key name %q, which no registered method's Doc declares "+
						"and which is neither pkg/sdk stat constant", desc.Name, name)
				}
			}
		}

		journalAssertNoValueSurvived(t, desc.Name, entry)
	}

	// Without these the sweep could pass having run 81 methods that
	// declare nothing, which would prove the intersection admits nothing
	// rather than that it admits the right things.
	if methodsWithReturns == 0 {
		t.Error("no registered method declares a Doc.Returns name, so the stat intersection was never exercised")
	}
	if methodsWithParams == 0 {
		t.Error("no registered method declares a Doc.Params name, so the param intersection was never exercised")
	}
	if inversesResolved == 0 {
		t.Error("no inverse record resolved, so the inverse half of this rule was never exercised")
	}
}

// journalWithSDKStatKeys returns declared plus the two pkg/sdk stat
// constants, sorted and de-duplicated.
//
// The de-duplication is load bearing rather than defensive: some methods
// really do declare "inverse" or "diff" in their own Doc.Returns, and
// those two names are also admitted unconditionally, so a naive append
// would expect them twice.
func journalWithSDKStatKeys(declared []string) []string {
	return journalSortedSet(func(add func(string)) {
		for _, name := range declared {
			add(name)
		}
		add(sdk.StatInverse)
		add(sdk.StatDiff)
	})
}

// journalAssertKeys compares one key vector against what the registry
// declares, naming the method and the field so a failure says which
// catalog entry to go read.
//
// It compares elementwise rather than through reflect.DeepEqual because
// a method that declares nothing produces an empty want and a nil got,
// which DeepEqual reports as different and which mean the same thing
// here: no key was named.
func journalAssertKeys(t *testing.T, fqcn, field string, got, want []string) {
	t.Helper()

	if slices.Equal(got, want) {
		return
	}
	t.Errorf("%s journaled %s %v, want exactly the names its own Doc declares: %v", fqcn, field, got, want)
}

// journalAssertCount pins one undeclared counter to the number of
// undeclared keys this file planted. Nothing may vanish silently, which
// is the shape LESSONS_LEARNED.md #71 exists to forbid, so a refused key
// has to show up somewhere and the counter is the somewhere.
func journalAssertCount(t *testing.T, fqcn, field string, got int) {
	t.Helper()

	if got != len(journalUndeclaredKeys) {
		t.Errorf("%s journaled %s %d, want %d: every key the whitelist refuses is counted, never dropped",
			fqcn, field, got, len(journalUndeclaredKeys))
	}
}

// journalAssertNoValueSurvived marshals the entry and fails if the probe
// value or an undeclared key name appears anywhere in it.
//
// Marshaling is the point. Asserting field by field can only cover the
// fields whoever wrote the assertion thought of, and the failure this
// guards against is a field a later phase adds. A search over the whole
// serialized entry covers every field the type has, including one added
// after this test was written.
func journalAssertNoValueSurvived(t *testing.T, fqcn string, entry engine.JournalEntry) {
	t.Helper()

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshaling %s's journal entry: %v", fqcn, err)
	}
	rendered := string(encoded)

	if strings.Contains(rendered, journalProbeValue) {
		t.Errorf("%s's journal entry carries the probe value: %s.\n"+
			"A journal entry stores key NAMES, counts and enums. If a field now needs the value "+
			"under a key, the field does not belong on the entry.", fqcn, rendered)
	}
	for _, key := range journalUndeclaredKeys {
		if strings.Contains(rendered, key) {
			t.Errorf("%s's journal entry names the undeclared key %q: %s", fqcn, key, rendered)
		}
	}
}
