package catalogdata

import "github.com/SubjectVoidLLC/the-pleiades/pkg/collection"

// Fragments names every reusable collection.Fragment a Doc.Fragments
// entry can reference, the analogue of Ansible's
// extends_documentation_fragment: a repeated parameter is documented once
// here, wherever real methods share it.
var Fragments = map[string]collection.Fragment{
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
