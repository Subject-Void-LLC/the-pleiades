// Tests of what a batfish record may say about where the service is.
package batfish_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// build makes a batfish device from props.
func build(props map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return batfish.NewService(record.Record{Name: "bf", Type: batfish.TypeService, Properties: props})
}

// TestNewService_PlainHTTPOnlyWhereItIsSafe: loopback over http is fine,
// another address needs the allowance (and is warned about), and an
// allowance that allows nothing is refused.
func TestNewService_PlainHTTPOnlyWhereItIsSafe(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:9996", "http://localhost:9996/", "http://[::1]:9996", "https://batfish.example:9996"} {
		item, err := build(map[string]inventory.PropertyValue{batfish.APIBaseProperty: base})
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		bf := item.(capability.BatfishAPICapable)
		if !item.HasCapability(capability.NameBatfishAPI) || bf.BatfishPlaintextWarning() != "" || strings.HasSuffix(bf.BatfishBaseURL(), "/") {
			t.Errorf("%s: capable %v, warning %q, base %q", base, item.HasCapability(capability.NameBatfishAPI), bf.BatfishPlaintextWarning(), bf.BatfishBaseURL())
		}
	}
	item, err := build(map[string]inventory.PropertyValue{batfish.APIBaseProperty: "http://10.0.0.9:9996", batfish.AllowPlaintextProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := item.(capability.BatfishAPICapable).BatfishPlaintextWarning(); !strings.Contains(w, "plain HTTP") {
		t.Errorf("warning = %q", w)
	}
	for name, props := range map[string]map[string]inventory.PropertyValue{
		"no base":                    {},
		"not a URL":                  {batfish.APIBaseProperty: "batfish:9996"},
		"another scheme":             {batfish.APIBaseProperty: "ftp://127.0.0.1:9996"},
		"a path":                     {batfish.APIBaseProperty: "http://127.0.0.1:9996/v2"},
		"credentials in the URL":     {batfish.APIBaseProperty: "http://u:p@127.0.0.1:9996"},
		"plain http to another host": {batfish.APIBaseProperty: "http://10.0.0.9:9996"},
		"an allowance on https":      {batfish.APIBaseProperty: "https://batfish.example", batfish.AllowPlaintextProperty: true},
		"an allowance on loopback":   {batfish.APIBaseProperty: "http://127.0.0.1:9996", batfish.AllowPlaintextProperty: true},
		"an allowance not a bool":    {batfish.APIBaseProperty: "http://10.0.0.9:9996", batfish.AllowPlaintextProperty: "yes"},
		"TLS settings on http":       {batfish.APIBaseProperty: "http://127.0.0.1:9996", "tls_server_name": "batfish"},
	} {
		if _, err := build(props); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
