// Architecture rule for absence (Phase 131, R4), the guard half: every
// implemented, read-only built-in method either declares an exists return
// (which TestALookupAnswersAbsenceAsData then holds to the whole
// convention) or says in its manifest's NoExistsReason why it answers none.
// The audit of 2026-10-28 judged the 56 read-only methods of that day; this
// is what keeps the 57th from arriving unjudged.
package archtest

import (
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestEveryReadOnlyMethodDeclaresExistsOrAReason holds every implemented,
// read-only built-in method to declaring an exists return or carrying a
// NoExistsReason, never both, and every method that carries one to being
// an implemented read-only method.
func TestEveryReadOnlyMethodDeclaresExistsOrAReason(t *testing.T) {
	var methods []collection.Descriptor
	for _, cfg := range catalogdata.Collections {
		if desc, ok := collection.Lookup(cfg.Name); ok {
			methods = append(methods, desc)
		}
	}
	readOnly, declare, reasons := 0, 0, 0
	for _, desc := range methods {
		if desc.Manifest.Status != collection.StatusImplemented || !desc.Manifest.Reversibility.ReadOnly {
			continue
		}
		readOnly++
		if desc.Manifest.Doc.DeclaresExists() {
			declare++
		}
		if desc.Manifest.NoExistsReason != "" {
			reasons++
		}
	}
	if readOnly == 0 || declare == 0 || reasons == 0 {
		t.Fatalf("%d read-only methods, %d declaring exists, %d with a reason: this test proved nothing", readOnly, declare, reasons)
	}
	for _, problem := range existsOrReasonProblems(methods) {
		t.Error(problem + " (Phase 131, R4)")
	}
	t.Logf("%d implemented read-only methods: %d declare exists, %d give a reason", readOnly, declare, reasons)
}

// existsOrReasonProblems returns, in name order, each implemented
// read-only method of methods that declares no exists return and gives no
// NoExistsReason, each that does both, and each other method that gives
// one.
func existsOrReasonProblems(methods []collection.Descriptor) []string {
	var problems []string
	for _, desc := range methods {
		m := desc.Manifest
		declares, reason := m.Doc.DeclaresExists(), m.NoExistsReason != ""
		switch {
		case m.Status != collection.StatusImplemented || !m.Reversibility.ReadOnly:
			if reason {
				problems = append(problems, desc.Name+" gives a NoExistsReason but is not an implemented read-only method")
			}
		case declares && reason:
			problems = append(problems, desc.Name+" declares exists and also gives a NoExistsReason; drop the reason")
		case !declares && !reason:
			problems = append(problems, desc.Name+" is read-only and declares no exists return: declare exists (bool, returned always) if it can be asked about something absent, else set Manifest.NoExistsReason to why it answers none")
		}
	}
	sort.Strings(problems)
	return problems
}

// TestExistsOrReasonRule_Control proves the rule above sees what it looks
// for: a read-only method with neither an exists return nor a reason is
// named, as are one with both and a reason on a method that is not
// read-only or not implemented, while a method with exactly one of the two,
// and a method that is not read-only with neither, are not.
func TestExistsOrReasonRule_Control(t *testing.T) {
	method := func(name string, readOnly, exists bool, reason string) collection.Descriptor {
		d := collection.Descriptor{Name: name, Manifest: collection.Manifest{Status: collection.StatusImplemented, NoExistsReason: reason}}
		d.Manifest.Reversibility.ReadOnly = readOnly
		if exists {
			d.Manifest.Doc.Returns = []collection.ReturnField{{Name: collection.ExistsReturn, Type: "bool", Returned: "always"}}
		}
		return d
	}
	whole := []collection.Descriptor{
		method("x.thing.info", true, true, ""),
		method("x.thing.list", true, false, collection.NoExistsLists),
		method("x.thing.set", false, false, ""),
	}
	if got := existsOrReasonProblems(whole); len(got) != 0 {
		t.Errorf("a whole catalog was named: %v", got)
	}
	unbuilt := method("x.later.list", true, false, collection.NoExistsLists)
	unbuilt.Manifest.Status = collection.StatusDeclared
	for name, d := range map[string]collection.Descriptor{
		"neither":           method("x.new.info", true, false, ""),
		"both":              method("x.thing.info", true, true, collection.NoExistsLists),
		"not read-only":     method("x.thing.set", false, false, collection.NoExistsLists),
		"declared, unbuilt": unbuilt,
	} {
		if got := existsOrReasonProblems([]collection.Descriptor{d}); len(got) != 1 {
			t.Errorf("%s: problems %v, want exactly one", name, got)
		}
	}
}
