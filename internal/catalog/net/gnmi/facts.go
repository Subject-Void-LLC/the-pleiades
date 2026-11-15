// Package gnmi implements the net.gnmi methods: "net.gnmi.facts", what a
// device's gNMI server says it supports, and "net.gnmi.config", gNMI's write.
// Both reach the device through pkg/gnmi.ClientFor, as onboarding does, so
// they cannot disagree about how a device is reached.
//
// net.gnmi.facts asks the server's Capabilities and records its gNMI
// version, encodings and models in the gnmi.* family, beside the
// protocol-neutral yang.* facts every YANG discovery records (owner decision
// 2026-11-14): yang.modules from the models, never complete, since gNMI's
// Capabilities is not a YANG library. It changes nothing, so a device read on
// a schedule shows a change in what it offers as drift.
package gnmi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

// The facts net.gnmi.facts records beside the yang.* family.
const (
	factVersion      = "gnmi.version"
	factEncodings    = "gnmi.encodings"
	factModels       = "gnmi.models"
	factModelCount   = "gnmi.model_count"
	factModelsDigest = "gnmi.models_digest"
)

// factKeys are every fact a read may record, in the order they are recorded.
var factKeys = append([]string{factVersion, factEncodings, factModels, factModelCount, factModelsDigest}, yangfacts.GNMIKeys...)

// noBecome is why neither method takes become.
const noBecome = "it speaks gNMI over gRPC to the device's own server, which no sudo prefix reaches"

// factsDescription is the method's description, word for word its catalog
// entry's.
const factsDescription = "Asks the device's gNMI server its Capabilities (gNMI specification section 3.2) and records what it says, changing nothing: the gNMI version, the encodings it answers in and the models it implements, in the gnmi.* family, beside the protocol-neutral yang.* facts net.netconf.facts and net.restconf.facts record from a YANG library. gNMI's Capabilities is not a YANG library (no namespace, and a model's own version in place of a revision when it is no date), so yang.completeness is always partial and no yang.modules_digest is recorded; gnmi.models_digest is the model list's own. The device is reached at its own target with its own TLS (always verified, a pinned certificate included, mutual TLS from the stored certificate) and its own stored credential, as the gNMI specification's username and password metadata, a bearer token, or none; a refused login is sent once and never again. A device reached over plaintext gRPC (grpc_plaintext) records its findings as stats only, never as facts, since nothing proves which server answered, and every run says so. Reports no change, and a check runs it for real."

func init() {
	doc := factsDoc()
	collection.MustRegister(collection.Descriptor{
		Name: "net.gnmi.facts",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportGNMI},
			RequiredCapabilities: []capability.Name{capability.NameGNMI},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion:  ">=0.4.0",
			Status:         collection.StatusImplemented,
			NoBecomeReason: noBecome,
			SupportsCheck:  true,
			NoExistsReason: collection.NoExistsDevice,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, factKeys),
		},
		Invoke: Facts,
		Check:  Facts,
	})
}

// factsDoc is net.gnmi.facts' documentation.
func factsDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Records what a device's gNMI server says about itself: its gNMI version, encodings and models.",
		Description: factsDescription,
		Returns:     factsReturns(),
		Examples: []collection.Example{
			{Name: "Read what a device offers over gNMI", RunbookYAML: "- name: Discover the device's gNMI models\n  net.gnmi.facts:\n  register: discovered\n"},
		},
		SeeAlso: []string{"net.gnmi.config", "net.netconf.facts", "net.restconf.facts"},
	}
}

// factsReturns documents the facts, one return each.
func factsReturns() []collection.ReturnField {
	const when = "on every read, as a stat; as a fact only when the device was reached over TLS"
	return append([]collection.ReturnField{
		{Name: factVersion, Type: "string", Returned: when, Description: "The gNMI version the server says it implements, such as 0.7.0; empty when it names none."},
		{Name: factEncodings, Type: "list of string", Returned: when, Description: "The encodings the server says it answers in, by gNMI's names lowercased (json, bytes, proto, ascii, json_ietf), in gNMI's own order. A server that answers PROTO alone (Stratum, measured 2026-11-15) refuses a Get in JSON."},
		{Name: factModels, Type: "list of string", Returned: when + ", when the list fits in 60 KiB", Description: "Every model the server lists, each name@version (name alone for a model with no version), as the server wrote them, sorted; an entry that is not printable or is too long is left out, with a warning."},
		{Name: factModelCount, Type: "int", Returned: when, Description: "How many models the server lists, as gnmi.models counts them."},
		{Name: factModelsDigest, Type: "string", Returned: when, Description: "The SHA-256 (hex) of the model list, each model's name, organization and version, sorted, so any change to what the server says it implements reads as a change of this one value. It is not yang.modules_digest, which only a YANG library gives."},
	}, yangfacts.GNMIReturns(when)...)
}

// Facts implements the "net.gnmi.facts" collection method: one Capabilities
// read, recorded as stats always and as facts only over TLS.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	const fqcn = "net.gnmi.facts"
	if device == nil {
		return collection.Result{}, errors.New(fqcn + ": the task has no target device")
	}
	client, warnings, err := gnmi.ClientFor(device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	defer func() { _ = client.Close() }()
	// Recorded before the read, so a read that fails still says what the
	// device's record let it do.
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	found, err := recordCapabilities(rc, client.Verified(), caps)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if len(found) > 0 {
		// The stat holds one list, so the record's own warnings are kept
		// beside what the read found.
		if err := sdk.RecordWarnings(rc, append(append([]string(nil), warnings...), found...)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// recordCapabilities records what caps says as stats, and as facts only when
// verified (the device was reached over TLS), returning the warnings the task
// should carry. net.gnmi.facts and net.gnmi.config both record through it.
func recordCapabilities(rc sdk.RunbookContext, verified bool, caps gnmi.Capabilities) ([]string, error) {
	values, warnings, err := capabilityValues(caps)
	if err != nil {
		return nil, err
	}
	if err := yangfacts.Record(rc, factKeys, values, verified); err != nil {
		return nil, err
	}
	if !verified {
		warnings = append(warnings, "the device was reached over gRPC without TLS, where nothing proves which server answered, so nothing this read found is recorded as a fact")
	}
	return warnings, nil
}

// capabilityValues are the facts caps gives and the warnings for what was
// left out of them.
func capabilityValues(caps gnmi.Capabilities) (map[string]any, []string, error) {
	modules, unread := caps.YANGModules()
	values, warnings, err := yangfacts.GNMIValues(modules, unread)
	if err != nil {
		return nil, nil, err
	}
	encodings := make([]string, 0, len(caps.Encodings))
	for _, e := range caps.Encodings {
		encodings = append(encodings, e.String())
	}
	models := make([]string, 0, len(caps.Models))
	for _, m := range caps.Models {
		if m.Version == "" {
			models = append(models, m.Name)
		} else {
			models = append(models, m.Name+"@"+m.Version)
		}
	}
	values[factVersion] = caps.Version
	values[factEncodings] = encodings
	values[factModelCount] = len(caps.Models)
	values[factModelsDigest] = caps.ModelsDigest()
	if yangfacts.Fits(models) {
		values[factModels] = models
	} else {
		warnings = append(warnings, fmt.Sprintf("the device lists %d gNMI models, more than %s holds in 60 KiB; recorded by count and digest only", len(models), factModels))
	}
	if caps.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized entries in the device's gNMI Capabilities were left out", caps.Dropped))
	}
	return values, warnings, nil
}
