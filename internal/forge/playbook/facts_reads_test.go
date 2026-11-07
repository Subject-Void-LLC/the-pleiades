// Black-box tests of a play that reads Ansible's facts (facts_reads.go).
package playbook_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// factPlay is a play gathering facts by default, with one task gated on when
// and one templated param.
func factPlay(when, msg string) string {
	return `- hosts: web
  tasks:
    - name: mark
      ansible.builtin.copy:
        dest: /tmp/marker
        content: "` + msg + `"
      when: ` + when + "\n"
}

func TestTranslate_AFactReadReadsTheNativeFactOfAnInsertedGather(t *testing.T) {
	for _, tc := range []struct {
		name, when, msg string
		wantWhen        string
		wantContent     string
		wantCode        playbook.Code
	}{
		{name: "family", when: `ansible_os_family == "Debian"`, msg: "x",
			wantWhen: `nodes.gathered_facts[device.id]["os.family"] == "debian"`},
		{name: "distribution in a list", when: `ansible_distribution in ["Ubuntu", "RedHat"]`, msg: "x",
			wantWhen: `nodes.gathered_facts[device.id]["os.distribution"] in ["ubuntu", "rhel"]`},
		{name: "major version as a string", when: `ansible_distribution_major_version == "12"`, msg: "x",
			wantWhen: `nodes.gathered_facts[device.id]["os.version_major"] == 12`},
		{name: "major version through int", when: `ansible_distribution_major_version | int >= 12`, msg: "x",
			wantWhen: `nodes.gathered_facts[device.id]["os.version_major"] >= 12`},
		{name: "a kernel name", when: `ansible_system == "Linux"`, msg: "x",
			wantWhen: `nodes.gathered_facts[device.id]["os.kernel.name"] == "linux"`},
		{name: "a value with no native word", when: `ansible_distribution == "Microsoft Windows Server 2022 Datacenter"`, msg: "x",
			wantCode: "when.fact"},
		// A template cannot index the facts by the device it runs on.
		{name: "a template reading a fact", when: `ansible_system == "Linux"`, msg: "{{ ansible_hostname }}",
			wantCode: "template.fact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, factPlay(tc.when, tc.msg))
			codes := codesOf(res.Report)
			if !slices.Contains(codes, "play.facts_gathered") || slices.Contains(codes, "play.facts") {
				t.Errorf("codes %v, want play.facts_gathered and no play.facts", codes)
			}
			if tc.wantCode != "" {
				if !slices.Contains(codes, tc.wantCode) {
					t.Errorf("codes %v, want %s", codes, tc.wantCode)
				}
				return
			}
			if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
				t.Fatalf("runbooks %+v; codes %v", res.Runbooks, codes)
			}
			text := string(res.Runbooks[0].YAML)
			build(t, res.Runbooks[0].YAML)
			gather := strings.Index(text, "facts.gather")
			mark := strings.Index(text, "name: mark")
			if gather < 0 || mark < 0 || gather > mark || !strings.Contains(text, "register: gathered_facts") {
				t.Errorf("the gather is not registered before the play's tasks:\n%s", text)
			}
			if !strings.Contains(text, tc.wantWhen) {
				t.Errorf("when is not %s:\n%s", tc.wantWhen, text)
			}
			if tc.wantContent != "" && !strings.Contains(text, tc.wantContent) {
				t.Errorf("the template does not read %s:\n%s", tc.wantContent, text)
			}
		})
	}
}

func TestTranslate_APlayThatGathersNothingOrReadsNoFactGetsNoGather(t *testing.T) {
	off := convert(t, "- hosts: web\n  gather_facts: false\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n")
	if codes := codesOf(off.Report); !slices.Contains(codes, "when.fact") || slices.Contains(codes, "play.facts_gathered") {
		t.Errorf("gather_facts off: codes %v, want when.fact and no gather", codes)
	}
	none := convert(t, "- hosts: web\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n")
	if codes := codesOf(none.Report); !slices.Contains(codes, "play.facts") || slices.Contains(codes, "play.facts_gathered") {
		t.Errorf("no fact read: codes %v, want play.facts and no gather", codes)
	}
}

func TestTranslate_TheGathersRegisterDoesNotTakeAPlaybooksOwn(t *testing.T) {
	res := convert(t, `- hosts: web
  tasks:
    - name: own
      ansible.builtin.command: uptime
      register: gathered_facts
    - name: mark
      ansible.builtin.copy: {dest: /tmp/m, content: x}
      when: ansible_os_family == "Debian"
`)
	text := ""
	if len(res.Runbooks) == 1 {
		text = string(res.Runbooks[0].YAML)
	}
	if !strings.Contains(text, "register: gathered_facts_2") || !strings.Contains(text, `nodes.gathered_facts_2[device.id]["os.family"] == "debian"`) {
		t.Errorf("the gather took the playbook's own register name:\n%s\ncodes %v", text, codesOf(res.Report))
	}
}
