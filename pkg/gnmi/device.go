// Package gnmi: a client built from a gNMI-capable device's record and its
// own credential, for the net.gnmi methods, wait.gnmi and onboarding alike,
// so they can never disagree about how a device is reached.
package gnmi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// PrefixTargeted is implemented by a gNMI device whose record names the
// target every request's prefix carries (generic_gnmi's gnmi_target), for a
// server that serves more than one data tree and refuses a request naming
// none, as SONiC's does ("Empty target data not supported").
type PrefixTargeted interface {
	// GNMIPrefixTarget returns the target, empty for none.
	GNMIPrefixTarget() string
}

// ClientFor builds the client of device, a GNMICapable device named name,
// from its record and its own credential (secrets): its target, its
// credential mode, its TLS (pkg/devicetls, always verified, a pinned
// certificate included, mutual TLS from the stored certificate), or the
// plaintext connection its record asks for, and the prefix target its record
// names (PrefixTargeted). It returns what the record's settings make every
// run say: each TLS weakening, a plaintext connection, and a credential sent
// over one.
//
// It is ClientForContext outside a run, for a caller no key log can reach:
// onboarding's probe, and tests.
func ClientFor(device any, name string, secrets map[string]string) (*Client, []string, error) {
	return ClientForContext(context.Background(), device, name, secrets)
}

// ClientForContext is ClientFor for a run's method: a TLS client built under
// ctx writes its sessions' secrets to the run's key log when ctx carries one
// (devicetls.WithKeyLog), as pleiades run --tls-keylog asks.
func ClientForContext(ctx context.Context, device any, name string, secrets map[string]string) (*Client, []string, error) {
	dev, ok := device.(capability.GNMICapable)
	if !ok {
		return nil, nil, errors.New("the device does not serve gNMI")
	}
	opts := Options{
		Target:                    dev.GNMITarget(),
		Plaintext:                 dev.GNMIPlaintext(),
		Auth:                      dev.GNMIAuth(),
		AllowPlaintextCredentials: dev.GNMIAllowPlaintextCredentials(),
		Secrets:                   secrets,
	}
	if targeted, ok := device.(PrefixTargeted); ok {
		opts.PrefixTarget = targeted.GNMIPrefixTarget()
	}
	settings := devicetls.For(device)
	var warnings []string
	if opts.Plaintext {
		warnings = append(warnings, PlaintextWarning(name))
		if opts.Auth != AuthNone {
			warnings = append(warnings, PlaintextCredentialWarning(name))
		}
	} else {
		cfg, err := settings.ConfigContext(ctx, secrets)
		if err != nil {
			return nil, nil, err
		}
		opts.TLS = cfg
		warnings = append(warnings, settings.Warnings(name)...)
	}
	client, err := New(opts)
	if err != nil {
		return nil, nil, err
	}
	return client, warnings, nil
}

// PlaintextWarning says what a plaintext gNMI connection to the named device
// means, for every onboarding and run that makes one.
func PlaintextWarning(name string) string {
	return fmt.Sprintf("device %s is reached over gRPC without TLS (grpc_plaintext): nothing proves which server answered, and every value read or written crosses the network unencrypted", name)
}

// PlaintextCredentialWarning says what sending the named device's credential
// over a plaintext connection means.
func PlaintextCredentialWarning(name string) string {
	return fmt.Sprintf("device %s's credential is sent over gRPC without TLS (grpc_allow_plaintext_credentials): anyone on the path can read it from the request metadata", name)
}
