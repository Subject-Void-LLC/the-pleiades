// The parts of container.info's answer that need more than a field copy:
// published ports, attached networks, and bounded engine text.
package container

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"unicode/utf8"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// maxStateError bounds the engine's last start error in container.info's
// answer: one sentence from the engine, never a log.
const maxStateError = 1024

// publishedPorts is each port the container publishes, as docker run
// writes one (host_ip:host_port:container_port/proto, the host address left
// out when it is every address), sorted so two reads compare equal.
func publishedPorts(bindings network.PortMap) []string {
	out := []string{}
	for port, bs := range bindings {
		for _, b := range bs {
			host := b.HostPort
			if b.HostIP.IsValid() && !b.HostIP.IsUnspecified() {
				addr := b.HostIP.String()
				// An IPv6 address is bracketed, as docker writes one, so the
				// colons that separate the fields stay unambiguous.
				if b.HostIP.Is6() {
					addr = "[" + addr + "]"
				}
				host = addr + ":" + host
			}
			out = append(out, fmt.Sprintf("%s:%d/%s", host, port.Num(), port.Proto()))
		}
	}
	slices.Sort(out)
	return out
}

// attachedNetworks is each network the container is attached to, sorted
// by name: its name, the engine's ID for it, and the container's address,
// gateway, MAC address and aliases on it, each left out when the engine
// gives none (a stopped container has no address).
func attachedNetworks(c moby.InspectResponse) []any {
	out := []any{}
	if c.NetworkSettings == nil {
		return out
	}
	names := make([]string, 0, len(c.NetworkSettings.Networks))
	for name := range c.NetworkSettings.Networks {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		ep := c.NetworkSettings.Networks[name]
		entry := map[string]any{"name": name}
		if ep == nil {
			out = append(out, entry)
			continue
		}
		entry["network_id"] = ep.NetworkID
		if ep.IPAddress.IsValid() {
			entry["ip_address"] = ep.IPAddress.String()
		}
		if ep.Gateway.IsValid() {
			entry["gateway"] = ep.Gateway.String()
		}
		if len(ep.MacAddress) > 0 {
			entry["mac_address"] = net.HardwareAddr(ep.MacAddress).String()
		}
		if len(ep.Aliases) > 0 {
			entry["aliases"] = slices.Clone(ep.Aliases)
		}
		out = append(out, entry)
	}
	return out
}

// nonNilStrings is list, or an empty list for nil, so a result always
// holds a list where its documentation says one.
func nonNilStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return slices.Clone(list)
}

// boundedText is s cut to at most limit bytes on a character boundary,
// marked with "..." when cut.
func boundedText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRightFunc(s[:cut], func(r rune) bool { return r == ' ' }) + "..."
}
