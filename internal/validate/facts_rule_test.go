// Tests of FactsRule, FactCheckWarnings and the template rule's refusal of a
// parameter reading facts, through the real builder and the real catalog.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// factsItems is one active Linux server, web1.
func factsItems() []inventory.InventoryItem {
	return []inventory.InventoryItem{&inventorytest.Stub{StubID: "dev-1", StubName: "web1", StubState: inventory.StateActive}}
}

// gatherThenReadFacts gathers web1's facts, then decides on its family.
const gatherThenReadFacts = `id: facts
hosts: web1
tasks:
  - name: gather
    facts.gather: {}
  - name: on debian
    when: facts["os.family"] == "debian"
    noop: {}
`

// readOnlyFacts decides on a fact no task of the run gathers.
const readOnlyFacts = `id: facts
hosts: web1
tasks:
  - name: on debian
    when: facts["os.family"] == "debian"
    noop: {}
`

// factFindings is what FactsRule says of src against a store.
func factFindings(t *testing.T, src string, store validate.FactStore) []validate.Finding {
	t.Helper()
	return validate.FactsRule(validate.WorldView{Items: factsItems(), DAG: buildYAML(t, src), StoredFacts: store})
}

// TestFactsRule_ATierWithNoStoreRefusesEveryRead: a Runner (and any caller
// that does not say) refuses a runbook reading facts before any task runs,
// naming the task and the key and saying nothing ran and what works; a
// runbook reading none is not refused.
func TestFactsRule_ATierWithNoStoreRefusesEveryRead(t *testing.T) {
	for _, src := range []string{gatherThenReadFacts, readOnlyFacts} {
		findings := factFindings(t, src, validate.FactStoreNone)
		if len(findings) != 1 {
			t.Fatalf("findings %v, want one", findings)
		}
		msg := findings[0].Message
		for _, want := range []string{`on debian`, `facts["os.family"]`, "Runner cannot read stored facts in this release", "nothing ran", "registered result"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal %q does not say %q", msg, want)
			}
		}
	}
	if findings := factFindings(t, "id: none\nhosts: web1\ntasks:\n  - name: t\n    noop: {}\n", validate.FactStoreNone); len(findings) != 0 {
		t.Errorf("a runbook reading no fact: %v", findings)
	}
}

// TestFactsRule_AProjectWithNoKeyAllowsOnlyWhatTheRunGathersFirst: with no
// master key nothing stored can be read, so a read nothing before it gathers
// is refused naming the key to set; a read after a gather declaring the key
// is allowed, and one before the gather that would answer it is not.
func TestFactsRule_AProjectWithNoKeyAllowsOnlyWhatTheRunGathersFirst(t *testing.T) {
	refused := factFindings(t, readOnlyFacts, validate.FactStoreNoKey)
	if len(refused) != 1 || !strings.Contains(refused[0].Message, "set PLEIADES_MASTER_KEY or restore .pleiades/master.key") || !strings.Contains(refused[0].Message, "gather the fact in an earlier task") {
		t.Fatalf("a read with no key and no gather: %v", refused)
	}
	if findings := factFindings(t, gatherThenReadFacts, validate.FactStoreNoKey); len(findings) != 0 {
		t.Errorf("a gather then a read with no key: %v, want it allowed", findings)
	}
	before := "id: facts\nhosts: web1\ntasks:\n  - name: on debian\n    when: facts[\"os.family\"] == \"debian\"\n    noop: {}\n  - name: gather\n    facts.gather: {}\n"
	if findings := factFindings(t, before, validate.FactStoreNoKey); len(findings) != 1 {
		t.Errorf("a read before the gather: %v, want it refused", findings)
	}
	if findings := factFindings(t, readOnlyFacts, validate.FactStoreReadable); len(findings) != 0 {
		t.Errorf("a read in a project with its key: %v", findings)
	}
}

