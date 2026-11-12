// Tests for OperandProblem, the rule a value is held to when its parameter
// is a name placed on a device's command line, and for Register's refusal of
// an operand declared on a parameter that cannot hold a name.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestOperandProblem holds a value to the operand rule in every shape a
// task writes or a template renders: text, a list item by item, and a value
// of another kind, which is the type rule's. A parameter that is not an
// operand takes anything.
func TestOperandProblem(t *testing.T) {
	name := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatOperand}
	deps := collection.Param{Name: "dependencies", Type: "list", Format: collection.ParamFormatOperand}
	open := collection.Param{Name: "command", Type: "string"}
	unit := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatSystemdUnit}
	apt := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatAptPackage}
	aptDeps := collection.Param{Name: "dependencies", Type: "list", Format: collection.ParamFormatAptPackage}
	rpm := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatRPMPackage}
	pkg := collection.Param{Name: "name", Type: "string", Format: collection.ParamFormatPackage}
	version := collection.Param{Name: "version", Type: "string", Format: collection.ParamFormatPackageVersion}
	mount := collection.Param{Name: "src", Type: "string", Format: collection.ParamFormatMountField}
	for label, tc := range map[string]struct {
		p    collection.Param
		v    any
		want string // a substring; "" means none
	}{
		"a name":                   {name, "curl", ""},
		"an option":                {name, "--autoremove", `begins with "-"`},
		"a pattern":                {name, "s*", "pattern"},
		"null, an absent value":    {name, nil, ""},
		"a number is the type's":   {name, 7, ""},
		"a list of names":          {deps, []any{"libjq1", "libonig5"}, ""},
		"a Go list of names":       {deps, []string{"libjq1"}, ""},
		"a list with an option":    {deps, []any{"libjq1", "-oX"}, `item 1 is refused: it begins with "-"`},
		"a list with a non-string": {deps, []any{3, "jq"}, ""},
		"not an operand":           {open, "--anything *", ""},
		// Each program's own grammar, through the format its parameter
		// declares.
		"a systemd root unit":           {unit, "-.mount", ""},
		"the root slice":                {unit, "-.slice", ""},
		"another dash-led unit":         {unit, "-f", `begins with "-"`},
		"a unit pattern":                {unit, "s*", "pattern"},
		"the root unit as a plain name": {name, "-.mount", `begins with "-"`},
		"an APT trailing dash":          {apt, "diffutils-", `ends in "-"`},
		"an APT release":                {apt, "jq/bookworm", `holds "/"`},
		"an APT dependency's dash":      {aptDeps, []any{"libjq1", "jq-"}, `item 1 is refused: it ends in "-"`},
		"an RPM group":                  {rpm, "@core", `begins with "@"`},
		"a generic multiarch name":      {pkg, "libc6:amd64", ""},
		"a generic RPM group":           {pkg, "@core", `begins with "@"`},
		"a version pattern":             {version, "1.*", "pattern"},
		"a version ending in a dash":    {version, "1:3.8-4-", `ends in "-"`},
		"a real version":                {version, "1.6-2.1+deb12u3", ""},
		"a mount source with a line":    {mount, "/dev/sdb1\n/dev/sdc1", "line break"},
		"a bind source with brackets":   {mount, "/dev/sda1[/srv]", ""},
	} {
		t.Run(label, func(t *testing.T) {
			got := collection.OperandProblem(tc.p, tc.v)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("OperandProblem(%s, %#v) = %q, want none", tc.p.Name, tc.v, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("OperandProblem(%s, %#v) = %q, want it to say %q", tc.p.Name, tc.v, got, tc.want)
			}
		})
	}
}

// TestOperandFormats_MapEveryKindOnce: each operand format names one
// pkg/operand kind, every kind is some format's, and a format outside the
// list is not an operand, so a kind added on one side and not the other
// fails here rather than leaving a parameter unchecked.
func TestOperandFormats_MapEveryKindOnce(t *testing.T) {
	seen := map[operand.Kind]collection.ParamFormat{}
	for _, format := range collection.OperandFormats() {
		kind, ok := collection.OperandKind(format)
		if !ok {
			t.Fatalf("%s is listed as an operand format and maps to no kind", format)
		}
		if other, dup := seen[kind]; dup {
			t.Fatalf("%s and %s both map to %s", format, other, kind)
		}
		seen[kind] = format
	}
	for _, kind := range operand.Kinds() {
		if _, ok := seen[kind]; !ok {
			t.Errorf("operand kind %s is no format's, so no parameter can declare it", kind)
		}
	}
	for _, format := range []collection.ParamFormat{"", collection.ParamFormatCommand, collection.ParamFormatURL, "unknown"} {
		if _, ok := collection.OperandKind(format); ok {
			t.Errorf("%q reads as an operand format", format)
		}
	}
}

// TestRegister_HoldsOperandsToText: an operand is text or a list of text,
// and Register refuses one declared on any other type, naming the
// parameter.
func TestRegister_HoldsOperandsToText(t *testing.T) {
	restore := collection.SnapshotForTest()
	t.Cleanup(restore)
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	register := func(name string, p collection.Param) error {
		return collection.Register(collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{Params: []collection.Param{p}}}})
	}
	for _, typ := range []string{"bool", "int", "dict", "list of dict"} {
		err := register("operandtest.bad_"+strings.ReplaceAll(typ, " ", "_"), collection.Param{Name: "name", Type: typ, Format: collection.ParamFormatOperand})
		if err == nil || !strings.Contains(err.Error(), `parameter "name" of type `+typ+" an operand") {
			t.Errorf("%s: err = %v, want a refusal naming the parameter", typ, err)
		}
	}
	for _, typ := range []string{"string", "list", "list of string"} {
		if err := register("operandtest.ok_"+strings.ReplaceAll(typ, " ", "_"), collection.Param{Name: "name", Type: typ, Format: collection.ParamFormatOperand}); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	// Every operand format is held to the same rule, not only the first.
	for _, format := range collection.OperandFormats() {
		err := register("operandtest.bool_"+string(format), collection.Param{Name: "name", Type: "bool", Format: format})
		if err == nil {
			t.Errorf("%s on a bool parameter was registered", format)
		}
	}
}
