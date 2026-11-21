// Package facttest: the audit a package's TestMain runs after its tests, over
// what Observe recorded.
package facttest

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Options is what a package tells Main about its own methods.
type Options struct {
	// Exempt names each fact-declaring built-in of the package that no test
	// here can make emit, with why and which real-device gate makes it emit
	// instead. An entry that no longer names such a method, or names one a
	// test here made emit, fails the audit, so the table cannot outlive its
	// reason. A method another package's tests drive and audit (through Of)
	// belongs in DrivenBy instead, which names that package.
	Exempt map[string]string
	// DrivenBy names each fact-declaring built-in of the package that no test
	// here makes emit because another package's tests do, against a real
	// device, and audit it there: the value is that package's import path,
	// whose TestMain passes this package's import path as Of
	// (internal/archtest holds both ends). An entry is held as an Exempt one
	// is: it must name such a method, and no test here may make it emit.
	DrivenBy map[string]string
	// Unavailable, when not empty, says why this machine cannot run the tests
	// that make the package's methods emit (no /bin/sh for an in-process SSH
	// server): the audit then reports which methods went undriven rather than
	// failing on them. Every fact a test did emit is still checked. It is one
	// of the two helpers internal/archtest allows here,
	// need.POSIXShellMissing() and testsupport.DockerMissing(), so the reason
	// is the same one that skips those tests.
	Unavailable string
	// Of is the import path of the package whose methods these tests make
	// emit, for a test package that drives another's against a real device
	// (pkg/snmp/snmplive drives internal/catalog/net/snmp's). Empty means the
	// package whose TestMain calls Main. A package that holds methods of its
	// own may not name another (internal/archtest).
	Of string
}

// Main runs m's tests, audits what they emitted against the declarations of
// the fact-declaring built-ins of the package whose TestMain calls it (or of
// Options.Of), writes the audit to standard error, and returns the exit code:
// m's, or 1 when m passed and the audit failed. A TestMain passes the code to
// os.Exit. An audited package that holds no fact-declaring built-in fails the
// audit: a wrong Of, a renamed package or a misread caller would otherwise
// audit nothing and pass.
func Main(m *testing.M, opts Options) int {
	pkg := opts.Of
	if pkg == "" {
		pkg = callerPackage()
	}
	code := m.Run()
	failures, notes := Audit(pkg, opts, partialRun())
	report(os.Stderr, pkg, failures, notes)
	if code == 0 && len(failures) > 0 {
		return 1
	}
	return code
}

// callerPackage is the package under test whose TestMain called Main: the
// caller's package with an external test package's _test suffix removed.
func callerPackage() string {
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return ""
	}
	f := runtime.FuncForPC(pc)
	if f == nil {
		return ""
	}
	return strings.TrimSuffix(PackageOf(f.Name()), "_test")
}

// partialRun reports why this run chose only some of the package's tests
// (-run, -skip, -list or -fuzz, or -short, which the tests that start a
// device skip), "" when it ran them all. A partial run cannot say a method no
// test drove, since the test that drives it may be the one left out.
func partialRun() string {
	for _, name := range []string{"test.run", "test.skip", "test.list", "test.fuzz"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return "-" + strings.TrimPrefix(name, "test.") + " chose only some tests"
		}
	}
	if testing.Short() {
		return "-short skips the tests that start a device"
	}
	return ""
}

