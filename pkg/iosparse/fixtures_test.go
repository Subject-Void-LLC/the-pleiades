// Package iosparse_test: every ported parser held to every genieparser
// fixture for it, its skips each carrying a reason.
package iosparse_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// skipsFile lists fixtures a port cannot pass, each with its reason.
const skipsFile = "testdata/skips.json"

// fixtureArgs is a fixture's _arguments.json: the keyword arguments
// genieparser's harness passes the parser.
type fixtureArgs map[string]any

// s returns the argument named key as a string, "" when absent.
func (a fixtureArgs) s(key string) string {
	v, _ := a[key].(string)
	return v
}

// loadSkips reads testdata/skips.json, failing the test on any entry with
// no written reason.
func loadSkips(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(skipsFile)
	if err != nil {
		t.Fatalf("read %s: %v", skipsFile, err)
	}
	skips := map[string]string{}
	if err := json.Unmarshal(data, &skips); err != nil {
		t.Fatalf("parse %s: %v", skipsFile, err)
	}
	for k, reason := range skips {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s: %s has no reason", skipsFile, k)
		}
	}
	return skips
}

// holdToFixtures runs every one of genieparser's own fixtures for class,
// for each operating system copied, through Parse with the command render
// builds from the fixture's arguments. An equal fixture must parse to
// exactly its expected result, compared after both go through
// encoding/json, so an int and a float of the same value agree while a
// string "1" and a number 1 do not. An empty fixture must be refused as
// empty, as Genie refuses it.
func holdToFixtures(t *testing.T, class string, render func(fixtureArgs) string) {
	t.Helper()
	skips := loadSkips(t)
	systems, err := os.ReadDir(filepath.Join("testdata", class))
	if err != nil {
		t.Fatalf("no fixtures for %s: %v", class, err)
	}
	ran := 0
	for _, osDir := range systems {
		base := filepath.Join("testdata", class, osDir.Name())
		for _, name := range fixtureNames(t, filepath.Join(base, "equal")) {
			key := class + "/" + osDir.Name() + "/" + name
			ran++
			t.Run(osDir.Name()+"/"+name, func(t *testing.T) {
				checkEqual(t, filepath.Join(base, "equal"), name, render, skips[key])
			})
		}
		for _, name := range fixtureNames(t, filepath.Join(base, "empty")) {
			ran++
			t.Run(osDir.Name()+"/empty/"+name, func(t *testing.T) {
				checkEmpty(t, filepath.Join(base, "empty"), name, render)
			})
		}
	}
	if ran == 0 {
		t.Fatalf("%s: no fixtures ran", class)
	}
}

