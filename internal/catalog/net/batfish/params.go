// Package batfish: reading net.batfish.check's parameters, every one
// refused before Batfish is reached when it cannot mean what it says.
package batfish

import (
	"fmt"
	"regexp"
	"strings"

	bf "github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The configuration formats taken. Batfish reads a Cisco configuration as
// it is; FRR only in Cumulus's layout (pkg/netcfg).
const (
	formatFRR      = "frr"
	formatCiscoIOS = "cisco_ios"
)

// maxConfig bounds one configuration read from the project.
const maxConfig = 4 << 20

// nodeName is what a node name may be.
var nodeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// interfaceRef is node[interface].
var interfaceRef = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]{0,62})\[([A-Za-z0-9][A-Za-z0-9._:/-]{0,63})\]$`)

// request is the task, read.
type request struct {
	devices       []deviceConfig
	assertions    []assertion
	scenarios     []scenario
	accept        map[string]string
	acceptFeature map[string]string
	// expect names the devices the lab holds that the task means its answer
	// to stand for (expect_devices); each one not in devices is reported as
	// left out of the model.
	expect []string
	// networkPrefix begins the name of the Batfish network the task makes
	// (bf.NewNetworkNameFor), bf.DefaultNetworkPrefix unless network names
	// another.
	networkPrefix string
}

// deviceConfig is one device's configuration.
type deviceConfig struct {
	name, format, config string
}

// scenario is a fork of the snapshot and what must hold of it.
type scenario struct {
	name       string
	interfaces []bf.Interface
	nodes      []string
	assertions []assertion
}

// The assertion kinds.
const (
	kindOSPF        = "ospf_established"
	kindBGP         = "bgp_established"
	kindRoute       = "route"
	kindNoRoute     = "no_route"
	kindReach       = "reach"
	kindRoutesMatch = "routes_match"
	kindQuestion    = "question"
)

// assertion is one thing that must hold of a snapshot.
type assertion struct {
	kind                   string
	node, prefix, protocol string
	// vrf narrows route, no_route and routes_match to one VRF; empty is
	// every VRF the model has.
	vrf              string
	nextHops         []string
	from, dst        string
	delivered        bool
	acceptedBy       string
	through, avoids  []string
	liveRoutes       map[string]any
	ignore           []string
	question         string
	variables        map[string]any
	where            map[string]any
	minRows, maxRows int
}

// paramNetwork names the parameter that begins the Batfish network's name.
const paramNetwork = "network"

// paramExpectDevices names the parameter listing the devices a check is
// meant to cover, so one it was given no configuration for is said aloud.
const paramExpectDevices = "expect_devices"

// readExpectDevices reads expect_devices: node names, each once. Absent is
// none, which keeps a task that never set it answering exactly as before.
func readExpectDevices(params map[string]any) ([]string, error) {
	names, _, err := sdk.StringSlice(params, paramExpectDevices)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", checkFQCN, paramExpectDevices, err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case !nodeName.MatchString(n):
			return nil, fmt.Errorf("%s: %s: %q is not a node name", checkFQCN, paramExpectDevices, n)
		case seen[strings.ToLower(n)]:
			return nil, fmt.Errorf("%s: %s names %s twice", checkFQCN, paramExpectDevices, n)
		}
		seen[strings.ToLower(n)] = true
	}
	return names, nil
}

// readNetworkPrefix reads network: absent or empty is bf.DefaultNetworkPrefix,
// and anything else must be a prefix bf.ValidNetworkPrefix takes, refused
// before Batfish is reached.
func readNetworkPrefix(params map[string]any) (string, error) {
	v, ok := params[paramNetwork]
	if !ok || v == nil {
		return bf.DefaultNetworkPrefix, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string, not %T", checkFQCN, paramNetwork, v)
	}
	if s = strings.TrimSpace(s); s == "" {
		return bf.DefaultNetworkPrefix, nil
	}
	if err := bf.ValidNetworkPrefix(s); err != nil {
		return "", fmt.Errorf("%s: %s: %w", checkFQCN, paramNetwork, err)
	}
	return s, nil
}

// readParams reads the task.
func readParams(rc sdk.RunbookContext, params map[string]any) (request, error) {
	var req request
	var err error
	for k := range params {
		switch k {
		case "devices", "assertions", "scenarios", "accept_unrecognized", paramNetwork, "target", "within", paramExpectDevices:
		default:
			return req, fmt.Errorf("%s: no parameter %q", checkFQCN, k)
		}
	}
	if req.networkPrefix, err = readNetworkPrefix(params); err != nil {
		return req, err
	}
	devices, err := mapList(params, "devices")
	if err != nil {
		return req, err
	}
	if len(devices) == 0 {
		return req, fmt.Errorf("%s: devices must name at least one configuration", checkFQCN)
	}
	seen := map[string]bool{}
	for _, d := range devices {
		dc, err := readDevice(rc, d)
		if err != nil {
			return req, err
		}
		if seen[strings.ToLower(dc.name)] {
			return req, fmt.Errorf("%s: devices names %s twice", checkFQCN, dc.name)
		}
		seen[strings.ToLower(dc.name)] = true
		req.devices = append(req.devices, dc)
	}
	if req.expect, err = readExpectDevices(params); err != nil {
		return req, err
	}
	if req.assertions, err = readAssertions(params, "assertions", seen); err != nil {
		return req, err
	}
	scenarios, err := mapList(params, "scenarios")
	if err != nil {
		return req, err
	}
	for i, s := range scenarios {
		sc, err := readScenario(s, seen)
		if err != nil {
			return req, fmt.Errorf("%s: scenarios[%d]: %w", checkFQCN, i, err)
		}
		req.scenarios = append(req.scenarios, sc)
	}
	if len(req.assertions) == 0 && len(req.scenarios) == 0 {
		return req, fmt.Errorf("%s: give assertions or scenarios: a check that asks nothing passes everything", checkFQCN)
	}
	accepts, err := mapList(params, "accept_unrecognized")
	if err != nil {
		return req, err
	}
	req.accept, req.acceptFeature = map[string]string{}, map[string]string{}
	for i, a := range accepts {
		text, _ := a["text"].(string)
		feature, _ := a["feature"].(string)
		reason, _ := a["reason"].(string)
		switch {
		case len(a) != 2 || strings.TrimSpace(reason) == "" || (strings.TrimSpace(text) == "") == (feature == ""):
			return req, fmt.Errorf("%s: accept_unrecognized[%d] must be a text or a feature, and a reason", checkFQCN, i)
		case feature != "" && coverageWhy(feature) == "":
			return req, fmt.Errorf("%s: accept_unrecognized[%d]: no unmodelled feature %q", checkFQCN, i, feature)
		case feature != "":
			req.acceptFeature[feature] = strings.TrimSpace(reason)
		default:
			req.accept[strings.TrimSpace(text)] = strings.TrimSpace(reason)
		}
	}
	return req, nil
}

// readDevice reads one devices entry.
func readDevice(rc sdk.RunbookContext, d map[string]any) (deviceConfig, error) {
	var dc deviceConfig
	for k := range d {
		if k != "name" && k != "format" && k != "config" && k != "src" {
			return dc, fmt.Errorf("%s: a devices entry has no %q", checkFQCN, k)
		}
	}
	dc.name, _ = d["name"].(string)
	if !nodeName.MatchString(dc.name) {
		return dc, fmt.Errorf("%s: a devices entry's name must be a node name, got %q", checkFQCN, dc.name)
	}
	dc.format, _ = d["format"].(string)
	if dc.format == "" {
		dc.format = formatFRR
	}
	if dc.format != formatFRR && dc.format != formatCiscoIOS {
		return dc, fmt.Errorf("%s: %s's format must be %s or %s, got %q", checkFQCN, dc.name, formatFRR, formatCiscoIOS, dc.format)
	}
	config, hasConfig := d["config"].(string)
	src, hasSrc := d["src"].(string)
	switch {
	case hasConfig == hasSrc:
		return dc, fmt.Errorf("%s: %s needs config or src, not both and not neither", checkFQCN, dc.name)
	case hasSrc:
		files, ok := rc.(sdk.ProjectFiles)
		if !ok {
			return dc, fmt.Errorf("%s: %s's src: %w", checkFQCN, dc.name, sdk.ErrNoProjectFiles)
		}
		b, err := files.ReadProjectFile(src, maxConfig)
		if err != nil {
			return dc, fmt.Errorf("%s: %s's src: %w", checkFQCN, dc.name, err)
		}
		config = string(b)
	}
	if strings.TrimSpace(config) == "" {
		return dc, fmt.Errorf("%s: %s's configuration is empty", checkFQCN, dc.name)
	}
	dc.config = config
	if dc.format == formatCiscoIOS && !strings.EqualFold(netcfg.Hostname(config), dc.name) {
		return dc, fmt.Errorf("%s: %s's configuration names its hostname %q; Batfish names the node by it, so name must match", checkFQCN, dc.name, netcfg.Hostname(config))
	}
	return dc, nil
}

// readScenario reads one scenario.
func readScenario(s map[string]any, nodes map[string]bool) (scenario, error) {
	var sc scenario
	for k := range s {
		switch k {
		case "name", "down_interfaces", "down_nodes", "assertions":
		default:
			return sc, fmt.Errorf("no %q", k)
		}
	}
	sc.name, _ = s["name"].(string)
	if strings.TrimSpace(sc.name) == "" {
		return sc, fmt.Errorf("a scenario needs a name")
	}
	ifaces, _, err := sdk.StringSlice(s, "down_interfaces")
	if err != nil {
		return sc, err
	}
	for _, ref := range ifaces {
		m := interfaceRef.FindStringSubmatch(ref)
		if m == nil || !nodes[strings.ToLower(m[1])] {
			return sc, fmt.Errorf("down_interfaces: %q is not node[interface] for a device given", ref)
		}
		sc.interfaces = append(sc.interfaces, bf.Interface{Hostname: strings.ToLower(m[1]), Interface: m[2]})
	}
	down, _, err := sdk.StringSlice(s, "down_nodes")
	if err != nil {
		return sc, err
	}
	for _, n := range down {
		if !nodes[strings.ToLower(n)] {
			return sc, fmt.Errorf("down_nodes: %q is not a device given", n)
		}
		sc.nodes = append(sc.nodes, strings.ToLower(n))
	}
	if len(sc.interfaces)+len(sc.nodes) == 0 {
		return sc, fmt.Errorf("%s takes nothing down", sc.name)
	}
	if sc.assertions, err = readAssertions(s, "assertions", nodes); err != nil {
		return sc, err
	}
	if len(sc.assertions) == 0 {
		return sc, fmt.Errorf("%s has no assertions", sc.name)
	}
	return sc, nil
}

// mapList reads params[key] as a list of maps; absent is empty.
func mapList(params map[string]any, key string) ([]map[string]any, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be a list", checkFQCN, key)
	}
	out := make([]map[string]any, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] must be a map", checkFQCN, key, i)
		}
		out = append(out, m)
	}
	return out, nil
}
