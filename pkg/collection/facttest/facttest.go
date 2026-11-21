// Package facttest holds a built-in Collection method's emitted facts to its
// own registered declaration (collection.Manifest.Facts), in that method's
// tests (Phase 41b).
//
// A test's fake sdk.RunbookContext hands every fact it is given to Observe.
// Observe finds the method that emitted it from the call stack (the
// outermost registered built-in's Invoke or Check on it, which is the method
// a task would have named) and checks the key and the value's JSON type
// against that method's declaration, the same lookup the engine makes before
// it stores a fact. The package's TestMain runs its tests through Main, which
// then fails the test binary for every fact a method emitted that it does not
// declare, every value of another type, and every fact-declaring built-in of
// the package that no test made emit, and writes a note for each declared
// fact no test emitted. Failures and notes alike go to the test binary's
// standard error, which go test shows with -v, or for a package that fails.
//
// The engine already refuses an undeclared fact at run time; this moves the
// finding to the method's own tests, where a mistake costs a commit rather
// than a fact lost in a run's log.
//
// It is test support, and it imports testing, so internal/archtest keeps
// every production build from importing it. It lives under pkg/ rather than
// beside internal/testsupport because a Collection may import only pkg/, and
// the first-party Collections are planned to move into programs of their own,
// whose tests must still reach it.
package facttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ledger is what every Observe call in this test binary saw.
var ledger = struct {
	sync.Mutex
	// emitted is each method's emitted keys.
	emitted map[string]map[string]bool
	// problems is every distinct refusal of an emitted fact by its method's
	// declaration, in the order first seen; seen indexes it by message.
	problems []refusal
	seen     map[string]int
	// unattributed counts facts no registered built-in on the stack emitted
	// (a test calling a helper directly, or a method emitting from a
	// goroutine of its own), with a few of their keys for the report.
	unattributed     int
	unattributedKeys []string
}{emitted: map[string]map[string]bool{}, seen: map[string]int{}}

// refusal is one fact a method's declaration refused: the message, the first
// test that emitted it, and how many times it was emitted.
type refusal struct {
	message string
	test    string
	count   int
}

// String is the refusal as the report shows it.
func (r refusal) String() string {
	if r.count > 1 {
		return fmt.Sprintf("%s (in %s, and %d times in all)", r.message, r.test, r.count)
	}
	return fmt.Sprintf("%s (in %s)", r.message, r.test)
}

// maxReportedKeys bounds how many unattributed keys the report names.
const maxReportedKeys = 5

// Observe checks one fact a fake RunbookContext was handed against the
// declaration of the method that emitted it, and records it for Main. A fake
// calls it from its EmitFact and goes on recording the fact as it did.
func Observe(key string, value any) {
	methods, test := attribute()
	ledger.Lock()
	defer ledger.Unlock()
	if len(methods) == 0 {
		ledger.unattributed++
		if len(ledger.unattributedKeys) < maxReportedKeys {
			ledger.unattributedKeys = append(ledger.unattributedKeys, key)
		}
		return
	}
	for _, name := range methods {
		if p := problem(name, key, value); p != "" {
			if i, ok := ledger.seen[p]; ok {
				ledger.problems[i].count++
			} else {
				ledger.seen[p] = len(ledger.problems)
				ledger.problems = append(ledger.problems, refusal{message: p, test: test, count: 1})
			}
		}
		if ledger.emitted[name] == nil {
			ledger.emitted[name] = map[string]bool{}
		}
		ledger.emitted[name][key] = true
	}
}

// problem is why method's declaration refuses the fact key with value, or ""
// when it declares it with value's type.
func problem(method, key string, value any) string {
	d, _ := collection.Lookup(method)
	spec, ok := collection.FactFor(d.Manifest.Facts, key)
	if !ok {
		return fmt.Sprintf("%s emitted fact %q, which it does not declare (Manifest.Facts)", method, key)
	}
	if why := TypeProblem(spec.Type, value); why != "" {
		return fmt.Sprintf("%s emitted fact %q, and %s", method, key, why)
	}
	return ""
}

