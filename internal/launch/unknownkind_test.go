package launch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file is the behavioural half of Phase 21's adversarial gate. The
// source scan in internal/archtest catches a consumer that compares against
// a kind name; this catches one that dispatches on kind by any other
// mechanism, by driving the whole resolver with a kind that exists nowhere
// in this repository.
//
// It registers that kind at run time rather than adding a package for it,
// which is the point: if a kind arriving from outside the built-in set
// works end to end, then nothing in the path knows the built-in set exists.

// terraformKind is a kind this codebase has never heard of, with a field
// set that overlaps the built-ins in one place and diverges everywhere
// else. PLAN.md Section 28 names Terraform as a future kind; nothing here
// implements one, and that is exactly what makes it a good stand-in for
// whatever the seventh kind turns out to be.
func terraformKind(t *testing.T) launch.Descriptor {
	t.Helper()

	d := launch.Descriptor{
		Kind:       "terraform-" + strings.ToLower(t.Name()),
		Label:      "Terraform",
		BadgeClass: "badge-neutral",
		Summary:    "A kind registered by this test, which no package in this repository declares.",
		Adapter:    "opentofu",
		Fields: []launch.FieldSpec{
			// One field the built-ins also have, so the shared machinery is
			// exercised.
			{Name: "extra_vars", Type: launch.TypeMap, Label: "VARIABLES"},
			// Three they do not, so nothing can be passing by coincidence.
			{Name: "workspace", Type: launch.TypeString, Label: "WORKSPACE"},
			{Name: "parallelism", Type: launch.TypeInt, Min: 1, Max: 100, Label: "PARALLELISM"},
			{Name: "targets", Type: launch.TypeStringList, Label: "TARGETS"},
		},
		ValidateDefinition: func(reference string) error {
			if !strings.HasSuffix(reference, ".tf") {
				return fmt.Errorf("a terraform template names a .tf file")
			}
			return nil
		},
	}

	if err := launch.Register(d); err != nil {
		t.Fatalf("registering a new kind: %v", err)
	}
	return d
}

func TestAnUnknownKindResolvesWithoutAnythingKnowingItExists(t *testing.T) {
	d := terraformKind(t)

	tmpl := launch.Template{
		Name:           "provision the edge",
		KindName:       d.Kind,
		Definition:     "infra/edge.tf",
		InventoryID:    4,
		OrganizationID: 2,
		Defaults: launch.Fields{
			"workspace":   "staging",
			"parallelism": 10,
			"targets":     []string{"module.edge"},
			"extra_vars":  map[string]any{"region": "eu-west-1"},
		},
		Prompts: []string{"workspace", "extra_vars"},
	}

	if err := tmpl.Validate(); err != nil {
		t.Fatalf("a template of an unknown kind does not validate: %v", err)
	}

	resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{
			"workspace":   "production",
			"extra_vars":  map[string]any{"version": "1.7"},
			"parallelism": 100,
		},
	})
	if err != nil {
		t.Fatalf("Resolve of an unknown kind: %v", err)
	}

	// Its own adapter travels with the dispatch, so a runner routes on a
	// value it was given rather than on a set it was compiled with.
	if resolved.Adapter != "opentofu" {
		t.Errorf("resolved adapter = %q, want the kind's own %q", resolved.Adapter, "opentofu")
	}

	// Its own fields resolved: the open one applied, the locked one did not.
	if got := resolved.Fields.String("workspace"); got != "production" {
		t.Errorf("workspace = %q, want the override", got)
	}
	if got := resolved.Fields.Int("parallelism"); got != 10 {
		t.Errorf("parallelism = %d, want the template's 10: a locked field of an unknown kind took an override", got)
	}
	if len(ignored) != 1 || ignored[0].Name != "parallelism" {
		t.Fatalf("Resolve reported %+v, want parallelism alone", ignored)
	}

	// And the field it shares with the built-ins behaves the same way, so
	// the shared machinery is not special-casing kinds it recognises.
	if resolved.ExtraVars["region"] != "eu-west-1" || resolved.ExtraVars["version"] != "1.7" {
		t.Errorf("extra variables did not merge for an unknown kind: %+v", resolved.ExtraVars)
	}
}

func TestAnUnknownKindEnforcesItsOwnDefinitionRule(t *testing.T) {
	d := terraformKind(t)

	tmpl := launch.Template{
		Name: "wrong file", KindName: d.Kind, Definition: "infra/edge.yml", InventoryID: 4,
	}

	// The kind's own ValidateDefinition ran, not a built-in's. A resolver
	// that recognised only the kinds it shipped with would have accepted a
	// .yml here, or refused a .tf everywhere.
	if err := tmpl.Validate(); err == nil {
		t.Error("a template naming the wrong file type for its kind validated")
	} else if !strings.Contains(err.Error(), ".tf") {
		t.Errorf("Validate reported %v, want the kind's own rule about .tf files", err)
	}
}

func TestRegister_RefusesADescriptorNobodyCouldUse(t *testing.T) {
	cases := map[string]launch.Descriptor{
		"no kind": {Adapter: "native"},
		// A kind with no adapter is one the runner accepts a dispatch for
		// and then has nothing to run, which surfaces as a job that is
		// neither refused nor completed.
		"no adapter": {Kind: "adapterless-" + t.Name()},
		"duplicate field": {Kind: "dupe-" + t.Name(), Adapter: "native", Fields: []launch.FieldSpec{
			{Name: "limit", Type: launch.TypeString},
			{Name: "limit", Type: launch.TypeInt},
		}},
		"unknown field type": {Kind: "badtype-" + t.Name(), Adapter: "native", Fields: []launch.FieldSpec{
			{Name: "limit", Type: launch.FieldType("colour")},
		}},
		"impossible range": {Kind: "badrange-" + t.Name(), Adapter: "native", Fields: []launch.FieldSpec{
			{Name: "forks", Type: launch.TypeInt, Min: 10, Max: 5},
		}},
	}

	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if err := launch.Register(d); err == nil {
				t.Errorf("Register accepted a descriptor with %s", name)
			}
		})
	}
}

func TestKinds_ListsInAStableOrder(t *testing.T) {
	first := launch.Kinds()
	second := launch.Kinds()

	if len(first) < 2 {
		t.Fatalf("only %d kinds are registered, want at least the two built-ins", len(first))
	}
	for i := range first {
		if first[i].Kind != second[i].Kind {
			// The registry is a map, so an unsorted listing would put a
			// form's options in a different order on every page load.
			t.Fatalf("two listings disagree at position %d: %q then %q", i, first[i].Kind, second[i].Kind)
		}
	}
}
