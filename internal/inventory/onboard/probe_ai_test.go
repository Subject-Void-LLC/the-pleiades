// Tests for the language model provider probe against real servers
// speaking each kind's model listing.
package onboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestAIProbe_ListsModelsWithItsOwnKey: a hosted provider's listing is
// asked for with the stored key, over the pinned authority, and the
// models it names become the discovery.
func TestAIProbe_ListsModelsWithItsOwnKey(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-a"}]}`))
	}))
	defer srv.Close()
	dev := build(t, ai.TypeProvider, map[string]inventory.PropertyValue{
		ai.KindProperty: "anthropic", ai.APIBaseProperty: srv.URL, ai.ResidencyProperty: "hosted", devicetls.CAPEMProperty: caPEM(srv),
	})
	got, err := aiProber{}.Probe(context.Background(), dev, map[string]string{"bearer_token": "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Capabilities, []capability.Name{capability.NameAIProvider}) {
		t.Errorf("capabilities %v", got.Capabilities)
	}
	if models, _ := got.Facts[ai.FactModels].([]any); !slices.Equal(models, []any{"model-a", "model-b"}) {
		t.Errorf("models %v, want the listing sorted with its duplicate removed", got.Facts[ai.FactModels])
	}
	if got.Facts[ai.FactModelsTruncated] != false || got.Facts["kind"] != "anthropic" {
		t.Errorf("facts %v", got.Facts)
	}

	if _, err := (aiProber{}).Probe(context.Background(), dev, map[string]string{"bearer_token": "wrong"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a refused key: err %v, want the 401", err)
	}
}

// TestAIProbe_NoKeyIsRefusedBeforeContact: a kind that needs a key, with
// none stored, is refused naming the command that stores one, and the
// server never sees a request.
func TestAIProbe_NoKeyIsRefusedBeforeContact(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	dev := build(t, ai.TypeProvider, map[string]inventory.PropertyValue{
		ai.KindProperty: "google", ai.APIBaseProperty: srv.URL, ai.ResidencyProperty: "hosted", devicetls.CAPEMProperty: caPEM(srv),
	})
	_, err := aiProber{}.Probe(context.Background(), dev, nil)
	if err == nil || !strings.Contains(err.Error(), "add-credential d1") {
		t.Fatalf("err %v, want a refusal naming add-credential", err)
	}
	if hits.Load() != 0 {
		t.Errorf("the server saw %d requests", hits.Load())
	}
}

// TestAIProbe_KeyStaysOnItsOrigin: a listing that redirects elsewhere is
// not followed, so the key never reaches the other origin.
func TestAIProbe_KeyStaysOnItsOrigin(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	srv := httptest.NewTLSServer(http.RedirectHandler(other.URL+"/v1/models", http.StatusTemporaryRedirect))
	defer srv.Close()
	dev := build(t, ai.TypeProvider, map[string]inventory.PropertyValue{
		ai.KindProperty: "openai", ai.APIBaseProperty: srv.URL + "/v1", ai.ResidencyProperty: "hosted", devicetls.CAPEMProperty: caPEM(srv),
	})
	if _, err := (aiProber{}).Probe(context.Background(), dev, map[string]string{"bearer_token": "sk-test"}); err == nil {
		t.Error("a redirect proved the provider")
	}
	if elsewhere.Load() != 0 {
		t.Errorf("the other origin received %d requests", elsewhere.Load())
	}
}

// TestAIProbe_LocalOllamaNeedsNoKey: an Ollama host over plain HTTP, with
// no credential, is proved by its tag listing, and a listing longer than
// a discovery keeps is marked cut.
func TestAIProbe_LocalOllamaNeedsNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var b strings.Builder
		b.WriteString(`{"models":[`)
		for i := range maxFactList + 1 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"name":"m` + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + `:` + string(rune('0'+i%10)) + `"}`)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	dev := build(t, ai.TypeProvider, map[string]inventory.PropertyValue{
		ai.KindProperty: "ollama", ai.APIBaseProperty: srv.URL, ai.ResidencyProperty: "local",
	})
	got, err := aiProber{}.Probe(context.Background(), dev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Facts[ai.FactModelsTruncated] != true {
		t.Errorf("facts %v, want the listing marked cut", got.Facts)
	}
}
