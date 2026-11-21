// Package auditee_test: an external test package whose TestMain runs its
// tests through facttest.Main, each test doing what AUDITEE_CASE asks, so
// pkg/collection/facttest's main_test.go can read the exit code and report a
// whole test binary gives for each case.
package auditee_test

import (
	"context"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest/testdata/auditee"
)

// TestMain calls facttest.Main inside os.Exit, as most packages do, or, when
// AUDITEE_MAIN is "expression", as an expression whose value it keeps before
// exiting, as internal/catalog/net/netconf's does. AUDITEE_OF, when set, is
// passed as Options.Of.
func TestMain(m *testing.M) {
	opts := facttest.Options{Of: os.Getenv("AUDITEE_OF")}
	if os.Getenv("AUDITEE_MAIN") == "expression" {
		code := facttest.Main(m, opts)
		os.Exit(code)
	}
	os.Exit(facttest.Main(m, opts))
}

// recorder is a fake run context whose EmitFact reports to facttest.
type recorder struct{ facts map[string]any }

// InjectSecrets implements sdk.RunbookContext.
func (r *recorder) InjectSecrets() map[string]string { return nil }

// SetStat implements sdk.RunbookContext.
func (r *recorder) SetStat(string, any) error { return nil }

// EmitFact implements sdk.RunbookContext.
func (r *recorder) EmitFact(key string, value any) error {
	facttest.Observe(key, value)
	r.facts[key] = value
	return nil
}

// invoke runs the registered method name, emitting facts.
func invoke(t *testing.T, name string, facts map[string]any) {
	t.Helper()
	d, ok := collection.Lookup(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	if _, err := d.Invoke(context.Background(), &recorder{facts: map[string]any{}}, nil, map[string]any{"emit": facts}); err != nil {
		t.Fatal(err)
	}
}

// TestAlpha makes Alpha emit what it declares, or, by AUDITEE_CASE, a fact it
// does not declare ("refused") or a count that is not an integer
// ("wrongtype").
func TestAlpha(t *testing.T) {
	facts := map[string]any{"auditee.name": "web1", "auditee.count": 3}
	switch os.Getenv("AUDITEE_CASE") {
	case "refused":
		facts["auditee.unknown"] = "x"
	case "wrongtype":
		facts["auditee.count"] = "three"
	}
	invoke(t, auditee.Alpha, facts)
}

// TestBeta makes Beta emit, unless AUDITEE_CASE is "undriven". Under -short
// it skips, as a test that starts a device does.
func TestBeta(t *testing.T) {
	if testing.Short() {
		t.Skip("stands in for a test that starts a device")
	}
	if os.Getenv("AUDITEE_CASE") == "undriven" {
		return
	}
	invoke(t, auditee.Beta, map[string]any{"auditee.flag": true})
}

// TestFails fails when AUDITEE_CASE is "testfails", with an audit that
// passes.
func TestFails(t *testing.T) {
	if os.Getenv("AUDITEE_CASE") == "testfails" {
		t.Error("this test fails on purpose")
	}
}
