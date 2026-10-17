// A network's declared settings: parsed from a task, sent as a create
// request, compared with a live network, and read back off one for an undo.
package network

import (
	"fmt"
	"maps"
	"net/netip"
	"regexp"
	"slices"
	"strconv"

	moby "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The settings' parameters.
const (
	paramDriver   = "driver"
	paramInternal = "internal"
	paramIPv6     = "enable_ipv6"
	paramSubnet   = "subnet"
	paramGateway  = "gateway"
	paramIPRange  = "ip_range"
	paramMTU      = "mtu"
	paramParent   = "parent"
	paramLabels   = "labels"
)

// The drivers a task may name: those Docker and Podman both have on a
// single host. Overlay needs a swarm and is not offered.
const (
	driverBridge  = "bridge"
	driverMacvlan = "macvlan"
	driverIPvlan  = "ipvlan"
)

// drivers is the allowlist of driver names.
var drivers = []string{driverBridge, driverMacvlan, driverIPvlan}

// The driver options a setting becomes, the only ones a task can set.
const (
	optionMTU    = "com.docker.network.driver.mtu"
	optionParent = "parent"
)

// mirroredOptions are options an engine adds to a network's report that
// mirror a field already compared, so they say nothing an undo misses.
var mirroredOptions = []string{"com.docker.network.enable_ipv4", "com.docker.network.enable_ipv6"}

// The bounds of an MTU: IPv4's minimum to the largest frame size.
const (
	minMTU = 68
	maxMTU = 65535
)

// interfaceName is a host network interface's name, as the kernel allows
// one (at most 15 bytes), for a macvlan or ipvlan parent.
var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$`)

// settings are a network's declared settings. A zero subnet, gateway,
// range, MTU or parent is one the task leaves to the engine.
type settings struct {
	Driver   string
	Internal bool
	IPv6     bool
	Subnet   netip.Prefix
	Gateway  netip.Addr
	IPRange  netip.Prefix
	MTU      int
	Parent   string
	Labels   map[string]string
}

// parseSettings reads a network's settings from a task, refusing what an
// engine would read differently or reject later.
func parseSettings(params map[string]any) (settings, error) {
	var s settings
	var err error
	s.Driver = sdk.StringParam(params, paramDriver)
	if s.Driver == "" {
		s.Driver = driverBridge
	}
	if !slices.Contains(drivers, s.Driver) {
		return s, fmt.Errorf("%s: %q is not one of %v", paramDriver, s.Driver, drivers)
	}
	if s.Internal, err = sdk.BoolParamOr(params, paramInternal, false); err != nil {
		return s, err
	}
	if s.IPv6, err = sdk.BoolParamOr(params, paramIPv6, false); err != nil {
		return s, err
	}
	if raw := sdk.StringParam(params, paramSubnet); raw != "" {
		if s.Subnet, err = netip.ParsePrefix(raw); err != nil || s.Subnet != s.Subnet.Masked() {
			return s, fmt.Errorf("%s: %q is not a network address with its prefix length (10.10.0.0/24)", paramSubnet, raw)
		}
		if s.Subnet.Addr().Is6() && !s.IPv6 {
			return s, fmt.Errorf("%s: %s is IPv6; set %s", paramSubnet, raw, paramIPv6)
		}
	}
	if raw := sdk.StringParam(params, paramGateway); raw != "" {
		if s.Gateway, err = netip.ParseAddr(raw); err != nil {
			return s, fmt.Errorf("%s: %q is not an address", paramGateway, raw)
		}
		if !s.Subnet.IsValid() || !s.Subnet.Contains(s.Gateway) {
			return s, fmt.Errorf("%s: %s is not inside the declared %s", paramGateway, raw, paramSubnet)
		}
	}
	if raw := sdk.StringParam(params, paramIPRange); raw != "" {
		if s.IPRange, err = netip.ParsePrefix(raw); err != nil || s.IPRange != s.IPRange.Masked() {
			return s, fmt.Errorf("%s: %q is not a network address with its prefix length", paramIPRange, raw)
		}
		if !s.Subnet.IsValid() || !s.Subnet.Contains(s.IPRange.Addr()) || s.IPRange.Bits() < s.Subnet.Bits() {
			return s, fmt.Errorf("%s: %s is not inside the declared %s", paramIPRange, raw, paramSubnet)
		}
	}
	mtu, set, err := sdk.IntParam(params, paramMTU)
	if err != nil {
		return s, err
	}
	if set {
		if s.Driver != driverBridge {
			return s, fmt.Errorf("%s: a %s network takes its parent interface's MTU", paramMTU, s.Driver)
		}
		if mtu < minMTU || mtu > maxMTU {
			return s, fmt.Errorf("%s: %d is not from %d to %d", paramMTU, mtu, minMTU, maxMTU)
		}
		s.MTU = mtu
	}
	if s.Parent = sdk.StringParam(params, paramParent); s.Parent != "" {
		if s.Driver == driverBridge {
			return s, fmt.Errorf("%s: a bridge network has no parent interface", paramParent)
		}
		if !interfaceName.MatchString(s.Parent) {
			return s, fmt.Errorf("%s: %q is not a network interface's name", paramParent, s.Parent)
		}
	}
	if s.Labels, err = containerengine.StringMapParam(params, paramLabels); err != nil {
		return s, err
	}
	return s, nil
}

// create is the request that makes a network with these settings.
func (s settings) create() client.NetworkCreateOptions {
	ipv6 := s.IPv6
	opts := client.NetworkCreateOptions{Driver: s.Driver, Internal: s.Internal, EnableIPv6: &ipv6, Labels: s.Labels}
	if s.Subnet.IsValid() {
		opts.IPAM = &moby.IPAM{Config: []moby.IPAMConfig{{Subnet: s.Subnet, Gateway: s.Gateway, IPRange: s.IPRange}}}
	}
	options := map[string]string{}
	if s.MTU != 0 {
		options[optionMTU] = strconv.Itoa(s.MTU)
	}
	if s.Parent != "" {
		options[optionParent] = s.Parent
	}
	if len(options) > 0 {
		opts.Options = options
	}
	return opts
}

// differences names each declared setting the live network does not have.
// A setting the task leaves to the engine is not compared, and labels the
// task does not name are allowed beside the declared ones.
func (s settings) differences(live moby.Inspect) []string {
	var d []string
	if live.Driver != s.Driver {
		d = append(d, fmt.Sprintf("%s: %s, the task names %s", paramDriver, live.Driver, s.Driver))
	}
	if live.Internal != s.Internal {
		d = append(d, fmt.Sprintf("%s: %t, the task names %t", paramInternal, live.Internal, s.Internal))
	}
	if live.EnableIPv6 != s.IPv6 {
		d = append(d, fmt.Sprintf("%s: %t, the task names %t", paramIPv6, live.EnableIPv6, s.IPv6))
	}
	if s.Subnet.IsValid() {
		cfg, ok := configFor(live, s.Subnet)
		switch {
		case !ok:
			d = append(d, fmt.Sprintf("%s: %s is not among the network's subnets %s", paramSubnet, s.Subnet, subnets(live)))
		default:
			if s.Gateway.IsValid() && cfg.Gateway != s.Gateway {
				d = append(d, fmt.Sprintf("%s: %s, the task names %s", paramGateway, cfg.Gateway, s.Gateway))
			}
			if s.IPRange.IsValid() && cfg.IPRange != s.IPRange {
				d = append(d, fmt.Sprintf("%s: %s, the task names %s", paramIPRange, cfg.IPRange, s.IPRange))
			}
		}
	}
	if s.MTU != 0 && live.Options[optionMTU] != strconv.Itoa(s.MTU) {
		d = append(d, fmt.Sprintf("%s: %q, the task names %d", paramMTU, live.Options[optionMTU], s.MTU))
	}
	if s.Parent != "" && live.Options[optionParent] != s.Parent {
		d = append(d, fmt.Sprintf("%s: %q, the task names %q", paramParent, live.Options[optionParent], s.Parent))
	}
	for _, k := range slices.Sorted(maps.Keys(s.Labels)) {
		if v, ok := live.Labels[k]; !ok || v != s.Labels[k] {
			d = append(d, fmt.Sprintf("%s: %s is %q, the task names %q", paramLabels, k, v, s.Labels[k]))
		}
	}
	return d
}

// configFor finds the live network's address configuration for subnet.
func configFor(live moby.Inspect, subnet netip.Prefix) (moby.IPAMConfig, bool) {
	for _, cfg := range live.IPAM.Config {
		if cfg.Subnet == subnet {
			return cfg, true
		}
	}
	return moby.IPAMConfig{}, false
}

// subnets lists the live network's subnets for a message.
func subnets(live moby.Inspect) []string {
	out := make([]string, 0, len(live.IPAM.Config))
	for _, cfg := range live.IPAM.Config {
		out = append(out, cfg.Subnet.String())
	}
	return out
}

// recreateParams are the parameters of a container.network.create that
// makes live again, and whether they fall short of it: an option no
// parameter sets, or an address configuration past the first, is not
// carried.
func recreateParams(live moby.Inspect) (map[string]any, bool) {
	p := map[string]any{
		paramName:     live.Name,
		paramDriver:   live.Driver,
		paramInternal: live.Internal,
		paramIPv6:     live.EnableIPv6,
	}
	partial := !slices.Contains(drivers, live.Driver)
	for i, cfg := range live.IPAM.Config {
		if i > 0 {
			partial = true
			break
		}
		if cfg.Subnet.IsValid() {
			p[paramSubnet] = cfg.Subnet.String()
		}
		if cfg.Gateway.IsValid() {
			p[paramGateway] = cfg.Gateway.String()
		}
		if cfg.IPRange.IsValid() {
			p[paramIPRange] = cfg.IPRange.String()
		}
	}
	for k, v := range live.Options {
		switch {
		case k == optionMTU:
			n, err := strconv.Atoi(v)
			if err != nil {
				partial = true
				continue
			}
			p[paramMTU] = n
		case k == optionParent:
			p[paramParent] = v
		case slices.Contains(mirroredOptions, k):
		default:
			partial = true
		}
	}
	if len(live.Labels) > 0 {
		labels := make(map[string]any, len(live.Labels))
		for k, v := range live.Labels {
			labels[k] = v
		}
		p[paramLabels] = labels
	}
	return p, partial
}
