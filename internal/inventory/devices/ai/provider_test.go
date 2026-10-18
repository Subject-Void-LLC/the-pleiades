// Tests for the ai_provider record: what it refuses, what its accessors
// say, and where its key may go.
package ai_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// props is a valid hosted provider's record, with overrides.
func props(over map[string]inventory.PropertyValue) map[string]inventory.PropertyValue {
	p := map[string]inventory.PropertyValue{
		ai.KindProperty: "anthropic", ai.APIBaseProperty: "https://api.example.test", ai.ResidencyProperty: "hosted",
	}
	for k, v := range over {
		if v == nil {
			delete(p, k)
			continue
		}
		p[k] = v
	}
	return p
}

func build(p map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return ai.NewProvider(record.Record{ID: "p1", Name: "claude", Type: ai.TypeProvider, Properties: p})
}

// TestNewProvider_Refuses: each record a provider cannot be, refused with
// the property that makes it so.
func TestNewProvider_Refuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		over map[string]inventory.PropertyValue
		want string
	}{
		{"no kind", map[string]inventory.PropertyValue{ai.KindProperty: nil}, ai.KindProperty},
		{"a kind that is not a name", map[string]inventory.PropertyValue{ai.KindProperty: "Open AI"}, ai.KindProperty},
		{"no residency", map[string]inventory.PropertyValue{ai.ResidencyProperty: nil}, ai.ResidencyProperty},
		{"another residency", map[string]inventory.PropertyValue{ai.ResidencyProperty: "eu"}, ai.ResidencyProperty},
		{"no base", map[string]inventory.PropertyValue{ai.APIBaseProperty: nil}, ai.APIBaseProperty},
		{"a key in the base", map[string]inventory.PropertyValue{ai.APIBaseProperty: "https://k:sk@api.example.test"}, ai.APIBaseProperty},
		{"hosted over http", map[string]inventory.PropertyValue{ai.APIBaseProperty: "http://api.example.test"}, "hosted provider is reached over https only"},
		{"TLS on http", map[string]inventory.PropertyValue{ai.ResidencyProperty: "local", ai.APIBaseProperty: "http://127.0.0.1:11434", devicetls.ServerNameProperty: "x"}, devicetls.ServerNameProperty},
		{"a plaintext flag on https", map[string]inventory.PropertyValue{httpapi.AllowPlaintextCredentialsProperty: true}, "allows nothing"},
		{"a plaintext flag that is a string", map[string]inventory.PropertyValue{httpapi.AllowPlaintextCredentialsProperty: "yes"}, httpapi.AllowPlaintextCredentialsProperty},
		{"prices with no currency", map[string]inventory.PropertyValue{ai.PricesProperty: "m=1/2"}, ai.CurrencyProperty},
		{"a currency with no prices", map[string]inventory.PropertyValue{ai.CurrencyProperty: "USD"}, ai.PricesProperty},
		{"a price that is not a pair", map[string]inventory.PropertyValue{ai.PricesProperty: "m=1", ai.CurrencyProperty: "USD"}, "model=input/output"},
		{"a negative price", map[string]inventory.PropertyValue{ai.PricesProperty: "m=-1/2", ai.CurrencyProperty: "USD"}, "non-negative"},
		{"a price that is not a number", map[string]inventory.PropertyValue{ai.PricesProperty: "m=NaN/2", ai.CurrencyProperty: "USD"}, "non-negative"},
		{"an exponent", map[string]inventory.PropertyValue{ai.PricesProperty: "m=1e3/2", ai.CurrencyProperty: "USD"}, "non-negative"},
		{"a model priced twice", map[string]inventory.PropertyValue{ai.PricesProperty: "m=1/2, m=3/4", ai.CurrencyProperty: "USD"}, "twice"},
		{"a table that is not text", map[string]inventory.PropertyValue{ai.PricesProperty: 3, ai.CurrencyProperty: "USD"}, ai.PricesProperty},
		{"a guard on a hosted API", map[string]inventory.PropertyValue{ai.HostGuardProperty: ai.GuardSingleResident}, "Ollama host only"},
		{"another guard", map[string]inventory.PropertyValue{ai.KindProperty: "ollama", ai.HostGuardProperty: "none"}, ai.GuardSingleResident},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(props(tc.over))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// TestNewProvider_Accessors: a full record reads back through the
// capability's accessors, and the price table is a copy.
func TestNewProvider_Accessors(t *testing.T) {
	item, err := build(props(map[string]inventory.PropertyValue{
		ai.KindProperty: "ollama", ai.ResidencyProperty: "local", ai.APIBaseProperty: "http://10.0.0.5:11434",
		ai.HostGuardProperty: ai.GuardSingleResident, ai.PricesProperty: "llama3.1:8b=0/0, qwen3:14b = 0.5 / 1.25", ai.CurrencyProperty: "USD",
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := item.(capability.AIProviderCapable)
	if p.AIKind() != "ollama" || p.AIResidency() != "local" || p.AIHostGuard() != ai.GuardSingleResident || p.AIBaseURL() != "http://10.0.0.5:11434" {
		t.Errorf("accessors %s %s %s %s", p.AIKind(), p.AIResidency(), p.AIHostGuard(), p.AIBaseURL())
	}
	prices := p.AIPrices()
	if got := prices["qwen3:14b"]; got != (capability.AIPrice{Input: 0.5, Output: 1.25, Currency: "USD"}) {
		t.Errorf("price %+v", got)
	}
	prices["qwen3:14b"] = capability.AIPrice{}
	if p.AIPrices()["qwen3:14b"].Output != 1.25 {
		t.Error("changing the returned table changed the record's")
	}
	if item.(inventory.Shared).SharedAcrossRuns() != true {
		t.Error("a provider is not shared across runs")
	}
	// Before onboarding the capability is not held: it is discovered.
	if item.HasCapability(capability.NameAIProvider) {
		t.Error("an un-onboarded provider holds AIProviderCapable")
	}
}

// TestEndpointFor_KeyOverPlainHTTP: a local server over http:// is reached
// with no key; with one stored, the key is refused there unless the record
// allows it by name, and then the call warns.
func TestEndpointFor_KeyOverPlainHTTP(t *testing.T) {
	local := props(map[string]inventory.PropertyValue{ai.KindProperty: "openai", ai.ResidencyProperty: "local", ai.APIBaseProperty: "http://10.0.0.5:1234/v1"})
	item, err := build(local)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aiprovider.EndpointFor(item, "lm", nil); err != nil {
		t.Errorf("no key over http: %v", err)
	}
	if _, err := aiprovider.EndpointFor(item, "lm", map[string]string{"bearer_token": "k"}); err == nil || !strings.Contains(err.Error(), httpapi.AllowPlaintextCredentialsProperty) {
		t.Errorf("a key over http: err %v, want a refusal naming %s", err, httpapi.AllowPlaintextCredentialsProperty)
	}
	local[httpapi.AllowPlaintextCredentialsProperty] = true
	if item, err = build(local); err != nil {
		t.Fatal(err)
	}
	if _, err := aiprovider.EndpointFor(item, "lm", nil); err != nil {
		t.Errorf("allowed by name, with no key stored: %v", err)
	}
	got, err := aiprovider.EndpointFor(item, "lm", map[string]string{"bearer_token": "k"})
	if err != nil || len(got.Warnings) == 0 || got.Endpoint.Key != "k" {
		t.Errorf("allowed by name: %+v %v, want the key sent with a warning", got.Warnings, err)
	}
}

// TestEndpointFor_UnknownKind: a record naming a kind nothing registered
// loads (an external Collection may register it), and is refused where a
// call would be made.
func TestEndpointFor_UnknownKind(t *testing.T) {
	item, err := build(props(map[string]inventory.PropertyValue{ai.KindProperty: "elsewhere"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aiprovider.EndpointFor(item, "x", nil); err == nil || !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("err %v, want a refusal naming the kind", err)
	}
}
