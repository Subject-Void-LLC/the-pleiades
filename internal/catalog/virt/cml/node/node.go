// Package node implements the virt.cml.node.* methods: a node made in a
// CML lab, optionally standing for an inventory device whose login and
// address it is seeded with, and a node removed. This file holds the day-0
// configuration a seeded node boots with, and the checks that keep every
// value written into it a value and never a configuration line of its own.
package node

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The parameter names.
const (
	paramLab        = "lab"
	paramNode       = "node"
	paramLabel      = "label"
	paramDefinition = "definition"
	paramDevice     = "device"
	paramInterface  = "management_interface"
	paramPrefix     = "management_prefix"
	paramGateway    = "management_gateway"
	paramConfig     = "configuration"
	paramX          = "x"
	paramY          = "y"
)

// mgmtVRF is the VRF a seeded node's management interface sits in, as the
// CML sandbox's own nodes are built, so lab routing never carries it.
const mgmtVRF = "Mgmt-intf"

// hostname is what IOS takes as a hostname: a letter, then letters, digits
// and hyphens, at most 63 characters. A label written as one must be one.
var hostname = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)

// interfaceName is an IOS interface name, as Ethernet0/0 or
// GigabitEthernet1.
var interfaceName = regexp.MustCompile(`^[A-Za-z][A-Za-z-]*[0-9][0-9/.:]*$`)

// iosDefinitions are the node definitions a seed writes an IOS
// configuration for: CML's IOS and IOS XE images.
var iosDefinitions = map[string]bool{
	"iol-xe": true, "ioll2-xe": true, "iosv": true, "iosvl2": true, "csr1000v": true,
	"cat8000v": true, "cat9000v-q200": true, "cat9000v-uadp": true,
}

// seed is what a seeded node's day-0 configuration is written from.
type seed struct {
	label, username, secret, enableSecret string
	host                                  netip.Addr
	iface                                 string
	prefix                                int
	gateway                               netip.Addr
	extra                                 string
}

// readSeed reads a seed from the secrets the engine handed the method and
// the task's parameters, refusing any value that would not be one value in
// the configuration it is written into.
func readSeed(secrets map[string]string, definition, label, iface string, prefix int, gateway, extra string) (seed, error) {
	s := seed{label: label, username: secrets[wire.SecretSeedUsername], secret: secrets[wire.SecretSeedIOSSecret],
		enableSecret: secrets[wire.SecretSeedIOSEnableSecret], iface: iface, prefix: prefix, extra: extra}
	switch {
	case !iosDefinitions[definition]:
		return seed{}, fmt.Errorf("%s writes an IOS configuration, and %q is not an IOS or IOS XE definition", paramDevice, definition)
	case !hostname.MatchString(label):
		return seed{}, fmt.Errorf("%s %q becomes the node's hostname, which is a letter then letters, digits and hyphens, at most 63", paramLabel, label)
	case s.username == "" || s.secret == "":
		return seed{}, fmt.Errorf("the engine handed no IOS login to seed")
	case strings.ContainsAny(s.username, " \t\r\n"):
		return seed{}, fmt.Errorf("the device's username %q cannot be written into an IOS configuration", s.username)
	}
	host := secrets[wire.SecretSeedHost]
	if host == "" {
		// A device with no address of its own, reached through a console
		// server: no management interface is written.
		return s, nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Is4() {
		return seed{}, fmt.Errorf("the device's host %q is not an IPv4 address, which a management interface needs", host)
	}
	gw, err := netip.ParseAddr(gateway)
	switch {
	case gateway == "":
		return seed{}, fmt.Errorf("%s is required to give the node a management address", paramGateway)
	case err != nil || !gw.Is4():
		return seed{}, fmt.Errorf("%s %q is not an IPv4 address", paramGateway, gateway)
	case !interfaceName.MatchString(iface):
		return seed{}, fmt.Errorf("%s %q is not an interface name", paramInterface, iface)
	case prefix < 1 || prefix > 30:
		return seed{}, fmt.Errorf("%s must be 1 to 30, not %d", paramPrefix, prefix)
	}
	if !netip.PrefixFrom(addr, prefix).Masked().Contains(gw) {
		return seed{}, fmt.Errorf("%s %s is not on %s/%d", paramGateway, gw, addr, prefix)
	}
	s.host, s.gateway = addr, gw
	return s, nil
}

// mask is prefix as a dotted IPv4 netmask.
func mask(prefix int) string {
	m := uint32(0xffffffff) << (32 - prefix)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&0xff, m>>8&0xff, m&0xff)
}

// config is the node's day-0 configuration: its hostname, the login as
// type 9 secrets, SSH, and the management address in its own VRF, then the
// task's own lines.
func (s seed) config() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("hostname %s", s.label)
	line("no ip domain lookup")
	line("ip domain name pleiades.lab")
	line("username %s privilege 15 secret 9 %s", s.username, s.secret)
	if s.enableSecret != "" {
		line("enable secret 9 %s", s.enableSecret)
	}
	if s.host.IsValid() {
		line("vrf definition %s", mgmtVRF)
		line(" address-family ipv4")
		line(" exit-address-family")
		line("interface %s", s.iface)
		line(" vrf forwarding %s", mgmtVRF)
		line(" ip address %s %s", s.host, mask(s.prefix))
		line(" no shutdown")
		line("ip route vrf %s 0.0.0.0 0.0.0.0 %s", mgmtVRF, s.gateway)
	}
	line("crypto key generate rsa modulus 2048")
	line("ip ssh version 2")
	line("line con 0")
	line(" login local")
	line("line vty 0 4")
	line(" login local")
	line(" transport input ssh")
	if extra := strings.TrimSpace(s.extra); extra != "" {
		b.WriteString(extra + "\n")
	}
	line("end")
	return b.String()
}
