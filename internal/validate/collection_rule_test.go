package validate_test

import (
	"fmt"
	"strings"
	"testing"

	// Blank-imported purely to trigger the generated catalog's own init()
	// registration into pkg/collection, exactly as cmd/pleiades's own
	// catalog_builtins.go does for the real binary: without it, every
	// pkg/collection.Lookup below would report "not registered" instead
	// of exercising the real declared-but-unimplemented path this rule
	// exists to catch, RULE 0's "same config the platform runs" standard.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestCollectionRule_UnregisteredName is half of Phase 34's Release Gate:
// pleiades validate rejects a runbook calling a dotted name pkg/collection
// has never heard of, with a plain-language message.
func TestCollectionRule_UnregisteredName(t *testing.T) {
	world := validate.WorldView{DAG: dagWithOneTask("totally.fake.name", "")}

	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Fatal("expected an unregistered collection name to be flagged")
	}
	msg := report.String()
	if !strings.Contains(msg, "totally.fake.name") {
		t.Errorf("expected the message to name the fqcn, got: %s", msg)
	}
	if !strings.Contains(msg, "not a registered collection name") {
		t.Errorf("expected a plain-language 'not registered' message, got: %s", msg)
	}
}

// TestCollectionRule_DeclaredButUnimplemented is the other half of Phase
// 34's Release Gate: pleiades validate rejects a runbook calling a real
// catalog name whose manifest Status is still StatusDeclared, which is
// every catalog name today (Phase 34 generates stubs, not
// implementations).
func TestCollectionRule_DeclaredButUnimplemented(t *testing.T) {
	world := validate.WorldView{DAG: dagWithOneTask("pkg.apt.install", "")}

	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Fatal("expected a declared-but-unimplemented collection name to be flagged")
	}
	msg := report.String()
	if !strings.Contains(msg, "pkg.apt.install") {
		t.Errorf("expected the message to name the fqcn, got: %s", msg)
	}
	if !strings.Contains(msg, "declared but not yet implemented") {
		t.Errorf("expected a plain-language 'declared but not implemented' message, got: %s", msg)
	}
}

// TestCollectionRule_SkipsLegacyAndEngineKeywords proves the undotted-fqcn
// discriminator never flags a legacy built-in or an engine keyword,
// calling CollectionRule directly (not the full Validate pipeline) so a
// finding from a different rule can never be mistaken for one of this
// rule's own.
func TestCollectionRule_SkipsLegacyAndEngineKeywords(t *testing.T) {
	for _, fqcn := range []string{"noop", "set_metadata", "ssh_exec", "ios_backup", "set_fact", "debug", "import_tasks"} {
		t.Run(fqcn, func(t *testing.T) {
			world := validate.WorldView{DAG: dagWithOneTask(fqcn, "")}
			findings := validate.CollectionRule(world)
			if len(findings) != 0 {
				t.Errorf("expected CollectionRule to skip %q entirely, got: %v", fqcn, findings)
			}
		})
	}
}

// TestCollectionRule_StressAllCatalogNames is Phase 34's own named
// Fuzz/Stress checklist item: "stress the validation rule against a
// runbook calling every name in the catalog." It builds one task per real
// catalogdata.Collections entry (the same single source of truth
// tools/gencatalog drove the real CLI from), so this test can never drift
// out of sync with the actual generated catalog.
//
// It asserts exactly one declared-but-unimplemented Finding per declared
// entry, and none at all for an implemented one. The count used to be "one
// per entry, no more and no fewer", which was right while every entry was a
// stub and became wrong the moment the net.catalyst.* methods were verified
// against a real controller. Deriving the expectation from each entry's own
// registered status keeps the assertion honest as more methods land: a
// method that stops being flagged has to have earned it by carrying a real
// implementation, and a stub that stops being flagged still fails.
func TestCollectionRule_StressAllCatalogNames(t *testing.T) {
	nodes := make(map[string]*engine.Task, len(catalogdata.Collections))
	var wantFindings int
	implemented := make(map[string]bool)

	for i, cfg := range catalogdata.Collections {
		nodes[fmt.Sprintf("tasks[%d]", i)] = &engine.Task{FQCN: cfg.Name}

		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Fatalf("catalog entry %q is not registered; internal/catalog/builtins.go may be stale", cfg.Name)
		}
		if desc.Manifest.Status == collection.StatusImplemented {
			implemented[cfg.Name] = true
			continue
		}
		wantFindings++
	}

	world := validate.WorldView{DAG: &engine.DAG{Nodes: nodes}}
	findings := validate.CollectionRule(world)

	if len(findings) != wantFindings {
		t.Fatalf("expected one finding per declared catalog entry (%d of %d entries, %d implemented), got %d",
			wantFindings, len(catalogdata.Collections), len(implemented), len(findings))
	}
	for _, f := range findings {
		if !strings.Contains(f.Message, "declared but not yet implemented") {
			t.Errorf("expected every declared catalog entry to be flagged as declared-but-unimplemented, got: %s", f.Message)
		}
		for name := range implemented {
			if strings.Contains(f.Message, name) {
				t.Errorf("implemented method %q was flagged as unimplemented: %s", name, f.Message)
			}
		}
	}
}

// TestCollectionRule_NamedTaskInMessage mirrors
// TestCapabilityRule_NamedTaskInMessage: a task's human-given Name, when
// set, is folded into the Finding's Message alongside its synthesized ID.
func TestCollectionRule_NamedTaskInMessage(t *testing.T) {
	world := validate.WorldView{DAG: dagWithOneTask("pkg.apt.install", "")}
	world.DAG.Nodes["tasks[0]"].Name = "install apt packages"

	findings := validate.CollectionRule(world)
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Message, "install apt packages") {
		t.Errorf("expected the message to include the task's Name, got: %s", findings[0].Message)
	}
	if !strings.Contains(findings[0].Message, "tasks[0]") {
		t.Errorf("expected the message to still include the task's synthesized ID, got: %s", findings[0].Message)
	}
}

// TestCollectionRule_PleiadesBuiltinSetMetadataExempted proves the dotted
// "pleiades.builtin.set_metadata" spelling of the set_metadata builtin is
// exempted from CollectionRule exactly like its bare "set_metadata"
// spelling, despite containing a dot: it is a hardcoded engine builtin
// (internal/engine/action.go), never a real pkg/collection registration,
// so treating it as an unregistered collection name would be wrong.
func TestCollectionRule_PleiadesBuiltinSetMetadataExempted(t *testing.T) {
	world := validate.WorldView{DAG: dagWithOneTask("pleiades.builtin.set_metadata", "")}

	findings := validate.CollectionRule(world)
	if len(findings) != 0 {
		t.Fatalf("expected pleiades.builtin.set_metadata to be exempted, got findings: %v", findings)
	}
}