// TestFactsRule_ATaskWithNoDeviceOrTakingEveryLockIsRefused: a task with no
// device has no facts, and a task reading its device's facts is decided per
// device, so it cannot take every device's lock together.
func TestFactsRule_ATaskWithNoDeviceOrTakingEveryLockIsRefused(t *testing.T) {
	noDevice := "id: facts\ntasks:\n  - name: on debian\n    when: facts[\"os.family\"] == \"debian\"\n    noop: {}\n"
	if f := factFindings(t, noDevice, validate.FactStoreReadable); len(f) != 1 || !strings.Contains(f[0].Message, "it has no device") {
		t.Errorf("a task with no device: %v", f)
	}
	lockAll := strings.Replace(readOnlyFacts, "    noop: {}", "    lock_acquisition: all_at_plan_time\n    noop: {}", 1)
	if f := factFindings(t, lockAll, validate.FactStoreReadable); len(f) != 1 || !strings.Contains(f[0].Message, "all_at_plan_time") {
		t.Errorf("a task taking every lock: %v", f)
	}
}

// TestTemplateRule_NoParameterReadsAFact: a method's parameter, a set_fact's
// value and a rollback step's parameter reading facts are refused, saying a
// fact is read only in a condition, so no fact can reach the run's variables
// or a recorded undo; a template that does not compile is refused, never
// taken to read nothing.
func TestTemplateRule_NoParameterReadsAFact(t *testing.T) {
	for name, task := range map[string]string{
		"a parameter":               "  - name: t\n    file.copy:\n      dest: /tmp/x\n      content: \"{{ facts['os.family'] }}\"\n",
		"a set_fact":                "  - name: t\n    set_fact:\n      family: \"{{ facts['os.family'] }}\"\n",
		"a statement":               "  - name: t\n    file.copy:\n      dest: /tmp/x\n      content: \"{% if facts['os.family'] %}x{% endif %}\"\n",
		"a rollback step":           "  - name: t\n    file.copy:\n      dest: /tmp/x\n      content: x\n    rollback:\n      - file.copy:\n          dest: /tmp/x\n          content: \"{{ facts['os.family'] }}\"\n",
		"a loop":                    "  - name: t\n    loop: \"{{ facts['os.family'] }}\"\n    noop: {}\n",
		"one that does not compile": "  - name: t\n    file.copy:\n      dest: /tmp/x\n      content: \"{{ facts['os.family' }}\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			findings := validate.TemplateRule(validate.WorldView{Items: factsItems(), DAG: buildYAML(t, "id: t\nhosts: web1\ntasks:\n"+task)})
			if len(findings) == 0 {
				t.Fatal("no finding")
			}
			if name != "one that does not compile" && !strings.Contains(findings[0].Message, engine.ErrParamReadsFacts) {
				t.Errorf("finding %q, want the facts refusal", findings[0].Message)
			}
		})
	}
}

// TestFactCheckWarnings_NamesEveryReaderACheckCannotCover: a check of a
// runbook installing a package and then reading its fact warns that the
// reader will be unchecked, naming the install; a read-only gather's reader
// is not warned of, and a real run warns of nothing.
func TestFactCheckWarnings_NamesEveryReaderACheckCannotCover(t *testing.T) {
	dag := buildYAML(t, `id: facts
hosts: web1
tasks:
  - name: gather
    facts.gather: {}
  - name: install
    pkg.apt.install: {name: nginx}
  - name: configure
    when: facts["package.nginx"] is defined
    noop: {}
  - name: on debian
    when: facts["os.family"] == "debian"
    noop: {}
`)
	check := validate.FactCheckWarnings(validate.WorldView{Items: factsItems(), DAG: dag, Mode: collection.ModeCheck})
	if len(check) != 1 || check[0].Node != "tasks[2]" || !strings.Contains(check[0].Message, "pkg.apt.install") || !strings.Contains(check[0].Message, "unchecked in a check") {
		t.Fatalf("check warnings %v, want one naming the configure task and the install", check)
	}
	if real := validate.FactCheckWarnings(validate.WorldView{Items: factsItems(), DAG: dag}); len(real) != 0 {
		t.Errorf("a real run's warnings %v, want none", real)
	}
}