// fixtureNames lists the fixtures in dir by the name before _output.txt.
func fixtureNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), "_output.txt"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// readFixture returns a fixture's output and arguments.
func readFixture(t *testing.T, dir, name string) (string, fixtureArgs) {
	t.Helper()
	out, err := os.ReadFile(filepath.Join(dir, name+"_output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := fixtureArgs{}
	if data, err := os.ReadFile(filepath.Join(dir, name+"_arguments.json")); err == nil {
		if err := json.Unmarshal(data, &args); err != nil {
			t.Fatalf("arguments: %v", err)
		}
	}
	return string(out), args
}

// checkEqual holds one equal fixture: Parse must return its expected
// result. A fixture listed in skips.json must still fail, so an entry
// that has stopped being needed is noticed.
func checkEqual(t *testing.T, dir, name string, render func(fixtureArgs) string, skipReason string) {
	out, args := readFixture(t, dir, name)
	data, err := os.ReadFile(filepath.Join(dir, name+"_expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	command := render(args)
	_, parsed, err := iosparse.Parse(command, out)
	problem := ""
	if err != nil {
		problem = fmt.Sprintf("Parse(%q): %v", command, err)
	} else if diff := firstDiff("", normalize(t, parsed), want); diff != "" {
		problem = fmt.Sprintf("Parse(%q) differs from genie's expected result: %s", command, diff)
	}
	if skipReason != "" {
		if problem == "" {
			t.Fatalf("listed in %s but passes now: remove the entry", skipsFile)
		}
		t.Skipf("listed in %s: %s (%s)", skipsFile, skipReason, problem)
	}
	if problem != "" {
		t.Fatal(problem)
	}
}

// checkEmpty holds one empty fixture: Parse must refuse it as empty.
// genieparser's harness also accepts an AttributeError there.
func checkEmpty(t *testing.T, dir, name string, render func(fixtureArgs) string) {
	out, args := readFixture(t, dir, name)
	_, parsed, err := iosparse.Parse(render(args), out)
	var pe *iosparse.ParserError
	if errors.Is(err, iosparse.ErrEmpty) || (errors.As(err, &pe) && pe.Exception == "AttributeError") {
		return
	}
	t.Fatalf("Parse = %v, %v; want ErrEmpty", parsed, err)
}

// normalize sends v through encoding/json, giving it the same types the
// expected JSON decodes to.
func normalize(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// firstDiff returns the first difference between got and want, by key
// path, or "" when they are equal.
func firstDiff(path string, got, want any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %T %v, want a dict", path, got, got)
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			gv, gok := g[k]
			wv, wok := w[k]
			switch {
			case !gok:
				return fmt.Sprintf("%s/%s: missing, want %v", path, k, wv)
			case !wok:
				return fmt.Sprintf("%s/%s: unexpected %v", path, k, gv)
			}
			if d := firstDiff(path+"/"+k, gv, wv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: got %v, want %v", path, got, want)
		}
		for i := range w {
			if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), g[i], w[i]); d != "" {
				return d
			}
		}
		return ""
	default:
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("%s: got %#v, want %#v", path, got, want)
		}
		return ""
	}
}

// TestShowVersion holds ShowVersion to every genieparser fixture.
func TestShowVersion(t *testing.T) {
	holdToFixtures(t, "ShowVersion", func(fixtureArgs) string { return "show version" })
}

// TestShowInventory holds ShowInventory to every genieparser fixture.
func TestShowInventory(t *testing.T) {
	holdToFixtures(t, "ShowInventory", func(fixtureArgs) string { return "show inventory" })
}

// TestShowIpInterfaceBrief holds ShowIpInterfaceBrief to every
// genieparser fixture.
func TestShowIpInterfaceBrief(t *testing.T) {
	holdToFixtures(t, "ShowIpInterfaceBrief", func(a fixtureArgs) string {
		if a.s("interface") != "" {
			return "show ip interface brief " + a.s("interface")
		}
		return "show ip interface brief"
	})
}

// TestShowInterfaces holds ShowInterfaces to every genieparser fixture.
func TestShowInterfaces(t *testing.T) {
	holdToFixtures(t, "ShowInterfaces", func(a fixtureArgs) string {
		switch {
		case a.s("interface") != "":
			return "show interfaces " + a.s("interface")
		case a.s("include") != "":
			return "show interfaces | include " + a.s("include")
		}
		return "show interfaces"
	})
}

// TestShowIpRoute holds ShowIpRoute to every genieparser fixture.
func TestShowIpRoute(t *testing.T) {
	holdToFixtures(t, "ShowIpRoute", func(a fixtureArgs) string {
		vrf, protocol := a.s("vrf"), a.s("protocol")
		switch {
		case vrf != "" && protocol != "":
			return "show ip route vrf " + vrf + " " + protocol
		case vrf != "":
			return "show ip route vrf " + vrf
		case protocol != "":
			return "show ip route " + protocol
		}
		return "show ip route"
	})
}

// TestShowIpOspfNeighbor holds ShowIpOspfNeighbor to every genieparser
// fixture.
func TestShowIpOspfNeighbor(t *testing.T) {
	holdToFixtures(t, "ShowIpOspfNeighbor", func(a fixtureArgs) string {
		intf, pid := a.s("interface"), a.s("process_id")
		switch {
		case intf != "" && pid != "":
			return "show ip ospf " + pid + " neighbor " + intf
		case intf != "":
			return "show ip ospf neighbor " + intf
		case pid != "":
			return "show ip ospf " + pid + " neighbor"
		}
		return "show ip ospf neighbor"
	})
}

// bgpCommand renders a BGP summary command the way genieparser's class
// builds it from its arguments.
func bgpCommand(prefix string, a fixtureArgs) string {
	af, vrf, rd := a.s("address_family"), a.s("vrf"), a.s("rd")
	switch {
	case af != "" && rd != "":
		return prefix + " " + af + " rd " + rd + " summary"
	case af != "" && vrf != "":
		return prefix + " " + af + " vrf " + vrf + " summary"
	case af != "":
		return prefix + " " + af + " summary"
	}
	return prefix + " summary"
}

// TestShowIpBgpSummary holds ShowIpBgpSummary to every genieparser
// fixture.
func TestShowIpBgpSummary(t *testing.T) {
	holdToFixtures(t, "ShowIpBgpSummary", func(a fixtureArgs) string { return bgpCommand("show ip bgp", a) })
}

// TestShowBgpSummary holds ShowBgpSummary to every genieparser fixture.
func TestShowBgpSummary(t *testing.T) {
	holdToFixtures(t, "ShowBgpSummary", func(a fixtureArgs) string { return bgpCommand("show bgp", a) })
}

// TestShowVlan holds ShowVlan to every genieparser fixture.
func TestShowVlan(t *testing.T) {
	holdToFixtures(t, "ShowVlan", func(fixtureArgs) string { return "show vlan" })
}

// TestShowCdpNeighbors holds ShowCdpNeighbors to every genieparser
// fixture.
func TestShowCdpNeighbors(t *testing.T) {
	holdToFixtures(t, "ShowCdpNeighbors", func(a fixtureArgs) string {
		if a.s("interface") != "" {
			return "show cdp neighbors " + a.s("interface")
		}
		return "show cdp neighbors"
	})
}

// TestShowLldpNeighbors holds ShowLldpNeighbors to every genieparser
// fixture.
func TestShowLldpNeighbors(t *testing.T) {
	holdToFixtures(t, "ShowLldpNeighbors", func(fixtureArgs) string { return "show lldp neighbors" })
}

// TestShowArchiveConfigDifferences holds ShowArchiveConfigDifferences to
// every genieparser fixture.
func TestShowArchiveConfigDifferences(t *testing.T) {
	holdToFixtures(t, "ShowArchiveConfigDifferences", func(a fixtureArgs) string {
		cmd := "show archive config differences"
		if a.s("fileA") != "" {
			cmd += " " + a.s("fileA")
			if a.s("fileB") != "" {
				cmd += " " + a.s("fileB")
			}
		}
		return cmd
	})
}
