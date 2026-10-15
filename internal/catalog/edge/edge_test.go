// Tests for the edge methods against a real HTTPS server answering as
// Cloudflare's API documents, with a real, onboarded cloudflare_account.
package edge_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/edge"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cloudflare"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi/cfapitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

const (
	acct  = "0123456789abcdef0123456789abcdef"
	tun   = "11111111-2222-3333-4444-555555555555"
	app   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	zone  = "00000000000000000000000000000001"
	token = "cf-sentinel-token-7731"
)

// ctxStub keeps every stat, with the account's token as its credential.
type ctxStub struct{ stats map[string]any }

func (c *ctxStub) InjectSecrets() map[string]string { return map[string]string{"bearer_token": token} }
func (c *ctxStub) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *ctxStub) EmitFact(k string, v any) error   { return c.SetStat(k, v) }
func newCtx() *ctxStub                              { return &ctxStub{stats: map[string]any{}} }

// fixture is the fake API holding one account's tunnel, application and
// zone, and the onboarded account device that reads it.
func fixture(t *testing.T) (*cfapitest.Server, inventory.InventoryItem) {
	t.Helper()
	srv := cfapitest.New(t, acct, token)
	srv.Tunnels = []map[string]any{{"id": tun, "name": "prod", "status": "healthy"}}
	srv.Connectors[tun] = []map[string]any{
		{"id": "c1", "version": "2026.9.0", "arch": "linux_amd64", "conns": []map[string]any{{"colo_name": "fra01"}, {"colo_name": "ams01"}}},
		{"id": "c2", "version": "2026.9.0", "arch": "linux_arm64", "conns": []map[string]any{}},
	}
	srv.Apps = []map[string]any{{"id": app, "name": "Admin", "domain": "admin.example.com", "type": "self_hosted", "aud": "audtag", "session_duration": "24h"}}
	srv.AppPolicies[app] = []map[string]any{
		{"id": "p1", "name": "staff", "decision": "allow", "precedence": 1, "include": []any{map[string]any{"email_domain": map[string]any{"domain": "example.com"}}}},
		{"id": "p2", "name": "bots", "decision": "bypass", "precedence": 2, "include": []any{map[string]any{"ip": map[string]any{"ip": "192.0.2.0/24"}}}},
	}
	srv.AuthDomain = "example.cloudflareaccess.com"
	srv.Zones = []map[string]any{{"id": zone, "name": "example.com", "status": "active", "account": map[string]any{"id": acct}}}
	srv.Records[zone] = []map[string]any{
		{"id": "r1", "type": "A", "name": "www.example.com", "content": "192.0.2.10", "proxied": true, "ttl": 1},
		{"id": "r2", "type": "A", "name": "www.example.com", "content": "192.0.2.11", "proxied": true, "ttl": 1},
	}
	props := map[string]inventory.PropertyValue{
		cloudflare.AccountIDProperty: acct, cloudflare.APIBaseProperty: srv.URL + "/client/v4",
		devicetls.CAPEMProperty: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})),
	}
	d := inventory.Discovery{Protocol: "https", Capabilities: []capability.Name{capability.NameCloudflareAPI}}
	d.Binding = record.DiscoveryBinding(cloudflare.TypeAccount, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	dev, err := cloudflare.NewAccount(record.Record{ID: "cf", Name: "prod-cf", Type: cloudflare.TypeAccount, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return srv, dev
}

// TestTunnel: healthy with one connector up passes; asking for two fails
// naming it; another status fails.
func TestTunnel(t *testing.T) {
	_, dev := fixture(t)
	rc := newCtx()
	if _, err := edge.Tunnel(context.Background(), rc, dev, map[string]any{"tunnel": "prod"}); err != nil {
		t.Fatal(err)
	}
	if rc.Stats()["connectors_up"] != 1 || rc.Stats()["connections"] != 2 {
		t.Errorf("stats %v", rc.Stats())
	}
	if _, err := edge.Tunnel(context.Background(), newCtx(), dev, map[string]any{"tunnel": "prod", "min_connectors": 2}); err == nil || !strings.Contains(err.Error(), "fewer than 2") {
		t.Errorf("two connectors: %v", err)
	}
	if _, err := edge.Tunnel(context.Background(), newCtx(), dev, map[string]any{"tunnel": tun, "expect_status": "degraded"}); err == nil || !strings.Contains(err.Error(), "healthy, not degraded") {
		t.Errorf("status: %v", err)
	}
}

// TestAccess: the application as expected passes; every kind of
// difference is listed at once.
func TestAccess(t *testing.T) {
	_, dev := fixture(t)
	good := map[string]any{
		"audience": "audtag", "issuer": "https://EXAMPLE.cloudflareaccess.com", "domain": "admin.example.com",
		"policies": []any{
			map[string]any{"name": "staff", "decision": "allow", "include": []any{map[string]any{"email_domain": map[string]any{"domain": "example.com"}}}},
			map[string]any{"name": "bots", "decision": "bypass"},
		},
	}
	if _, err := edge.Access(context.Background(), newCtx(), dev, map[string]any{"app": "admin.example.com", "expect": good, "exact_policies": true}); err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{
		"audience": "other", "session_duration": "1h",
		"policies": []any{
			map[string]any{"name": "staff", "decision": "deny", "include": []any{map[string]any{"everyone": map[string]any{}}}},
			map[string]any{"name": "contractors", "decision": "allow"},
		},
	}
	rc := newCtx()
	_, err := edge.Access(context.Background(), rc, dev, map[string]any{"app": app, "expect": bad, "exact_policies": true})
	if err == nil {
		t.Fatal("a differing application passed")
	}
	for _, want := range []string{"audience is", "session_duration is", `policy "staff" decides allow, not deny`, `policy "staff"'s include rules`, `policy "contractors" is missing`, `policy "bots" is not expected`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
	if d, _ := rc.Stats()["differences"].([]any); len(d) != 6 {
		t.Errorf("differences %v", d)
	}
	if _, err := edge.Access(context.Background(), newCtx(), dev, map[string]any{"app": app, "expect": map[string]any{"audiences": "x"}}); err == nil {
		t.Error("an unknown expect key was accepted")
	}
}

// TestDNS: the set as expected passes in any order and form; a missing
// content, a proxied flag and absent are each caught.
func TestDNS(t *testing.T) {
	_, dev := fixture(t)
	ok := map[string]any{"zone": "example.com", "name": "www.example.com", "type": "A", "expect": map[string]any{"content": []any{"192.0.2.11", "192.0.2.10"}, "proxied": true, "ttl": 1}}
	if _, err := edge.DNS(context.Background(), newCtx(), dev, ok); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		expect map[string]any
		want   string
	}{
		"a content short": {map[string]any{"content": "192.0.2.10"}, "not [192.0.2.10]"},
		"not proxied":     {map[string]any{"proxied": false}, "proxied true, not false"},
		"should be gone":  {map[string]any{"absent": true}, "where none should"},
	} {
		params := map[string]any{"zone": zone, "name": "www.example.com", "type": "A", "expect": tc.expect}
		if _, err := edge.DNS(context.Background(), newCtx(), dev, params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := edge.DNS(context.Background(), newCtx(), dev, map[string]any{"zone": zone, "name": "mail.example.com", "type": "MX"}); err == nil || !strings.Contains(err.Error(), "no MX record") {
		t.Errorf("no record: %v", err)
	}
}

// TestEdge_TheTokenIsNowhere: every method's stats and errors are free of
// the token, on success and on refusal, and a check is the task itself.
func TestEdge_TheTokenIsNowhere(t *testing.T) {
	srv, dev := fixture(t)
	for _, fqcn := range []string{"edge.tunnel", "edge.access", "edge.dns"} {
		desc, _ := collection.Lookup(fqcn)
		if !desc.Manifest.Reversibility.ReadOnly || desc.Check == nil {
			t.Errorf("%s is not a read with a check", fqcn)
		}
	}
	srv.Fail["/accounts/"+acct+"/access"] = 403
	for _, call := range []func(*ctxStub) error{
		func(rc *ctxStub) error {
			_, err := edge.Tunnel(context.Background(), rc, dev, map[string]any{"tunnel": "prod"})
			return err
		},
		func(rc *ctxStub) error {
			_, err := edge.Access(context.Background(), rc, dev, map[string]any{"app": app})
			return err
		},
		func(rc *ctxStub) error {
			_, err := edge.DNS(context.Background(), rc, dev, map[string]any{"zone": zone, "name": "www.example.com", "type": "A"})
			return err
		},
	} {
		rc := newCtx()
		err := call(rc)
		if err != nil && strings.Contains(err.Error(), token) {
			t.Errorf("an error holds the token: %v", err)
		}
		for k, v := range rc.Stats() {
			if strings.Contains(strings.ToLower(strings.ReplaceAll(k+" "+toString(v), "\n", " ")), token) {
				t.Errorf("stat %s holds the token", k)
			}
		}
	}
}

func (c *ctxStub) Stats() map[string]any { return c.stats }

// toString is a stat as text, for a search.
func toString(v any) string { return fmt.Sprint(v) }
