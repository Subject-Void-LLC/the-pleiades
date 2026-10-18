// Package cfapi: a client built from a Cloudflare account device's record
// and credential.
package cfapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// requestTimeout bounds one request, its answer read included, so an API
// that accepts a connection and then never answers cannot hold a run open
// (a run has no deadline of its own by default).
const requestTimeout = time.Minute

// ClientFor builds the client of device, a CloudflareAPICapable device,
// from its record and its own credential: the stored bearer token, which
// is required; the base URL held to https; the device's TLS, always
// verified; and no redirect followed, so the token reaches the base URL's
// origin alone. It returns what the record's TLS settings make a run say.
func ClientFor(device any, name string, secrets map[string]string) (*Client, []string, error) {
	dev, ok := device.(capability.CloudflareAPICapable)
	if !ok {
		return nil, nil, errors.New("the device is not a Cloudflare account")
	}
	token := secrets[wire.SecretBearerToken]
	if token == "" {
		return nil, nil, fmt.Errorf("%s has no API token stored (pleiades add-credential %s --token-stdin)", name, name)
	}
	base, err := httpapi.ValidateBaseURL(dev.CloudflareAPIBase(), httpapi.AuthBearer, false)
	if err != nil {
		return nil, nil, err
	}
	settings := devicetls.For(device)
	cfg, err := settings.Config(secrets)
	if err != nil {
		return nil, nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = cfg
	transport.Proxy = nil
	return &Client{
		Base:  base,
		Token: token,
		HTTP: &http.Client{
			Transport:     transport,
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, settings.Warnings(name), nil
}
