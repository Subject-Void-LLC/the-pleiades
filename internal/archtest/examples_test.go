// Every built-in method's documented examples pass the check `pleiades
// validate` makes: an example a reader copies should run as written.
package archtest

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	devai "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestEveryDocumentedExampleValidates builds each registered method's
// documented examples, the ones `pleiades doc` prints, into a runbook
// through the engine's real Builder, which compiles every condition, and
// runs internal/validate's rules over it, as `pleiades validate` does.
//
// Nothing did until 2026-10-18, and nineteen examples had broken:
// conditions reading a register by its bare name (a condition reads it
// under result), a register's changed (a register holds the method's
// stats), a hosts: key on a task, a template root that does not exist.
// cmd/pleiades' TestExamples_EveryRunbookBuildsAndValidates runs the real
// binary over the runbooks under examples/ only, and
// TestCatalogDataDocsMatchTheRegistry only holds the two copies of each
// Doc equal, so a broken example copied into both passed it
// (FAILURE_PATTERNS 515). This runs in process, over the registry, where
// pr-check always runs it; an example has no inventory of its own, so
// the rules that need a device find none to check.
func TestEveryDocumentedExampleValidates(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	// The providers the ai examples are written against, as real records,
	// so the residency rule reads what a reader's inventory would hold.
	providers := []inventory.InventoryItem{
		aitest.Provider(t, "workstation", map[string]any{devai.KindProperty: "ollama", devai.APIBaseProperty: "http://10.0.0.5:11434", devai.ResidencyProperty: devai.ResidencyLocal}),
		aitest.Provider(t, "lab-vllm", map[string]any{devai.KindProperty: "openai", devai.APIBaseProperty: "http://10.0.0.6:8000", devai.ResidencyProperty: devai.ResidencyLocal}),
		aitest.Provider(t, "claude", map[string]any{devai.KindProperty: "anthropic", devai.APIBaseProperty: "https://api.anthropic.com", devai.ResidencyProperty: devai.ResidencyHosted}),
	}
	var checked int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		for _, ex := range desc.Manifest.Doc.Examples {
			checked++
			dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(exampleRunbook(ex.RunbookYAML)))
			if err != nil {
				t.Errorf("%s, example %q: %v", cfg.Name, ex.Name, err)
				continue
			}
			for _, f := range validate.Validate(validate.WorldView{Items: providers, DAG: dag}).Findings {
				t.Errorf("%s, example %q: %s: %s: %s", cfg.Name, ex.Name, f.RuleName, f.Node, f.Message)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no documented example was examined, so this test proved nothing")
	}
	t.Logf("%d documented example(s) validated", checked)
}

// exampleRunbook wraps an example that is a task list, as every one is
// today, in the runbook a reader pastes it into.
func exampleRunbook(example string) string {
	if strings.HasPrefix(example, "tasks:") || strings.Contains(example, "\ntasks:") {
		return example
	}
	return "id: example\nhosts: example\ntasks:\n  " + strings.ReplaceAll(strings.TrimRight(example, "\n"), "\n", "\n  ") + "\n"
}