// TypeProblem is why value, as the engine would encode it, is not of the
// JSON type want, or "" when it is. It is internal/facts' rule (a whole
// number is a number too, and null is no type's value), held equal to it by
// internal/archtest's tests, since a package under pkg/ cannot import it.
func TypeProblem(want collection.FactType, value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("its value does not encode as JSON: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return fmt.Sprintf("its value does not decode as JSON: %v", err)
	}
	got := jsonType(decoded)
	if got == want || (want == collection.FactNumber && got == collection.FactInteger) {
		return ""
	}
	name := "null"
	switch got {
	case "":
	case collection.FactInteger, collection.FactObject:
		name = "an " + string(got)
	default:
		name = "a " + string(got)
	}
	return fmt.Sprintf("its value is %s, and the method declares it %s", name, want)
}

// jsonType names a decoded JSON value's type, "" for null.
func jsonType(v any) collection.FactType {
	switch x := v.(type) {
	case string:
		return collection.FactString
	case bool:
		return collection.FactBoolean
	case json.Number:
		// A number written with a fraction or an exponent is a number; any
		// other, however large, is an integer.
		if strings.ContainsAny(x.String(), ".eE") {
			return collection.FactNumber
		}
		return collection.FactInteger
	case []any:
		return collection.FactList
	case map[string]any:
		return collection.FactObject
	}
	return ""
}

// attribute returns the methods whose Invoke or Check is the outermost
// registered built-in function on the calling goroutine's stack (several when
// one function serves several methods), and the test that called it, for the
// report.
func attribute() ([]string, string) {
	index := functionIndex()
	pcs := make([]uintptr, 512)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var methods []string
	test := "an unnamed test"
	for {
		frame, more := frames.Next()
		// Callers lists the innermost frame first, so the last match is the
		// outermost: the method a task named, not one it dispatched to.
		if names, ok := index[frame.Function]; ok {
			methods = names
		}
		if name, ok := testName(frame.Function); ok {
			test = name
		}
		if !more {
			break
		}
	}
	return methods, test
}

// testName is the top-level test a function name belongs to
// ("pkg_test.TestX.func1" is TestX's), and whether it belongs to one.
func testName(function string) (string, bool) {
	_, rest := splitFunction(function)
	first, _, _ := strings.Cut(rest, ".")
	if strings.HasPrefix(first, "Test") {
		return first, true
	}
	return "", false
}

// functionIndex maps the name of each registered built-in's Invoke and Check
// to the methods it implements. It is built on each call, since a test may
// register a method after another test observed a fact.
func functionIndex() map[string][]string {
	index := map[string][]string{}
	for _, d := range Builtins() {
		for _, fn := range []collection.Method{d.Invoke, d.Check} {
			if name := FunctionName(fn); name != "" && !containsString(index[name], d.Name) {
				index[name] = append(index[name], d.Name)
			}
		}
	}
	return index
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Builtins returns every registered built-in method (one with no Provider),
// sorted by name: the methods of every first-party group this binary
// registered.
func Builtins() []collection.Descriptor {
	var out []collection.Descriptor
	for _, g := range collection.FirstPartyGroups() {
		for name := range collection.GroupManifests(g.Name) {
			if d, ok := collection.Lookup(name); ok && d.Provider == nil {
				out = append(out, d)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FunctionName is the full name of fn's code ("example.com/x/apt.Install"),
// "" for a nil function.
func FunctionName(fn collection.Method) string {
	if fn == nil {
		return ""
	}
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return ""
	}
	return f.Name()
}

// PackageOf is the import path of the package a function name was declared
// in ("example.com/x/apt" for "example.com/x/apt.Install.func1").
func PackageOf(function string) string {
	pkg, _ := splitFunction(function)
	return pkg
}

// splitFunction splits a function's full name into its package's import path
// and the rest, reading a generic function's type arguments (which may hold
// slashes and dots) as part of the rest.
func splitFunction(function string) (string, string) {
	head := function
	if i := strings.IndexByte(head, '['); i >= 0 {
		head = head[:i]
	}
	slash := strings.LastIndexByte(head, '/')
	dot := strings.IndexByte(head[slash+1:], '.')
	if dot < 0 {
		return function, ""
	}
	cut := slash + 1 + dot
	return function[:cut], function[cut+1:]
}
