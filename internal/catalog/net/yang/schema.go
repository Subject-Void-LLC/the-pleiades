// Package yang implements "net.yang.schema": the YANG source of a device's
// modules, fetched from the device itself and saved into the project, so an
// author or an agent reads the schema the device really serves rather than
// guessing a path (CVD run 11, 2026-11-15, read sources through http.request
// on a third device record, and the configuration masker turned 481 of their
// words into placeholders).
//
// # One method, two protocols, chosen at plan time
//
// Following the owner's 2026-11-14 decision for the YANG methods, it is one
// method rather than one per protocol: NETCONF (RFC 6022's get-schema) when
// the device serves it, else RESTCONF (the URL its YANG library names, RFC
// 8040 section 3.7), or the one a task names with via. The two are the
// manifest's alternatives, chosen by collection.ChooseAlternative over the
// manifest For shapes from the call, so what validate, admission and the run
// choose cannot disagree, and a failed fetch never falls back to the other
// protocol. gNMI defines no way to fetch a module's source (its Capabilities
// name models only), so a gNMI-only device fits neither and is refused.
//
// # Masked by value only
//
// YANG source is schema, not configuration: a leaf named password, a list's
// key statement and the word secret are its grammar. It is never run through
// pkg/netcfg's configuration masker, and nothing of it reaches the run's
// report (whose boundary masks by shape too): the method's stats name modules,
// files, sizes and digests only. What a source is masked by is the run's
// known secret values (the device's credential and the project's named
// secrets), each replaced wherever it stands, as a value. A value shorter
// than netcfg.MinLearnedLength cannot be masked without cutting the schema's
// words apart, so a source holding one as a word of its own is refused,
// naming the module and never the value, and nothing is written.
//
// # Written into the project
//
// Each source is written through sdk.ProjectFileWriter under dest, as
// name@revision.yang built from the library's identifier and date, never from
// device text, for its owner alone (0600); only the command line holds the
// project, so a Walk-tier Runner refuses the task at validation
// (internal/validate's project file rule, which counts a dest left to its
// default). Every source is fetched, checked, masked and held to the bounds,
// and every file to the project writer's rules in a dry run, before any file
// is written, so a refusal writes nothing; a write failing after that leaves
// the files before it, which the error says and the stats name.
package yang

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fqcn is the method's name.
const fqcn = "net.yang.schema"

// The alternatives, by name: the protocols a source is fetched over.
const (
	viaNETCONF  = "netconf"
	viaRESTCONF = "restconf"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name:     fqcn,
		Manifest: manifest(),
		Invoke:   Schema,
		Check:    Check,
	})
}

// alternatives are the protocols, in the order a device serving both is read
// over (NETCONF first, as the owner decided for the YANG methods).
func alternatives() []collection.Alternative {
	return []collection.Alternative{
		{Name: viaNETCONF, SupportedTransports: []string{capability.TransportNetconf}, RequiredCapabilities: []capability.Name{capability.NameNetconf, capability.NameSSHTransport}},
		{Name: viaRESTCONF, SupportedTransports: []string{capability.TransportRestconf}, RequiredCapabilities: []capability.Name{capability.NameRestconf}},
	}
}

// manifest is net.yang.schema's manifest.
func manifest() collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{capability.TransportNetconf, capability.TransportRestconf},
		RequiredCapabilities: []capability.Name{},
		Alternatives:         alternatives(),
		AlternativeParam:     paramVia,
		ExecutionContext: collection.ExecutionContext{
			RequiresElevation: false,
			Site:              collection.SiteTarget,
			Device:            collection.DeviceRequired,
		},
		EngineVersion:  ">=0.4.0",
		Status:         collection.StatusImplemented,
		NoBecomeReason: "it speaks NETCONF over an SSH subsystem or RESTCONF over HTTPS, which no sudo prefix reaches, so it would run as the login",
		Reversibility: collection.Reversibility{
			Reversible: false,
			Notes: "It changes nothing on the device. It writes YANG source files into the project, whose earlier content is never journaled: " +
				"the project is the author's working copy, kept under version control, which is where an earlier version comes back from.",
		},
		// Its check reads the library and fetches every source (both reads),
		// and predicts which files a run would write, writing none.
		SupportsCheck: true,
		Doc:           Doc(),
	}
}

// Schema implements "net.yang.schema".
func Schema(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeExecute)
}

// Check is "net.yang.schema"'s check: every read a run makes, no write.
func Check(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return run(ctx, rc, device, params, collection.ModeCheck)
}
