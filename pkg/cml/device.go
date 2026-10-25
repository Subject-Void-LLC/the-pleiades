// Package cml: a client built from a cml_controller device's record and
// credential, logged in, for the virt.cml methods and import-cml
// --controller alike.
package cml

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// requestTimeout bounds one request, its answer read included, so a
// controller that accepts a connection and never answers cannot hold a run
// open.
const requestTimeout = time.Minute

// ClientFor builds and logs in the client of device, a CMLAPICapable
// device named name, from its record and its own credential (secrets): the
// stored username and password, both required; the device's TLS, always
// verified (a pinned certificate included); no proxy; and no redirect
// followed, so the login reaches the base URL's origin alone. It returns
// the controller's version and what the record's settings make a run say.
func ClientFor(ctx context.Context, device any, name string, secrets map[string]string) (*Client, string, []string, error) {
	dev, ok := device.(capability.CMLAPICapable)
	if !ok {
		return nil, "", nil, errors.New("the device is not a CML controller")
	}
	creds := Credentials{Username: secrets[wire.SecretUsername], Password: secrets[wire.SecretPassword]}
	if creds.Username == "" || creds.Password == "" {
		return nil, "", nil, fmt.Errorf("%s has no login stored (pleiades add-credential %s --username <user> --password-stdin)", name, name)
	}
	settings := devicetls.For(device)
	cfg, err := settings.Config(secrets)
	if err != nil {
		return nil, "", nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = cfg
	transport.Proxy = nil
	hc := &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client, err := New(dev.CMLBaseURL(), hc, creds)
	if err != nil {
		return nil, "", nil, err
	}
	warnings := settings.Warnings(name)
	if w := dev.CMLPlaintextWarning(); w != "" {
		warnings = append(warnings, w)
	}
	version, err := client.Connect(ctx)
	if err != nil {
		return nil, "", warnings, err
	}
	return client, version, warnings, nil
}
