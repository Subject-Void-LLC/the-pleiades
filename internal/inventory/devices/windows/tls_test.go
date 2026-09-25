// Tests for the TLS settings a windows_server's WinRM connection applies.
package windows_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/windows"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// testCA is a certificate in PEM, used only as a pinned authority.
const testCA = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`

// newServer builds a windows_server from props.
func newServer(props map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return windows.NewServer(record.Record{ID: "w1", Name: "w1", Type: "windows_server", Properties: props})
}

func TestServer_TLSSettings(t *testing.T) {
	item, err := newServer(map[string]inventory.PropertyValue{
		"port": 5986, devicetls.CAPEMProperty: testCA, devicetls.ServerNameProperty: "vengeance",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	settings := devicetls.For(item)
	if !settings.PinnedCA() || len(settings.CAPEM()) == 0 || settings.ServerName != "vengeance" {
		t.Errorf("settings = pinned %v, %d PEM bytes, name %q", settings.PinnedCA(), len(settings.CAPEM()), settings.ServerName)
	}
	plain, err := newServer(nil)
	if err != nil {
		t.Fatalf("NewServer with no TLS properties: %v", err)
	}
	if devicetls.For(plain).PinnedCA() {
		t.Error("a server naming no authority pinned one")
	}
}

func TestServer_RefusesTLSPropertiesWinRMDoesNotApply(t *testing.T) {
	for _, key := range []string{devicetls.MinVersionProperty, devicetls.AllowDeprecatedProperty,
		devicetls.AllowLegacyCiphersProperty, devicetls.ClientCertificateProperty} {
		_, err := newServer(map[string]inventory.PropertyValue{key: true})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("property %s: err = %v, want a refusal naming it", key, err)
		}
	}
	if _, err := newServer(map[string]inventory.PropertyValue{"port": 5986, devicetls.CAPEMProperty: "not a certificate"}); err == nil {
		t.Error("a tls_ca_pem holding no certificate was accepted")
	}
}

// The HTTP listener has no TLS, so a pin aimed at it would be ignored on
// every run. It is refused whether the port is written or defaulted, and
// accepted on any other port, since an HTTPS listener can be moved.
func TestServer_RefusesAPinOnTheHTTPListener(t *testing.T) {
	for name, props := range map[string]map[string]inventory.PropertyValue{
		"default port":  {devicetls.CAPEMProperty: testCA},
		"explicit 5985": {"port": 5985, devicetls.ServerNameProperty: "vengeance"},
	} {
		_, err := newServer(props)
		if err == nil || !strings.Contains(err.Error(), "HTTP one") || !strings.Contains(err.Error(), "5986") {
			t.Errorf("%s: err = %v, want a refusal pointing at 5986", name, err)
		}
	}
	if _, err := newServer(map[string]inventory.PropertyValue{"port": 443, devicetls.CAPEMProperty: testCA}); err != nil {
		t.Errorf("a pin on a moved HTTPS listener was refused: %v", err)
	}
}
