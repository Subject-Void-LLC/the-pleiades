// Black-box tests of a filtered setup task's split by device (facts_split.go):
// it converts as two facts.gather tasks, the filtered one for a device that is
// not WindowsShellCapable and an unfiltered one for a device that is, as
// Ansible's Windows setup ignores filter. Each task's condition is decided by
// the engine's own compiled condition over the device root the engine builds,
// with the capabilities of real device types.
package playbook_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	// The device types the tests build register through the inventory's
	// builtins.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// deviceOf builds a device of the registered type typ, as the inventory
// factory would, and returns the device root a condition reads for it: the
// shape internal/engine's deviceRoot gives (device_root.go), its capabilities
// a list of names.
func deviceOf(t *testing.T, typ string) map[string]any {
	t.Helper()
	build, ok := record.LookupType(typ)
	if !ok {
		t.Fatalf("no device type %s", typ)
	}
	d, err := build(record.Record{ID: inventory.DeviceID("dev"), Name: "dev", Type: typ, Properties: map[string]inventory.PropertyValue{}})
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	var capabilities []any
	for _, c := range d.Capabilities() {
		capabilities = append(capabilities, string(c))
	}
	return map[string]any{"name": "dev", "id": "dev", "tags": []any{}, "properties": map[string]any{}, "capabilities": capabilities, "state": "active"}
}

// runsOn reports whether node's condition holds for device, with gathered
// facts as the inserted gather's register on that device (nil for none),
// through the condition the engine compiled.
func runsOn(t *testing.T, dag *engine.DAG, node string, device, gathered map[string]any) bool {
	t.Helper()
	cp := dag.Conditions[node]
	if cp == nil {
		return true
	}
	nodes := map[string]any{}
	if gathered != nil {
		nodes["gathered_facts"] = map[string]any{"dev": gathered}
	}
	res, err := cp.Eval(map[string]any{"device": device, "nodes": nodes, "stat": nodes, "vars": map[string]any{}, "result": map[string]any{}})
	if err != nil {
		t.Fatalf("%s: the condition does not evaluate: %v", node, err)
	}
	return res.OK
}

// gatherNodes is the DAG's facts.gather nodes named after the setup task
// called name, by name.
func gatherNodes(dag *engine.DAG, name string) map[string]string {
	out := map[string]string{}
	for id, n := range dag.Nodes {
		if n.FQCN == "facts.gather" && strings.HasPrefix(n.Name, name) {
			out[n.Name] = id
		}
	}
	return out
}

func TestTranslate_AFilteredSetupSplitsByWhetherTheDeviceIsWindows(t *testing.T) {
	res := convert(t, "- hosts: web\n  gather_facts: false\n  tasks:\n    - name: facts\n      ansible.builtin.setup: {filter: ansible_os_family}\n      register: found\n")
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("the setup task did not convert: codes %v\n%s", codesOf(res.Report), res.Report.String())
	}
	dag := build(t, res.Runbooks[0].YAML)
	if report := validate.Validate(validate.WorldView{DAG: dag}); report.HasErrors() {
		t.Errorf("the converted runbook does not validate:\n%s", report.String())
	}
	nodes := gatherNodes(dag, "facts")
	filtered, whole := nodes["facts (not Windows)"], nodes["facts (Windows)"]
	if len(nodes) != 2 || filtered == "" || whole == "" {
		t.Fatalf("facts.gather tasks %v, want facts (not Windows) and facts (Windows):\n%s", nodes, res.Runbooks[0].YAML)
	}
	if got := dag.Nodes[filtered].Params["filter"]; !slices.Equal(anyStrings(got), []string{"os.family"}) {
		t.Errorf("the filtered task's filter is %v, want [os.family]", got)
	}
	if _, ok := dag.Nodes[whole].Params["filter"]; ok {
		t.Errorf("the Windows task carries a filter: %v", dag.Nodes[whole].Params)
	}
	// Both register the playbook's name: on each device only the one that
	// ran writes it, since a skipped task registers nothing.
	if dag.Nodes[filtered].Register != "found" || dag.Nodes[whole].Register != "found" {
		t.Errorf("registers %q and %q, want found on both", dag.Nodes[filtered].Register, dag.Nodes[whole].Register)
	}
	for _, tc := range []struct {
		typ            string
		filtered, full bool
	}{
		{"windows_server", false, true},
		{"linux_server", true, false},
		// The known gap: a Windows host reached as generic_ssh is not
		// WindowsShellCapable unless onboarding granted it, so it gets the
		// filtered gather.
		{"generic_ssh", true, false},
	} {
		device := deviceOf(t, tc.typ)
		if got := runsOn(t, dag, filtered, device, nil); got != tc.filtered {
			t.Errorf("%s: the filtered task runs %v, want %v", tc.typ, got, tc.filtered)
		}
		if got := runsOn(t, dag, whole, device, nil); got != tc.full {
			t.Errorf("%s: the unfiltered task runs %v, want %v", tc.typ, got, tc.full)
		}
	}
}

