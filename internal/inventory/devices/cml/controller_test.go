// Tests of what a cml_controller record may say about where the controller
// is, how it is trusted and where its console server listens.
package cml_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cml"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// pin is a well-formed certificate fingerprint.
var pin = "sha256:" + strings.Repeat("ab", 32)

// build makes a cml_controller from props.
func build(props map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return cml.NewController(record.Record{Name: "cml", Type: cml.TypeController, Properties: props})
}

// TestNewController_ReadsTheControllerAndItsConsole: an https base (a
// trailing slash dropped) with a pinned certificate is a CML API device,
// addressable at its host, whose console server listens on 22 there
// unless the record names another port, and whose pin is warned about.
func TestNewController_ReadsTheControllerAndItsConsole(t *testing.T) {
	item, err := build(map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://10.10.20.161/", devicetls.FingerprintProperty: pin})
	if err != nil {
		t.Fatal(err)
	}
	ctl := item.(capability.CMLAPICapable)
	host, port := ctl.CMLConsoleAddress()
	if !item.HasCapability(capability.NameCMLAPI) || !item.HasCapability(capability.NameNetworkAddressable) ||
		ctl.CMLBaseURL() != "https://10.10.20.161" || ctl.CMLPlaintextWarning() != "" || host != "10.10.20.161" || port != 22 {
		t.Errorf("controller: base %q, warning %q, console %s:%d", ctl.CMLBaseURL(), ctl.CMLPlaintextWarning(), host, port)
	}
	if got := devicetls.For(item).PinnedFingerprint(); got != pin {
		t.Errorf("the pin reads as %q", got)
	}
	if w := devicetls.For(item).Warnings("cml"); len(w) != 1 {
		t.Errorf("warnings %q", w)
	}
	if !item.(inventory.Shared).SharedAcrossRuns() || item.(interface{ IPAddress() string }).IPAddress() != "10.10.20.161" {
		t.Error("a controller is shared across runs and addressed at its host")
	}
	for _, port := range []inventory.PropertyValue{2222, 2222.0} {
		item, err := build(map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab:8443", cml.ConsolePortProperty: port})
		if err != nil {
			t.Fatal(err)
		}
		if _, p := item.(capability.CMLAPICapable).CMLConsoleAddress(); p != 2222 {
			t.Errorf("console port %v reads as %d", port, p)
		}
	}
}

// TestNewController_PlainHTTPOnlyByOptIn: plain http:// sends the login
// in the clear, so it needs httpapi's per-device flag and is warned about;
// the flag on an https base allows nothing and is refused.
func TestNewController_PlainHTTPOnlyByOptIn(t *testing.T) {
	if _, err := build(map[string]inventory.PropertyValue{cml.APIBaseProperty: "http://10.0.0.9"}); err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Errorf("plain http without the flag: %v", err)
	}
	item, err := build(map[string]inventory.PropertyValue{cml.APIBaseProperty: "http://10.0.0.9", httpapi.AllowPlaintextCredentialsProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := item.(capability.CMLAPICapable).CMLPlaintextWarning(); !strings.Contains(w, "plain HTTP") || !strings.Contains(w, "10.0.0.9") {
		t.Errorf("warning %q", w)
	}
	if _, err := build(map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://10.0.0.9", httpapi.AllowPlaintextCredentialsProperty: true}); err == nil {
		t.Error("the plaintext flag on an https base was accepted")
	}
}

// TestNewController_Refusals: each malformed record is refused, naming the
// property at fault.
func TestNewController_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		props map[string]inventory.PropertyValue
		want  string
	}{
		"no base":           {map[string]inventory.PropertyValue{}, cml.APIBaseProperty},
		"a path":            {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab/api/v0"}, "without /api/v0"},
		"user information":  {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://admin:pw@cml.lab"}, "user information"},
		"ftp":               {map[string]inventory.PropertyValue{cml.APIBaseProperty: "ftp://cml.lab"}, "http or https"},
		"flag as text":      {map[string]inventory.PropertyValue{cml.APIBaseProperty: "http://cml.lab", httpapi.AllowPlaintextCredentialsProperty: "yes"}, "true or false"},
		"TLS on plain http": {map[string]inventory.PropertyValue{cml.APIBaseProperty: "http://cml.lab", httpapi.AllowPlaintextCredentialsProperty: true, devicetls.FingerprintProperty: pin}, "has none"},
		"a bad pin":         {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab", devicetls.FingerprintProperty: "sha256:12"}, devicetls.FingerprintProperty},
		"port zero":         {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab", cml.ConsolePortProperty: 0}, cml.ConsolePortProperty},
		"port too high":     {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab", cml.ConsolePortProperty: 70000}, cml.ConsolePortProperty},
		"port as text":      {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab", cml.ConsolePortProperty: "22"}, cml.ConsolePortProperty},
		"a fractional port": {map[string]inventory.PropertyValue{cml.APIBaseProperty: "https://cml.lab", cml.ConsolePortProperty: 22.5}, cml.ConsolePortProperty},
	} {
		if _, err := build(tc.props); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}