// Audit compares what Observe recorded with the declarations of pkg's
// fact-declaring built-ins (those whose Invoke or Check pkg declares). It
// returns the failures (a fact refused by its method's declaration, a method
// no test made emit, an exemption that no longer holds, and a pkg holding no
// fact-declaring built-in at all) and the notes (a declared fact no test
// emitted, facts emitted outside any method, and why the coverage was not
// enforced). partial, when not empty, is why this run cannot judge coverage.
func Audit(pkg string, opts Options, partial string) (failures, notes []string) {
	ledger.Lock()
	defer ledger.Unlock()
	for _, r := range ledger.problems {
		failures = append(failures, r.String())
	}
	methods := DeclaringMethods(pkg)
	if len(methods) == 0 {
		failures = append(failures, fmt.Sprintf("the audit found no fact-declaring built-in whose Invoke or Check %q declares, so it held nothing: "+
			"Options.Of names the wrong package, the package was renamed, or its methods stopped declaring facts (then drop facttest.Main here)", pkg))
	}
	// A function serving several methods, one of them a fact-declaring
	// method of pkg, is refused whether or not a test ran it. Observe refuses
	// each fact such a function emits, naming the test, and the same message
	// is not repeated here.
	index := functionIndex()
	for _, function := range sortedKeys(index) {
		names := index[function]
		if len(names) < 2 || PackageOf(function) != pkg || !slices.ContainsFunc(names, func(n string) bool { return hasKey(methods, n) }) {
			continue
		}
		if msg := sharedFunction(function, names); !hasKey(ledger.seen, msg) {
			failures = append(failures, msg)
		}
	}
	for _, table := range []struct {
		field   string
		entries map[string]string
	}{{"Exempt", opts.Exempt}, {"DrivenBy", opts.DrivenBy}} {
		for _, name := range sortedKeys(table.entries) {
			switch _, ok := methods[name]; {
			case !ok:
				failures = append(failures, fmt.Sprintf("Options.%s names %s, which is not a fact-declaring built-in of %s: remove the entry", table.field, name, pkg))
			case len(ledger.emitted[name]) > 0:
				failures = append(failures, fmt.Sprintf("Options.%s names %s, and a test here made it emit: remove the entry", table.field, name))
			}
		}
	}
	for _, name := range sortedKeys(opts.DrivenBy) {
		if other := opts.DrivenBy[name]; other == "" || other == pkg {
			failures = append(failures, fmt.Sprintf("Options.DrivenBy names %s, driven by %q: name the other package whose TestMain passes Of: %q", name, other, pkg))
		}
		if hasKey(opts.Exempt, name) {
			failures = append(failures, fmt.Sprintf("Options.Exempt and Options.DrivenBy both name %s: keep one", name))
		}
	}
	why := partial
	if why == "" {
		why = opts.Unavailable
	}
	for _, name := range sortedKeys(methods) {
		emitted := ledger.emitted[name]
		if _, exempt := opts.Exempt[name]; exempt {
			continue
		}
		if _, elsewhere := opts.DrivenBy[name]; elsewhere {
			continue
		}
		if len(emitted) == 0 {
			if why == "" {
				failures = append(failures, fmt.Sprintf("%s declares facts, and no test of %s made it emit one through facttest.Observe", name, pkg))
			} else {
				notes = append(notes, fmt.Sprintf("%s was not driven, and this run cannot say whether a test would: %s", name, why))
			}
			continue
		}
		for _, spec := range methods[name] {
			if !anyMatches(spec, emitted) {
				notes = append(notes, fmt.Sprintf("%s declares %s, which no test here emitted", name, specName(spec)))
			}
		}
	}
	driven := []string{}
	for _, name := range sortedKeys(ledger.emitted) {
		driven = append(driven, fmt.Sprintf("%s (%d keys)", name, len(ledger.emitted[name])))
	}
	if len(driven) == 0 {
		driven = append(driven, "none")
	}
	notes = append(notes, fmt.Sprintf("held the facts these methods emitted to their declarations: %s", strings.Join(driven, ", ")))
	if ledger.unattributed > 0 {
		notes = append(notes, fmt.Sprintf("%d facts were emitted with no registered built-in's Invoke on the stack (a helper called directly, or a goroutine of the method's own), for example %s",
			ledger.unattributed, strings.Join(ledger.unattributedKeys, ", ")))
	}
	return failures, notes
}

// DeclaringMethods is each registered built-in that declares facts and whose
// Invoke or Check pkg declares, with its declarations.
func DeclaringMethods(pkg string) map[string][]collection.FactSpec {
	out := map[string][]collection.FactSpec{}
	for _, d := range Builtins() {
		if len(d.Manifest.Facts) == 0 {
			continue
		}
		for _, fn := range []collection.Method{d.Invoke, d.Check} {
			if name := FunctionName(fn); name != "" && PackageOf(name) == pkg {
				out[d.Name] = d.Manifest.Facts
			}
		}
	}
	return out
}

// hasKey reports whether m holds key.
func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}

// anyMatches reports whether one of keys is the fact spec declares.
func anyMatches(spec collection.FactSpec, keys map[string]bool) bool {
	for key := range keys {
		if spec.Matches(key) {
			return true
		}
	}
	return false
}

// specName is a declaration as the report shows it: a family with a trailing *.
func specName(s collection.FactSpec) string {
	if s.Prefix {
		return fmt.Sprintf("%q*", s.Key)
	}
	return fmt.Sprintf("%q", s.Key)
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// report writes the audit: every failure, then every note.
func report(w io.Writer, pkg string, failures, notes []string) {
	for _, f := range failures {
		fmt.Fprintf(w, "facttest: FAIL: %s: %s\n", pkg, f)
	}
	for _, n := range notes {
		fmt.Fprintf(w, "facttest: note: %s: %s\n", pkg, n)
	}
}
