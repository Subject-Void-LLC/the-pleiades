// Package restconf: a client built from a RESTCONF-capable device's record
// and its own credential, for the net.restconf methods and onboarding alike,
// so the two can never disagree about how a device is reached.
package restconf

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// requestTimeout bounds one request, its answer read included, so a device
// that accepts a connection and never answers cannot hold a run open. It is
// a variable only so a test can prove the bound quickly; nothing else sets
// it.
var requestTimeout = time.Minute

// ClientFor builds the client of device, a RestconfCapable device named
// name, from its record and its own credential (secrets): its origin and
// credential mode (httpapi.ValidateBaseURL's rules), its TLS (pkg/devicetls,
// always verified, a pinned certificate included), no proxy, and no redirect
// followed. It returns what the record's settings make a run say: each TLS
// weakening, and a credential sent over http://.
//
// It is ClientForContext outside a run, for a caller no key log can reach:
// onboarding's probe, and tests.
func ClientFor(device any, name string, secrets map[string]string) (*Client, []string, error) {
	return ClientForContext(context.Background(), device, name, secrets)
}

// ClientForContext is ClientFor for a run's method: an https:// client built
// under ctx writes its sessions' secrets to the run's key log when ctx carries
// one (devicetls.WithKeyLog), as pleiades run --tls-keylog asks.
func ClientForContext(ctx context.Context, device any, name string, secrets map[string]string) (*Client, []string, error) {
	dev, ok := device.(capability.RestconfCapable)
	if !ok {
		return nil, nil, errors.New("the device does not serve RESTCONF")
	}
	base, err := httpapi.ValidateBaseURL(dev.RestconfBaseURL(), dev.RestconfAuth(), dev.RestconfAllowPlaintextCredentials())
	if err != nil {
		return nil, nil, err
	}
	settings := devicetls.For(device)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	var watch *devicetls.Watch
	if base.Scheme == "https" {
		cfg, err := settings.ConfigContext(ctx, secrets)
		if err != nil {
			return nil, nil, err
		}
		// Watched, so a server refusing the client certificate (or its
		// absence) is said as a refusal naming tls_client_certificate.
		transport.TLSClientConfig, watch = devicetls.Watched(cfg)
	}
	hc := &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client, err := New(dev.RestconfBaseURL(), hc, dev.RestconfAuth(), dev.RestconfAllowPlaintextCredentials(), secrets)
	if err != nil {
		return nil, nil, err
	}
	client.certWatch = watch
	var warnings []string
	if base.Scheme == "https" {
		warnings = settings.Warnings(name)
	}
	if httpapi.SendsPlaintextCredential(base, dev.RestconfAuth()) {
		warnings = append(warnings, httpapi.PlaintextWarning(name))
	}
	return client, warnings, nil
}
