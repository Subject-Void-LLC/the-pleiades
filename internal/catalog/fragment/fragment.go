// Package fragment holds the shared parameter sets ("fragments") a
// built-in Collection method's Doc.Fragments names: the analogue of
// Ansible's extends_documentation_fragment, where a repeated parameter is
// written once and every method sharing it points here.
//
// It is a leaf package, importing only pkg/collection, so everything that
// needs to know a method's full parameter list can reach it: the
// documentation generator and `pleiades doc`, which render it, and
// internal/validate, which refuses a task parameter no method declares.
// It used to live in internal/forge/catalogdata, which pulls in the
// generation tooling and the whole data layer with it, so the one place
// that decides whether a runbook is valid could not see these names.
package fragment

import (
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Builtin names every reusable collection.Fragment a built-in method's
// Doc.Fragments entry can reference.
var Builtin = map[string]collection.Fragment{
	// catalyst_connection covers the TLS-verification param every
	// net.catalyst.* method reads through the shared clientForDevice
	// helper (internal/catalog/net/catalyst/client.go). The username
	// and password themselves are never a param: they come from the
	// device's own stored credential via sdk.RunbookContext.InjectSecrets,
	// which is why this fragment names insecure_skip_verify only.
	"catalyst_connection": {
		Params: []collection.Param{
			{Name: "insecure_skip_verify", Type: "bool", Default: "false", Description: "Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller."},
		},
	},

	// catalyst_pagination covers the one tuning knob the two
	// EachDevice-based methods (device_facts, reachability) read.
	"catalyst_pagination": {
		Params: []collection.Param{
			{Name: "page_size", Type: "int", Default: "500", Description: "How many devices to request per page from the Catalyst Center API. A tuning knob, not a correctness one: an invalid or non-positive value silently falls back to the default."},
		},
	},
}

// Params returns every parameter the named fragments add, in the order
// named, and, sorted, every name Builtin does not know. A caller deciding
// what a method accepts treats an unknown name as adding nothing, since
// nothing documents what it would add.
func Params(names []string) (params []collection.Param, unknown []string) {
	for _, name := range names {
		frag, ok := Builtin[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		params = append(params, frag.Params...)
	}
	slices.Sort(unknown)
	return params, unknown
}

// Declared returns the set of parameter names d's own documentation
// declares: its Doc.Params plus every param its named fragments add.
func Declared(d collection.Descriptor) map[string]bool {
	declared := make(map[string]bool, len(d.Manifest.Doc.Params))
	for _, p := range d.Manifest.Doc.Params {
		declared[p.Name] = true
	}
	shared, _ := Params(d.Manifest.Doc.Fragments)
	for _, p := range shared {
		declared[p.Name] = true
	}
	return declared
}
