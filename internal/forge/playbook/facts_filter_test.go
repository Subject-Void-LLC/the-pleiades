// Black-box tests of setup's filter (facts_filter.go): a playbook's setup
// task converts to a facts.gather whose filter is the native keys the
// patterns match, which facts.gather itself accepts, and a filter it cannot
// expand is refused at conversion, naming the pattern.
package playbook_test

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// setupPlay is a play gathering nothing itself, with one setup task whose
// arguments are args, written as a YAML flow map.
func setupPlay(module, args string) string {
	return "- hosts: web\n  gather_facts: false\n  tasks:\n    - name: list\n      ansible.builtin.command: ls\n      register: listed\n    - name: facts\n      " + module + ": " + args + "\n"
}

// gatherTask is the converted facts.gather task of a report, or fails.
func gatherTask(t *testing.T, res playbook.Result) playbook.TaskResult {
	t.Helper()
	for _, task := range res.Report.Tasks {
		if task.FQCN == "facts.gather" && task.Outcome != playbook.OutcomeBlocked {
			return task
		}
	}
	t.Fatalf("no converted facts.gather:\n%s", res.Report.String())
	return playbook.TaskResult{}
}

// planRefusal is what facts.gather's own planning says of params: the
// method refuses a filter before it reaches a device, so with none it fails
// at the connection. It returns the refusal when the plan refused, "" when
// planning passed.
func planRefusal(t *testing.T, params map[string]any) string {
	t.Helper()
	_, err := facts.Gather(context.Background(), nil, nil, params)
	if err != nil && strings.Contains(err.Error(), "filter") {
		return err.Error()
	}
	return ""
}

func TestTranslate_ASetupFilterIsWrittenAsTheNativeKeysItMatches(t *testing.T) {
	// The control: the Ansible pattern copied across as it was before is
	// what facts.gather refuses (FAILURE_PATTERNS 967).
	if planRefusal(t, map[string]any{"filter": []any{"ansible_os_family"}}) == "" {
		t.Fatal("facts.gather accepts an Ansible pattern, so planRefusal proves nothing")
	}
	for _, tc := range []struct {
		name, args string
		// keys is the filter the facts.gather carries; nil for none.
		keys []string
	}{
		{name: "one name", args: "{filter: ansible_os_family}", keys: []string{"os.family"}},
		{name: "the distribution brings the Windows edition", args: "{filter: ansible_distribution}", keys: []string{"os.distribution", "os.edition"}},
		{name: "swap brings memory", args: "{filter: ansible_swaptotal_mb}", keys: []string{"hardware.memory_mb", "hardware.swap_mb"}},
		{name: "Ansible's prefix rule", args: "{filter: os_family}", keys: []string{"os.family"}},
		{name: "a prefixed glob", args: "{filter: 'ansible_distribution*'}",
			keys: []string{"os.codename", "os.distribution", "os.edition", "os.version", "os.version_major"}},
		{name: "an unprefixed glob", args: "{filter: '*_mb'}", keys: []string{"hardware.memory_mb", "hardware.swap_mb"}},
		{name: "a question mark", args: "{filter: 'ansible_processor_co??s'}", keys: []string{"hardware.cpu.cores"}},
		{name: "a list", args: "{filter: [ansible_kernel, ansible_hostname, ansible_kernel]}", keys: []string{"host.name", "os.kernel.release"}},
		{name: "a list beside gather_subset", args: "{filter: [ansible_system], gather_subset: [min]}", keys: []string{"os.kernel.name"}},
		// Under min both Ansible and facts.gather gather the min facts of a
		// mixed filter and drop the hardware ones; facts.gather refuses only a
		// filter matching none of the subsets' facts.
		{name: "a mixed filter under min", args: "{filter: [ansible_swaptotal_mb, ansible_os_family], gather_subset: [min]}",
			keys: []string{"hardware.memory_mb", "hardware.swap_mb", "os.family"}},
		{name: "every fact", args: "{filter: '*'}"},
		{name: "every Ansible fact", args: "{filter: 'ansible_*'}"},
		{name: "an empty pattern", args: "{filter: ''}"},
		{name: "an empty list", args: "{filter: []}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, setupPlay("ansible.builtin.setup", tc.args))
			if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
				t.Fatalf("the setup task did not convert: codes %v\n%s", codesOf(res.Report), res.Report.String())
			}
			build(t, res.Runbooks[0].YAML)
			task := gatherTask(t, res)
			var got []string
			if raw, ok := task.Params["filter"]; ok {
				for _, key := range raw.([]any) {
					got = append(got, key.(string))
				}
			}
			if !reflect.DeepEqual(got, tc.keys) {
				t.Errorf("filter %v, want %v", got, tc.keys)
			}
			// What the runbook hands facts.gather passes its own planning, where
			// the pattern copied across failed on every host.
			if why := planRefusal(t, task.Params); why != "" {
				t.Errorf("facts.gather refuses the converted filter: %s", why)
			}
			if text := string(res.Runbooks[0].YAML); strings.Contains(text, "ansible_") {
				t.Errorf("an Ansible pattern was copied into the runbook:\n%s", text)
			}
			// A written list carries the review; a filter keeping every fact
			// converts exactly, with none.
			if got := strings.Contains(res.Report.String(), "setup ignores filter"); got != (tc.keys != nil) {
				t.Errorf("the report says a Windows host's setup ignores filter: %v, want %v\n%s", got, tc.keys != nil, res.Report.String())
			}
			if tc.keys == nil && task.Outcome != playbook.OutcomeConverted {
				t.Errorf("outcome %s, want converted:\n%s", task.Outcome, res.Report.String())
			}
		})
	}
}

