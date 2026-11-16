// Package snmp implements the net.snmp.* methods: net.snmp.facts (a device's
// system group, interfaces and engine), net.snmp.get (OIDs read or walked)
// and net.snmp.set (one object written, its change read back and its undo
// recorded exactly). Each reaches its device's agent through pkg/snmp's
// ClientFor, the client onboarding and wait.snmp use, with the SNMP family of
// the device's own credential, which each method declares it reads
// (collection.Manifest.UsesSNMPCredential) so the Walk tier's Controller
// carries it to the dispatch.
package snmp

import (
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// noBecomeReason is every net.snmp method's reason become does not apply.
const noBecomeReason = "it speaks SNMP over UDP to the device's agent, which no sudo prefix reaches"

// session is one task's client and what it masks.
type session struct {
	client *snmp.Client
	// masked are the credential's values every recorded answer is masked of.
	masked []string
	// warnings are the device's settings' own (snmp.Settings.Warnings).
	warnings []string
}

// open builds fqcn's client for device from rc's credential and records the
// warnings its settings give, before anything is sent, so a task that fails
// still says what the device's record let it do.
func open(rc sdk.RunbookContext, device inventory.InventoryItem, fqcn string) (*session, error) {
	if device == nil {
		return nil, errors.New(fqcn + ": the task has no target device")
	}
	secrets := rc.InjectSecrets()
	client, warnings, err := snmp.ClientFor(device, device.Name(), secrets)
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	return &session{client: client, masked: wire.MaskedValues(secrets), warnings: warnings}, nil
}

// warn records the settings' warnings followed by more, since the stat holds
// one list.
func (s *session) warn(rc sdk.RunbookContext, more ...string) error {
	if len(more) == 0 {
		return nil
	}
	return sdk.RecordWarnings(rc, append(append([]string(nil), s.warnings...), more...))
}

// close releases the client.
func (s *session) close() { _ = s.client.Close() }

// binding is v as a stat or fact records it: its OID, type and value text,
// masked, and hex when the text is.
func (s *session) binding(v snmp.Varbind) map[string]any {
	m := snmp.Mask(v, s.masked)
	out := map[string]any{"oid": m.OID, "type": string(m.Type), "value": m.Text}
	if m.Hex {
		out["hex"] = true
	}
	return out
}
