// The gNMI probe: the server's own Capabilities RPC.
package onboard

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

type gnmiProber struct{}

func init() { Register(generic.TypeGNMI, gnmiProber{}) }

func (gnmiProber) Protocol() string { return "gnmi" }

// Probe asks the device's gNMI server its Capabilities, over the device's
// own TLS (pkg/devicetls: always verified) with its own credential, through
// pkg/gnmi.ClientFor exactly as net.gnmi.facts does, and records a summary of
// what it said: the gNMI version, the encodings, and the models' count and
// digest. The model list itself is the fact store's (net.gnmi.facts'
// gnmi.models), not the inventory record's. An answer is what grants
// GNMICapable: only a gNMI server answers that RPC. A refused credential, an
// untrusted certificate or no answer proves nothing.
//
// The summary never says the model list is complete: gNMI's Capabilities
// is not a YANG library, and nothing in the protocol makes it every module
// the device implements.
func (gnmiProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	client, warnings, err := gnmi.ClientFor(device, device.Name(), secrets)
	if err != nil {
		return Probed{}, err
	}
	defer func() { _ = client.Close() }()
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return Probed{}, err
	}
	probed := gnmiProbed(caps)
	probed.Warnings = append(warnings, probed.Warnings...)
	if !client.Verified() {
		probed.Warnings = append(probed.Warnings, "the device is reached over gRPC without TLS, so nothing proves which server answered this probe")
	}
	return probed, nil
}

// gnmiProbed builds the result from a Capabilities answer.
func gnmiProbed(caps gnmi.Capabilities) Probed {
	encodings := make([]string, 0, len(caps.Encodings))
	for _, e := range caps.Encodings {
		encodings = append(encodings, e.String())
	}
	facts := map[string]any{
		"gnmi_version":   factText(caps.Version),
		"gnmi_encodings": factList(encodings),
		// Text, as every other probe's count is, so a re-onboard of an
		// unchanged device compares equal (FAILURE_PATTERNS 1250).
		"gnmi_model_count":   strconv.Itoa(len(caps.Models)),
		"gnmi_models_digest": caps.ModelsDigest(),
	}
	var warnings []string
	if caps.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized entries in the device's gNMI Capabilities were left out", caps.Dropped))
	}
	return Probed{Capabilities: []capability.Name{capability.NameGNMI}, Facts: facts, Warnings: warnings}
}
