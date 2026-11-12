// FuzzReadParams holds net.batfish.check's parameter reader to its refusals
// on input no table lists: the parameters arrive as YAML, as a runbook's do,
// and whatever readParams accepts must be a request Batfish can be asked.
package batfish

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	bf "github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
)

// quoted is a Go-quoted string, as a refusal's %q echoes the input.
var quoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

func FuzzReadParams(f *testing.F) {
	for _, seed := range []string{
		"devices: [{name: r1, config: \"hostname r1\\n\"}]\nassertions: [{ospf_established: true}]\n",
		"devices: [{name: r1, format: cisco_ios, config: \"hostname r1\\n\"}]\nassertions: [{route: {node: r1, prefix: 10.0.0.0/8, vrf: INET}}]\n",
		"devices: [{name: r1, config: x}]\nscenarios: [{name: s, down_interfaces: [\"R1[eth1]\"], assertions: [{reach: {from: \"r1[eth3]\", dst: 10.0.0.1, avoids: [R1]}}]}]\n",
		"devices: [{name: r1, config: x}]\nassertions: [{reach: {from: \"@enter(r1[eth1])\", dst: 10.0.0.1}}]\n",
		"devices: [{name: r1, config: x}]\nassertions: [{ospf_established: true}]\naccept_unrecognized: [{feature: glbp, reason: measured}, {text: exit, reason: r}]\n",
		"devices: [{name: r1, config: x}]\nassertions: [{question: {name: routes, where: {Node: r1}, min_rows: 1, max_rows: 3}}]\nexpect_devices: [r1, sw1]\nnetwork: lab-1\n",
		"devices: [{name: ../r1, config: x}]\nassertions: [{ospf_established: true}]\n",
		"devices: [{name: r1, config: x}, {name: R1, config: y}]\nassertions: [{ospf_established: true}]\n",
		"devices: [{name: r1, config: x}]\nassertions: [{ospf_established: true}]\n'net.batfish.check: x': 1\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		var params map[string]any
		if yaml.Unmarshal([]byte(text), &params) != nil || params == nil {
			return
		}
		req, err := readParams(nil, params)
		again, errAgain := readParams(nil, params)
		if (err == nil) != (errAgain == nil) || (err == nil && !reflect.DeepEqual(req, again)) {
			t.Fatalf("readParams read the same parameters two ways: %v and %v", err, errAgain)
		}
		if err != nil {
			// The method's name opens the refusal and appears nowhere else but
			// inside a quoted echo of the input, which may hold anything.
			msg := err.Error()
			if !strings.HasPrefix(msg, checkFQCN+": ") || strings.Contains(quoted.ReplaceAllString(strings.TrimPrefix(msg, checkFQCN+": "), `""`), checkFQCN) {
				t.Fatalf("a refusal does not name the method once: %v", err)
			}
			return
		}

		if len(req.devices) == 0 {
			t.Fatalf("accepted a check with no devices: %v", params)
		}
		if len(req.assertions) == 0 && len(req.scenarios) == 0 {
			t.Fatalf("accepted a check that asks nothing: %v", params)
		}
		seen := map[string]bool{}
		for _, d := range req.devices {
			if !nodeName.MatchString(d.name) || seen[strings.ToLower(d.name)] {
				t.Fatalf("accepted the device name %q (twice: %v)", d.name, seen[strings.ToLower(d.name)])
			}
			seen[strings.ToLower(d.name)] = true
			if d.format != formatFRR && d.format != formatCiscoIOS {
				t.Fatalf("accepted the format %q", d.format)
			}
			if strings.TrimSpace(d.config) == "" {
				t.Fatalf("accepted %s with an empty configuration", d.name)
			}
		}
		for _, name := range req.expect {
			if !nodeName.MatchString(name) {
				t.Fatalf("accepted the expected device %q", name)
			}
		}
		for feature, reason := range req.acceptFeature {
			if coverageWhy(feature) == "" || strings.TrimSpace(reason) == "" {
				t.Fatalf("accepted the feature %q with the reason %q", feature, reason)
			}
		}
		for text, reason := range req.accept {
			if strings.TrimSpace(text) == "" || strings.TrimSpace(reason) == "" {
				t.Fatalf("accepted the text %q with the reason %q", text, reason)
			}
		}
		if err := bf.ValidNetworkPrefix(req.networkPrefix); err != nil {
			t.Fatalf("accepted the network %q: %v", req.networkPrefix, err)
		}
	})
}

// TestReadParams_NamesTheMethodOnce: a refusal of an assertion names the
// method once, at the top level and under a scenario alike (found by
// FuzzReadParams: the top level named it not at all, a scenario's list twice).
func TestReadParams_NamesTheMethodOnce(t *testing.T) {
	dev := []any{map[string]any{"name": "r1", "config": "x"}}
	for name, p := range map[string]map[string]any{
		"an assertion that is no map":       {"devices": dev, "assertions": []any{map[string]any{"route": "r1"}}},
		"an assertion of two kinds":         {"devices": dev, "assertions": []any{map[string]any{"ospf_established": true, "bgp_established": true}}},
		"assertions that are no list":       {"devices": dev, "assertions": "ospf"},
		"a scenario's assertions no list":   {"devices": dev, "scenarios": []any{map[string]any{"name": "s", "down_nodes": []any{"r1"}, "assertions": "ospf"}}},
		"a scenario's assertion of no kind": {"devices": dev, "scenarios": []any{map[string]any{"name": "s", "down_nodes": []any{"r1"}, "assertions": []any{map[string]any{"ospf_up": true}}}}},
	} {
		_, err := readParams(nil, p)
		if err == nil || !strings.HasPrefix(err.Error(), checkFQCN+": ") || strings.Count(err.Error(), checkFQCN) != 1 {
			t.Errorf("%s: %v, want one refusal naming %s once", name, err, checkFQCN)
		}
	}
}
