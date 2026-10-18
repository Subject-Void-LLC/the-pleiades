// Package aiprovider: a provider device's endpoint, built from its record
// and credential under the same rules every HTTP API device is held to.
package aiprovider

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Provider is a provider device's kind and endpoint, with what its record
// allows that a run must say.
type Provider struct {
	Kind     Kind
	Endpoint Endpoint
	Warnings []string
}

// EndpointFor builds the endpoint of device, an AIProviderCapable device,
// from its record and its credential (secrets): the key is the stored
// bearer token, or the password; the base URL is held to pkg/httpapi's
// rules (https when a key is sent, unless the record says otherwise by
// name); TLS is the device's own (pkg/devicetls, always verified); and no
// redirect is followed, so the key reaches the provider's origin alone.
func EndpointFor(device any, name string, secrets map[string]string) (Provider, error) {
	dev, ok := device.(capability.AIProviderCapable)
	if !ok {
		return Provider{}, errors.New("the device is not a language model provider")
	}
	kind, ok := Lookup(dev.AIKind())
	if !ok {
		return Provider{}, fmt.Errorf("kind %q is not one this platform calls (%v)", dev.AIKind(), Names())
	}
	key := secrets[wire.SecretBearerToken]
	if key == "" {
		key = secrets[wire.SecretPassword]
	}
	auth := httpapi.AuthNone
	if key != "" {
		auth = httpapi.AuthBearer
	}
	// With no key stored nothing is sent, so the record's allowance for
	// sending one over http:// has nothing to allow.
	base, err := httpapi.ValidateBaseURL(dev.AIBaseURL(), auth, dev.AIAllowPlaintextCredentials() && key != "")
	if err != nil {
		return Provider{}, err
	}
	settings := devicetls.For(device)
	var cfg *tls.Config
	if base.Scheme == "https" {
		if cfg, err = settings.Config(secrets); err != nil {
			return Provider{}, err
		}
	}
	warnings := settings.Warnings(name)
	if httpapi.SendsPlaintextCredential(base, auth) {
		warnings = append(warnings, httpapi.PlaintextWarning(name))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = cfg
	transport.Proxy = nil
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return Provider{Kind: kind, Endpoint: Endpoint{Base: base, Client: client, Key: key}, Warnings: warnings}, nil
}

// PricesFor converts a record's price table into Prices.
func PricesFor(device any) Prices {
	dev, ok := device.(capability.AIProviderCapable)
	if !ok {
		return nil
	}
	out := Prices{}
	for model, p := range dev.AIPrices() {
		out[model] = Price{Input: p.Input, Output: p.Output, Currency: p.Currency}
	}
	return out
}
