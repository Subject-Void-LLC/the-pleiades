// Tests for the Cloudflare account probe against a real HTTPS server
// answering as Cloudflare's API documents.
package onboard

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cloudflare"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi/cfapitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestCloudflareProbe: the account read with its token proves it, with
// the account's name; a wrong token and another account prove nothing.
func TestCloudflareProbe(t *testing.T) {
	const acct = "0123456789abcdef0123456789abcdef"
	srv := cfapitest.New(t, acct, "tok")
	srv.AccountName = "Production"
	props := func(id string) map[string]inventory.PropertyValue {
		return map[string]inventory.PropertyValue{cloudflare.AccountIDProperty: id, cloudflare.APIBaseProperty: srv.URL + "/client/v4", devicetls.CAPEMProperty: caPEM(srv.Server)}
	}
	got, err := cloudflareProber{}.Probe(context.Background(), build(t, cloudflare.TypeAccount, props(acct)), map[string]string{"bearer_token": "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Capabilities, []capability.Name{capability.NameCloudflareAPI}) || got.Facts[cloudflare.FactAccountName] != "Production" {
		t.Errorf("probed %+v", got)
	}
	if _, err := (cloudflareProber{}).Probe(context.Background(), build(t, cloudflare.TypeAccount, props(acct)), map[string]string{"bearer_token": "wrong"}); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong") {
		t.Errorf("a wrong token: %v", err)
	}
	if _, err := (cloudflareProber{}).Probe(context.Background(), build(t, cloudflare.TypeAccount, props("fedcba9876543210fedcba9876543210")), map[string]string{"bearer_token": "tok"}); err == nil {
		t.Error("another account was proved")
	}
	// A stand-in API answering success for the account asked about with
	// another account's object, or none, proves nothing.
	for _, result := range []string{`{"id":"fedcba9876543210fedcba9876543210","name":"Other"}`, `null`} {
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":` + result + `}`))
		})
		if _, err := (cloudflareProber{}).Probe(context.Background(), build(t, cloudflare.TypeAccount, props(acct)), map[string]string{"bearer_token": "tok"}); err == nil || !strings.Contains(err.Error(), "answered for account") {
			t.Errorf("an answer of %s = %v", result, err)
		}
	}
}
