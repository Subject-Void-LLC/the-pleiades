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
		// | int differs where the fact is absent (Ansible's int of NA is 0).
		{name: "major version through int", when: `ansible_distribution_major_version | int >= 12`, msg: "x", wantCode: "when.fact"},
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

func TestTranslate_AFactReadKeepsAnsiblesMeaning(t *testing.T) {
	for _, tc := range []struct {
		name, when string
		want       string
		code       playbook.Code
	}{
		// An absent fact (no codename on a rolling release) is not equal to
		// anything, as Ansible's NA is not, rather than failing the task.
		{name: "an absent fact compares false", when: `ansible_distribution_release == "bookworm"`,
			want: `("os.codename" in nodes.gathered_facts[device.id] && (nodes.gathered_facts[device.id]["os.codename"] == "bookworm"))`},
		{name: "an absent fact is not equal", when: `ansible_distribution_release != "bookworm"`,
			want: `(!("os.codename" in nodes.gathered_facts[device.id]) || (nodes.gathered_facts[device.id]["os.codename"] != "bookworm"))`},
		// Ansible's major version is a string: an ordering compares text and a
		// number never equals it, which the native integer would change.
		{name: "an ordering over the version string", when: `ansible_distribution_major_version >= "8"`, code: "when.fact"},
		{name: "a number against the version string", when: `ansible_distribution_major_version == 12`, code: "when.fact"},
		{name: "through int", when: `ansible_distribution_major_version | int >= 8`, code: "when.fact"},
		{name: "a pattern over a fact", when: `ansible_distribution is match("Ubuntu|Debian")`, code: "when.fact"},
		{name: "a fact read as a truth", when: `ansible_distribution_release`, code: "when.fact"},
		{name: "an architecture Windows words differently", when: `ansible_architecture == "x86_64"`, code: "when.fact"},
		{name: "a filter other than int", when: `ansible_hostname | length > 3`, code: "when.fact"},
		{name: "the ansible_facts spelling", when: `ansible_facts['os_family'] == "Debian"`,
			want: `nodes.gathered_facts[device.id]["os.family"] == "debian"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, factPlay(tc.when, "x"))
			codes := codesOf(res.Report)
			if tc.code != "" {
				if !slices.Contains(codes, tc.code) {
					t.Errorf("codes %v, want %s", codes, tc.code)
				}
				return
			}
			if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
				t.Fatalf("runbooks %+v; codes %v", res.Runbooks, codes)
			}
			build(t, res.Runbooks[0].YAML)
			if text := string(res.Runbooks[0].YAML); !strings.Contains(text, tc.want) {
				t.Errorf("when is not %s:\n%s", tc.want, text)
			}
		})
	}
}

func TestTranslate_OnlyAConditionDecidesThePlayGathers(t *testing.T) {
	// A task named after a fact reads no fact: the play gathers nothing.
	res := convert(t, "- hosts: web\n  tasks:\n    - name: ansible_os_family is not read here\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n")
	if codes := codesOf(res.Report); !slices.Contains(codes, "play.facts") || slices.Contains(codes, "play.facts_gathered") {
		t.Errorf("codes %v, want play.facts and no gather", codes)
	}
}

func TestTranslate_TwoPlaysOnOneHostGroupGatherUnderTwoNames(t *testing.T) {
	play := "- hosts: web\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n"
	res := convert(t, play+play)
	var text string
	for _, rb := range res.Runbooks {
		text += string(rb.YAML)
	}
	if !strings.Contains(text, "register: gathered_facts\n") || !strings.Contains(text, "register: gathered_facts_2") {
		t.Errorf("the two plays' gathers do not register two names:\n%s", text)
	}
}

func TestTranslate_TheGatherRunsWhateverTagsSelect(t *testing.T) {
	res := convert(t, factPlay(`ansible_os_family == "Debian"`, "x"))
	text := ""
	if len(res.Runbooks) == 1 {
		text = string(res.Runbooks[0].YAML)
	}
	gather := strings.Index(text, "facts.gather")
	mark := strings.Index(text, "name: mark")
	if gather < 0 || mark < 0 || !strings.Contains(text[gather:mark], "always") {
		t.Errorf("the inserted gather is not tagged always:\n%s", text)
	}
}

func TestTranslate_APlayTheGatherCannotServeKeepsItsFactsDropped(t *testing.T) {
	for name, play := range map[string]string{
		"localhost":       "- hosts: localhost\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n",
		"gather_subset":   "- hosts: web\n  gather_subset: [network]\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n",
		"a shadowing var": "- hosts: web\n  vars:\n    ansible_os_family: Debian\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: ansible_os_family == \"Debian\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			res := convert(t, play)
			codes := codesOf(res.Report)
			if name != "a shadowing var" && slices.Contains(codes, "play.facts_gathered") {
				t.Errorf("codes %v: a gather was inserted", codes)
			}
			for _, rb := range res.Runbooks {
				if strings.Contains(string(rb.YAML), "nodes.gathered_facts") {
					t.Errorf("the condition reads the gathered fact:\n%s", rb.YAML)
				}
			}
		})
	}
}

func TestTranslate_AnOpenBSDReleaseWordIsRefusedNamingOpenBSD(t *testing.T) {
	// Ansible reports these as OpenBSD's release (kern.version's suffix, or
	// release); none is a codename, and this platform gathers no facts there.
	for _, word := range []string{"release", "current", "stable", "beta"} {
		for _, when := range []string{
			`ansible_distribution_release == "` + word + `"`,
			`ansible_distribution_release != "` + word + `"`,
			`ansible_distribution_release in ["bookworm", "` + word + `"]`,
		} {
			t.Run(when, func(t *testing.T) {
				res := convert(t, factPlay(when, "x"))
				if !slices.Contains(codesOf(res.Report), "when.fact") {
					t.Errorf("codes %v, want when.fact", codesOf(res.Report))
				}
				if report := res.Report.String(); !strings.Contains(report, "OpenBSD") {
					t.Errorf("the refusal does not name OpenBSD:\n%s", report)
				}
				for _, rb := range res.Runbooks {
					if strings.Contains(string(rb.YAML), `"os.codename"`) {
						t.Errorf("the condition was converted:\n%s", rb.YAML)
					}
				}
			})
		}
	}
	// A codename still converts.
	res := convert(t, factPlay(`ansible_distribution_release == "bookworm"`, "x"))
	if len(res.Runbooks) != 1 || !strings.Contains(string(res.Runbooks[0].YAML), `["os.codename"] == "bookworm"`) {
		t.Errorf("a codename no longer converts: codes %v", codesOf(res.Report))
	}
}

func TestTranslate_AFactRefusalNamesTheValuesKindNotTheValue(t *testing.T) {
	const value = "hunter2-like-text-in-a-condition"
	res := convert(t, factPlay(`ansible_distribution == "`+value+`"`, "x"))
	if !slices.Contains(codesOf(res.Report), "when.fact") {
		t.Fatalf("codes %v, want when.fact", codesOf(res.Report))
	}
	if strings.Contains(res.Report.String(), value) {
		t.Errorf("the report quotes the playbook's value:\n%s", res.Report.String())
	}
}
