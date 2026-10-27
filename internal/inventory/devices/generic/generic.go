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

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The generic device types.
const (
	TypeSSH     = "generic_ssh"
	TypeNetconf = "generic_netconf"
	TypeHTTP    = "generic_http"
	TypeGRPC    = "generic_grpc"
)

// rules is, per type, what onboarding may grant it and which properties a
// discovery holds for (record.DiscoveryRules), registered in init.
//
// generic_http's discovery is bound because its stored credential goes to
// its base URL, and TLS verification against a public authority would admit
// any host with a valid certificate for its own name. generic_grpc sends no
// credential, and is bound because a discovery made against one server says
// nothing about another. The SSH-based types are not bound: a strict
// host-key check already refuses a repointed host before any credential is
// sent.
var rules = map[string]record.DiscoveryRules{
	TypeSSH: {Grants: []capability.Name{
		capability.NameShellExec,
		capability.NameLinux,
		capability.NamePOSIXFileSystem,
		capability.NameFactGatherer,
		capability.NameSystemd,
		capability.NameFirewalld,
		capability.NameApt,
		capability.NameDnf,
		capability.NamePosixAccount,
		// The container engines an SSH probe finds (engines.SSHGrants).
		capability.NameContainerEngine,
		capability.NameDocker,
		capability.NamePodman,
		capability.NameEngineSocket,
		capability.NameEngineStdio,
		// What the probe finds answering as the connecting account: FRR's
		// vtysh and Open vSwitch's ovs-vsctl.
		capability.NameFRR,
		// VMware Fusion, found answering as the connecting account on a Mac.
		capability.NameVMwareFusion,
		capability.NameOpenVSwitch,
	}},
	TypeNetconf: {Grants: []capability.Name{capability.NameNetconf}},
	TypeHTTP: {
		Grants: []capability.Name{capability.NameHTTPAPI},
		Bound:  append([]string{BaseURLProperty, HTTPAuthProperty, httpapi.AllowPlaintextCredentialsProperty}, devicetls.Properties()...),
	},
	TypeGRPC: {
		Grants: []capability.Name{capability.NameGRPC},
		Bound:  append([]string{GRPCTargetProperty, GRPCPlaintextProperty}, devicetls.Properties()...),
	},
}

// Discoverable returns the capabilities onboarding may grant a device of
// deviceType, generic or not (record.Discoverable), or nil for a type
// onboarding grants nothing.
func Discoverable(deviceType string) []capability.Name {
	return record.Discoverable(deviceType)
}

// Binding is the digest a discovery of a deviceType device must carry for
// props to hold it (inventory.Discovery.Binding), or the empty string for a
// type whose discovery is not bound (record.DiscoveryBinding). Onboarding
// reaches it through the device's DiscoveryBinding; it is exported for a
// caller that builds a discovery from properties alone.
func Binding(deviceType string, props inventory.Properties) string {
	return record.DiscoveryBinding(deviceType, props)
}

// Types returns the generic device types, sorted.
func Types() []string {
	return []string{TypeGRPC, TypeHTTP, TypeNetconf, TypeSSH}
}

func init() {
	for _, t := range Types() {
		record.RegisterOnboardedType(t)
		record.RegisterDiscoveryRules(t, rules[t])
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