func TestTranslate_ASetupFilterRefusalKeepsTheSubsetsReview(t *testing.T) {
	// gather_subset's network has no facts.gather counterpart, a review the
	// refusal of the filter beside it must not swallow.
	res := convert(t, setupPlay("ansible.builtin.setup", "{filter: nosuchfact, gather_subset: [network]}"))
	codes := codesOf(res.Report)
	if !slices.Contains(codes, "args.value") || !strings.Contains(res.Report.String(), "gather_subset's network") {
		t.Errorf("codes %v, want args.value and the subset's review of network:\n%s", codes, res.Report.String())
	}
	// And beside a filter that converts, both reviews reach the report: the
	// report keeps one finding per code and position.
	res = convert(t, setupPlay("ansible.builtin.setup", "{filter: 'ansible_os_*', gather_subset: [network]}"))
	for _, want := range []string{"gather_subset's network", "written as the native keys", `"ansible_os_*" may also match`} {
		if !strings.Contains(res.Report.String(), want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Report.String())
		}
	}
}

func TestTranslate_ASetupFilterGlobNamesWhatItMayAlsoMatch(t *testing.T) {
	res := convert(t, setupPlay("ansible.builtin.setup", "{filter: 'ansible_distribution*'}"))
	if task := gatherTask(t, res); task.Outcome != playbook.OutcomeReview {
		t.Errorf("outcome %s, want review", task.Outcome)
	}
	if report := res.Report.String(); !strings.Contains(report, `"ansible_distribution*" may also match`) {
		t.Errorf("the report does not name the glob:\n%s", report)
	}
}

func TestTranslate_ASetupFilterItCannotExpandIsRefusedNamingThePattern(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		// named is text the refusal must carry.
		named string
	}{
		{name: "a fact the platform does not gather", args: "{filter: ansible_default_ipv4}", named: strconv.Quote("ansible_default_ipv4")},
		{name: "one pattern of a list", args: "{filter: [ansible_memtotal_mb, nosuchfact]}", named: strconv.Quote("nosuchfact")},
		{name: "a capital", args: "{filter: Ansible_Distribution}", named: strconv.Quote("Ansible_Distribution")},
		{name: "the ansible_facts spelling", args: "{filter: ansible_facts.os_family}", named: strconv.Quote("ansible_facts.os_family")},
		{name: "a facter pattern", args: "{filter: 'facter_*'}", named: strconv.Quote("facter_*")},
		{name: "a character class", args: "{filter: 'ansible_[dk]*'}", named: "character class"},
		// A loop over a registered result stays a native loop, whose item is
		// known only at run time.
		{name: "a run-time value", args: "{filter: '{{ item }}'}\n      loop: '{{ listed.stdout }}'", named: "run time"},
		{name: "a comma list", args: "{filter: 'ansible_os_family,ansible_system'}", named: "commas"},
		{name: "hardware only without the hardware subset", args: "{filter: ansible_memtotal_mb, gather_subset: ['!hardware']}", named: "hardware"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, setupPlay("ansible.builtin.setup", tc.args))
			for _, task := range res.Report.Tasks {
				if task.FQCN == "facts.gather" && task.Outcome != playbook.OutcomeBlocked {
					t.Errorf("the setup task converted: %+v", task.Params)
				}
			}
			if res.Report.Counts.Blocked != 1 {
				t.Errorf("blocked %d, want 1", res.Report.Counts.Blocked)
			}
			// The setup task's own refusal, args.value, names it, not some
			// other construct's.
			refused := false
			for _, f := range res.Report.Findings {
				if f.Task == "facts" && f.Outcome == playbook.OutcomeBlocked && f.Code == "args.value" && strings.Contains(f.Message, tc.named) {
					refused = true
				}
			}
			if !refused {
				t.Errorf("no args.value refusal of the setup task names %s:\n%s", tc.named, res.Report.String())
			}
		})
	}
}

func TestTranslate_AFilterInModuleDefaultsConvertsNoFactRead(t *testing.T) {
	// Ansible's implicit gather takes a filter only through module_defaults,
	// which the inserted facts.gather would not honor; module_defaults is
	// refused outright, so no task reads a fact the filter left out.
	res := convert(t, "- hosts: web\n  module_defaults:\n    ansible.builtin.setup:\n      filter: ansible_hostname\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n")
	if !slices.Contains(codesOf(res.Report), "keyword.module_defaults") {
		t.Errorf("codes %v, want keyword.module_defaults", codesOf(res.Report))
	}
	for _, task := range converted(res.Report) {
		if task.FQCN != "facts.gather" {
			t.Errorf("a task converted under module_defaults: %s", task.FQCN)
		}
	}
}

func TestTranslate_WindowsSetupDropsTheFilterItIgnores(t *testing.T) {
	res := convert(t, setupPlay("ansible.windows.setup", "{filter: ansible_os_family}"))
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("the setup task did not convert: codes %v", codesOf(res.Report))
	}
	if task := gatherTask(t, res); task.Params["filter"] != nil {
		t.Errorf("filter %v, want none: ansible.windows.setup gathers every fact", task.Params["filter"])
	}
	if !slices.Contains(codesOf(res.Report), "args.ignored") {
		t.Errorf("codes %v, want args.ignored", codesOf(res.Report))
	}
}
