// Package restconf: a client built from a RESTCONF-capable device's record
// and its own credential, for the net.restconf methods and onboarding alike,
// so the two can never disagree about how a device is reached.
package restconf

import (
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
func ClientFor(device any, name string, secrets map[string]string) (*Client, []string, error) {
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
	if base.Scheme == "https" {
		cfg, err := settings.Config(secrets)
		if err != nil {
			return nil, nil, err
		}
		transport.TLSClientConfig = cfg
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
	var warnings []string
	if base.Scheme == "https" {
		warnings = settings.Warnings(name)
	}
	if httpapi.SendsPlaintextCredential(base, dev.RestconfAuth()) {
		warnings = append(warnings, httpapi.PlaintextWarning(name))
	}
	return client, warnings, nil
}
