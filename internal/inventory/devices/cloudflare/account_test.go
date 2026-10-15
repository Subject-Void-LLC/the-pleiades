// Tests for the cloudflare_account record and the client built from it.
package cloudflare_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cloudflare"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

const acct = "0123456789abcdef0123456789abcdef"

func build(p map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return cloudflare.NewAccount(record.Record{ID: "cf", Name: "prod-cf", Type: cloudflare.TypeAccount, Properties: p})
}

// TestNewAccount: what a record must be, and what it reads back as.
func TestNewAccount(t *testing.T) {
	item, err := build(map[string]inventory.PropertyValue{cloudflare.AccountIDProperty: acct})
	if err != nil {
		t.Fatal(err)
	}
	cf := item.(capability.CloudflareAPICapable)
	if cf.CloudflareAccountID() != acct || cf.CloudflareAPIBase() != cfapi.DefaultBase {
		t.Errorf("%s %s", cf.CloudflareAccountID(), cf.CloudflareAPIBase())
	}
	if item.HasCapability(capability.NameCloudflareAPI) {
		t.Error("an account holds CloudflareAPICapable before onboarding")
	}
	for name, p := range map[string]map[string]inventory.PropertyValue{
		"no id":            {},
		"a short id":       {cloudflare.AccountIDProperty: "0123"},
		"an upper id":      {cloudflare.AccountIDProperty: strings.ToUpper(acct)},
		"plain http":       {cloudflare.AccountIDProperty: acct, cloudflare.APIBaseProperty: "http://api.example.test"},
		"a key in the URL": {cloudflare.AccountIDProperty: acct, cloudflare.APIBaseProperty: "https://t:x@api.example.test"},
		"old TLS":          {cloudflare.AccountIDProperty: acct, devicetls.MinVersionProperty: "1.0"},
		"a client cert":    {cloudflare.AccountIDProperty: acct, devicetls.ClientCertificateProperty: true},
	} {
		if _, err := build(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestClientFor: no token is refused naming the command that stores one;
// a token is sent only as the client's own.
func TestClientFor(t *testing.T) {
	item, err := build(map[string]inventory.PropertyValue{cloudflare.AccountIDProperty: acct})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfapi.ClientFor(item, "prod-cf", map[string]string{"password": "p"}); err == nil || !strings.Contains(err.Error(), "add-credential prod-cf --token-stdin") {
		t.Errorf("no token: %v", err)
	}
	c, _, err := cfapi.ClientFor(item, "prod-cf", map[string]string{"bearer_token": "tok"})
	if err != nil || c.Token != "tok" || c.Base.String() != cfapi.DefaultBase {
		t.Errorf("client %+v %v", c, err)
	}
}
