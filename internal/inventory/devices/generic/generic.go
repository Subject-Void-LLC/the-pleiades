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

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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

// Types returns the generic device types, sorted.
func Types() []string {
	return []string{TypeGRPC, TypeHTTP, TypeNetconf, TypeSSH}
}

// declared returns baseline plus the capabilities rec's discovery records,
// refusing a discovery that is malformed or names a capability deviceType
// may not be granted. rec.Capabilities is deliberately not read.
func declared(deviceType string, rec record.Record, baseline []capability.Name) ([]capability.Name, error) {
	d, ok, err := inventory.DiscoveryFrom(inventory.NewProperties(rec.Properties))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", deviceType, rec.Name, err)
	}
	if !ok {
		return baseline, nil
	}
	allowed := discoverable[deviceType]
	for _, c := range d.Capabilities {
		if !slices.Contains(allowed, c) {
			return nil, fmt.Errorf("%s %s: its discovery grants %s, which onboarding may not grant this type", deviceType, rec.Name, c)
		}
	}
	return policy.UnionSlices(baseline, d.Capabilities), nil
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