func TestTranslate_ASetupFilterKeepingEverythingIsNotSplit(t *testing.T) {
	// No filter to ignore, so one gather serves every device.
	for _, filter := range []string{"'*'", "'ansible_*'", "[]"} {
		res := convert(t, setupPlay("ansible.builtin.setup", "{filter: "+filter+"}"))
		dag := build(t, res.Runbooks[0].YAML)
		nodes := gatherNodes(dag, "facts")
		if len(nodes) != 1 || nodes["facts"] == "" || dag.Conditions[nodes["facts"]] != nil {
			t.Errorf("%s: facts.gather tasks %v, want one named facts with no condition:\n%s", filter, nodes, res.Runbooks[0].YAML)
		}
	}
}

func TestTranslate_ASplitSetupKeepsItsOwnCondition(t *testing.T) {
	// The task's own condition (over an earlier task's result, decided at
	// run time) is ANDed with the split on both tasks, never replaced.
	res := convert(t, "- hosts: web\n  gather_facts: false\n  tasks:\n    - name: list\n      ansible.builtin.command: ls\n      register: listed\n    - name: facts\n      ansible.builtin.setup: {filter: ansible_os_family}\n      when: listed.rc == 0\n")
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("codes %v\n%s", codesOf(res.Report), res.Report.String())
	}
	text := string(res.Runbooks[0].YAML)
	dag := build(t, res.Runbooks[0].YAML)
	if report := validate.Validate(validate.WorldView{DAG: dag}); report.HasErrors() {
		t.Errorf("the converted runbook does not validate:\n%s", report.String())
	}
	nodes := gatherNodes(dag, "facts")
	if len(nodes) != 2 {
		t.Fatalf("facts.gather tasks %v:\n%s", nodes, text)
	}
	for _, rc := range []int64{0, 1} {
		listed := map[string]any{"listed": map[string]any{"dev": map[string]any{"rc": rc, "stdout": "", "stderr": ""}}}
		for _, typ := range []string{"windows_server", "linux_server"} {
			ran := 0
			for _, id := range nodes {
				if runsOnWith(t, dag, id, deviceOf(t, typ), listed) {
					ran++
				}
			}
			// The listing succeeded: exactly one of the two runs; it failed:
			// neither, as Ansible skips the one setup task.
			if want := map[int64]int{0: 1, 1: 0}[rc]; ran != want {
				t.Errorf("rc %d on a %s: %d of the split tasks ran, want %d:\n%s", rc, typ, ran, want, text)
			}
		}
	}
}

// runsOnWith is runsOn over a whole stat tree.
func runsOnWith(t *testing.T, dag *engine.DAG, node string, device, tree map[string]any) bool {
	t.Helper()
	res, err := dag.Conditions[node].Eval(map[string]any{"device": device, "nodes": tree, "stat": tree, "vars": map[string]any{}, "result": map[string]any{}})
	if err != nil {
		t.Fatalf("%s: the condition does not evaluate: %v", node, err)
	}
	return res.OK
}

func TestTranslate_AFactReadAfterASplitSetupDecidesPerHost(t *testing.T) {
	res := convert(t, `- hosts: web
  tasks:
    - name: facts
      ansible.builtin.setup: {filter: ansible_os_family}
    - name: mark
      ansible.builtin.copy: {dest: /tmp/m, content: x, mode: '0644'}
      when: ansible_distribution_major_version | int >= 11
`)
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("codes %v\n%s", codesOf(res.Report), res.Report.String())
	}
	dag := build(t, res.Runbooks[0].YAML)
	if report := validate.Validate(validate.WorldView{DAG: dag}); report.HasErrors() {
		t.Errorf("the converted runbook does not validate:\n%s", report.String())
	}
	var mark string
	for id, n := range dag.Nodes {
		if n.Name == "mark" {
			mark = id
		}
		// The split tasks never take the inserted gather's register, which
		// the read below reads whichever of them ran.
		if n.FQCN == "facts.gather" && strings.HasPrefix(n.Name, "facts") && n.Register == "gathered_facts" {
			t.Errorf("%s registers the inserted gather's name", n.Name)
		}
	}
	// Debian 12 from the measured fixture through the real os-release
	// parser; Windows from the min script's sample JSON (a hand fixture: no
	// Windows image is captured) through the real Windows parser.
	debian := measuredHosts(t)["debian-12.12"].native
	windowsProbes := facts.Probes("Windows", "min", "")
	if len(windowsProbes) == 0 {
		t.Fatal("no Windows min probe")
	}
	windows := windowsProbes[0].Read(`{"host.name":"WIN-CHECK","os.kernel.name":"windows","os.distribution":"windows","os.family":"windows","os.version":"10.0.26100.0","os.edition":"Microsoft Windows 11 Pro"}`)
	for _, tc := range []struct {
		name     string
		device   map[string]any
		gathered map[string]any
		// want is Jinja's int of Ansible's major (Debian's "12", Windows'
		// Version.Major "10", setup.ps1 line 662) compared with 11.
		want bool
	}{
		{"debian", deviceOf(t, "linux_server"), debian, true},
		{"windows", deviceOf(t, "windows_server"), windows, false},
	} {
		if got := runsOn(t, dag, mark, tc.device, tc.gathered); got != tc.want {
			t.Errorf("%s: mark runs %v, want %v (gathered %v)", tc.name, got, tc.want, tc.gathered)
		}
	}
}

// anyStrings is a list of strings held as []any, as strings.
func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
