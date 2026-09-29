// Package generic holds the protocol-shaped device types: generic_ssh,
// generic_netconf, generic_http and generic_grpc. They exist so a device
// nobody has written a vendor type for can still be managed, over the
// protocol it speaks.
//
// A vendor type's capabilities are backed by its Go code. A generic type
// cannot do that, since it knows only the protocol, so its capabilities
// beyond a small baseline come from the device itself: onboarding
// (internal/inventory/onboard) probes it over its protocol and records
// what it proved in the reserved inventory.DiscoveredProperty. Nothing
// else grants one. Classification cannot add to a generic type either:
// its constructor reads the baseline and the discovery, never
// rec.Capabilities, because a rule is a claim nobody verified.
//
// Each type implements the accessors of every capability onboarding may
// grant it (Discoverable), so the structural half of HasCapability holds
// for each, and refuses to load a record whose discovery names one it may
// not be granted.
//
// Every type here starts discovered (record.RegisterOnboardedType), which
// admits no execution until onboarding makes it active.
package generic

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// The generic device types.
const (
	TypeSSH     = "generic_ssh"
	TypeNetconf = "generic_netconf"
	TypeHTTP    = "generic_http"
	TypeGRPC    = "generic_grpc"
)

// discoverable is, per type, every capability onboarding may grant: the
// ones its protocol can carry and the type implements.
var discoverable = map[string][]capability.Name{
	TypeSSH: {
		capability.NameShellExec,
		capability.NameLinux,
		capability.NamePOSIXFileSystem,
		capability.NameFactGatherer,
		capability.NameSystemd,
		capability.NameFirewalld,
		capability.NameApt,
		capability.NameDnf,
		capability.NamePosixAccount,
	},
	TypeNetconf: {capability.NameNetconf},
	TypeHTTP:    {capability.NameHTTPAPI},
	TypeGRPC:    {capability.NameGRPC},
}

// Discoverable returns the capabilities onboarding may grant a device of
// deviceType, or nil for a type that is not generic.
func Discoverable(deviceType string) []capability.Name {
	return slices.Clone(discoverable[deviceType])
}

// bound is, per type, the properties a discovery holds for: the ones that
// decide where a probe went and what it trusted. A discovery records a
// digest over them (inventory.Discovery.Binding), and a type grants nothing
// from one whose digest no longer matches its record, however the record
// changed: set-host, the Controller's PATCH, a sync, or a hand-edited
// inventory.yaml (Phase 117a, finding S2).
//
// generic_http is the type this protects a credential for: its stored
// credential goes to its base URL, and TLS verification against a public
// authority would admit any host with a valid certificate for its own name.
// generic_grpc sends no credential, and is bound because a discovery made
// against one server says nothing about another. The SSH-based types are
// not bound: a strict host-key check already refuses a repointed host
// before any credential is sent.
var bound = map[string][]string{
	TypeHTTP: append([]string{BaseURLProperty, HTTPAuthProperty, httpapi.AllowPlaintextCredentialsProperty}, devicetls.Properties()...),
	TypeGRPC: append([]string{GRPCTargetProperty, GRPCPlaintextProperty}, devicetls.Properties()...),
}

// Binding is the digest a discovery of a deviceType device must carry for
// props to hold it (inventory.Discovery.Binding), or the empty string for a
// type whose discovery is not bound. Onboarding reaches it through the
// device's DiscoveryBinding; it is exported for a caller that builds a
// discovery from properties alone.
func Binding(deviceType string, props inventory.Properties) string {
	keys, ok := bound[deviceType]
	if !ok {
		return ""
	}
	return inventory.BindingDigest(props, keys)
}

// Types returns the generic device types, sorted.
func Types() []string {
	return []string{TypeGRPC, TypeHTTP, TypeNetconf, TypeSSH}
}

// declared returns baseline plus the capabilities rec's discovery records,
// refusing a discovery that is malformed or names a capability deviceType
// may not be granted. rec.Capabilities is deliberately not read.
//
// A bound type's discovery whose binding does not match rec grants
// nothing, and declared returns why as stale: the device keeps its
// baseline, loads, and says what to do, rather than failing to load.
func declared(deviceType string, rec record.Record, baseline []capability.Name) (caps []capability.Name, stale string, err error) {
	props := inventory.NewProperties(rec.Properties)
	d, ok, err := inventory.DiscoveryFrom(props)
	if err != nil {
		return nil, "", fmt.Errorf("%s %s: %w", deviceType, rec.Name, err)
	}
	if !ok {
		return baseline, "", nil
	}
	allowed := discoverable[deviceType]
	for _, c := range d.Capabilities {
		if !slices.Contains(allowed, c) {
			return nil, "", fmt.Errorf("%s %s: its discovery grants %s, which onboarding may not grant this type", deviceType, rec.Name, c)
		}
	}
	// Checked after the capabilities, so a corrupted record is still
	// refused rather than loaded with its baseline.
	if want := Binding(deviceType, props); want != "" && d.Binding != want {
		reason := fmt.Sprintf("device %s's discovery was made against other values of %s; run `pleiades onboard %s` again",
			rec.Name, strings.Join(bound[deviceType], ", "), rec.Name)
		if d.Binding == "" {
			reason = fmt.Sprintf("device %s's discovery predates binding a discovery to the address it probed; run `pleiades onboard %s` again",
				rec.Name, rec.Name)
		}
		return baseline, reason, nil
	}
	return policy.UnionSlices(baseline, d.Capabilities), "", nil
}

// discoveredFact returns one text fact from props' recorded discovery, or
// the empty string. It is how a generic type's accessors read what
// onboarding learned, such as generic_ssh's kernel release.
func discoveredFact(props inventory.Properties, key string) string {
	d, ok, err := inventory.DiscoveryFrom(props)
	if err != nil || !ok {
		return ""
	}
	s, _ := d.Facts[key].(string)
	return s
}

func init() {
	for _, t := range Types() {
		record.RegisterOnboardedType(t)
	}
	record.RegisterType(TypeSSH, NewSSH)
	record.RegisterType(TypeNetconf, NewNetconf)
	record.RegisterType(TypeHTTP, NewHTTP)
	record.RegisterType(TypeGRPC, NewGRPC)
}

// deviceTLS reads rec's TLS settings. Where the connection has no TLS (an
// http:// base URL, a plaintext gRPC target), any TLS setting is refused
// rather than ignored: it would describe protection the device does not
// get.
func deviceTLS(rec record.Record, usesTLS bool) (devicetls.Settings, error) {
	if !usesTLS {
		for _, key := range devicetls.Properties() {
			if _, present := rec.Properties[key]; present {
				return devicetls.Settings{}, fmt.Errorf("property %s applies to a TLS connection, and this device's has none", key)
			}
		}
	}
	return devicetls.Parse(inventory.NewProperties(rec.Properties))
}

// strictBool reads key as a boolean, false when absent; anything else is
// refused, so a setting that weakens nothing by accident reads as true.
func strictBool(props map[string]inventory.PropertyValue, key string) (bool, error) {
	v, present := props[key]
	if !present {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("property %s must be true or false", key)
	}
	return b, nil
}
