// Tests of how a family probe's answer is judged (gather_outcome.go) and of
// the parsers the sweep corrected (items 1, 8, 9, 11).
package facts

import (
	"strings"
	"testing"
)

// TestReadOutcome tells absent, failed, malformed and read apart, and takes
// the marks out of an answer it keeps.
func TestReadOutcome(t *testing.T) {
	notEmpty := func(s string) error { return listsSomething("x")(s) }
	cases := map[string]struct {
		out  string
		want gatherOutcome
		body string
	}{
		"absent":                {gatherAbsentMark + "\n", outcomeAbsent, ""},
		"failed after a line":   {"a\n" + gatherFailedMark + "\n", outcomeFailed, ""},
		"read":                  {"a\nb\n", outcomeRead, "a\nb\n"},
		"lacking its structure": {"\n", outcomeFailed, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, body, err := readOutcome(tc.out, notEmpty)
			if got != tc.want || body != tc.body || (got == outcomeFailed) != (err != nil) {
				t.Fatalf("readOutcome = %v, %q, %v; want %v, %q", got, body, err, tc.want, tc.body)
			}
		})
	}
}

// TestFamilyVerdict: a failure fails whatever else answered; nothing read
// fails a named subset and warns for one reached through all.
func TestFamilyVerdict(t *testing.T) {
	if _, err := familyVerdict("package.", familyState{subset: "packages", read: true, failures: []string{"rpm: its listing failed"}}, false); err == nil || !strings.Contains(err.Error(), "rpm") {
		t.Errorf("a failure beside a read source: %v", err)
	}
	if w, err := familyVerdict("package.", familyState{subset: "packages", read: true}, true); w != "" || err != nil {
		t.Errorf("a read family: %q, %v", w, err)
	}
	if _, err := familyVerdict("service.", familyState{subset: "services"}, true); err == nil || !strings.Contains(err.Error(), "!services") {
		t.Errorf("nothing read for a named subset: %v", err)
	}
	if w, err := familyVerdict("service.", familyState{subset: "services"}, false); err != nil || !strings.Contains(w, "service facts") {
		t.Errorf("nothing read through all: %q, %v", w, err)
	}
}

// TestGatherExplicit: all is not a name, and an exclusion does not take a
// name back, as Ansible's rule has it.
func TestGatherExplicit(t *testing.T) {
	got := gatherExplicit(map[string]any{gatherParamSubset: []any{"all", " packages", "!services"}})
	if len(got) != 1 || !got["packages"] {
		t.Fatalf("explicit = %v, want packages alone", got)
	}
}

// TestGatherNamed_FollowsAnsiblesRuleInAnyOrder: an exclusion removes what
// all added whatever its place in the list, a name survives its own
// exclusion, !all keeps min, and exclusions alone start from the default
// (sweep item 16).
func TestGatherNamed_FollowsAnsiblesRuleInAnyOrder(t *testing.T) {
	cases := map[string]struct {
		names []string
		want  []string
	}{
		"an exclusion before all":  {[]string{"!packages", "all"}, []string{"min", "hardware", "services", "files", "ports", "users"}},
		"an exclusion after all":   {[]string{"all", "!packages"}, []string{"min", "hardware", "services", "files", "ports", "users"}},
		"a name and its exclusion": {[]string{"packages", "!packages"}, []string{"min", "packages"}},
		"not all, then a name":     {[]string{"!all", "packages"}, []string{"min", "packages"}},
		"exclusions alone, spaced": {[]string{" !hardware"}, []string{"min"}},
		"min excluded":             {[]string{"all", "!min"}, []string{"hardware", "packages", "services", "files", "ports", "users"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := gatherNamed(tc.names, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("gatherNamed(%q) = %v, want %v", tc.names, got, tc.want)
			}
			for _, s := range tc.want {
				if !got[s] {
					t.Fatalf("gatherNamed(%q) = %v, want %v", tc.names, got, tc.want)
				}
			}
		})
	}
}

// TestStructureChecks: each source's answer is refused without the
// structure it always has, and accepted with it.
func TestStructureChecks(t *testing.T) {
	unit := "sshd.service loaded active running OpenSSH\n"
	cases := []struct {
		name  string
		check func(string) error
		out   string
		ok    bool
	}{
		{"systemd, both sections", checkSystemd, "sshd.service enabled\n" + serviceSectionMark + "\n" + unit, true},
		{"systemd, no loaded service", checkSystemd, "sshd.service enabled\n" + serviceSectionMark + "\n", false},
		{"systemd, one section", checkSystemd, "sshd.service enabled\n", false},
		{"openrc, three sections", checkOpenRC, "sshd\n" + serviceSectionMark + "\n" + serviceSectionMark + "\n", true},
		{"openrc, no script", checkOpenRC, "\n" + serviceSectionMark + "\n" + serviceSectionMark + "\n", false},
		{"rc, three sections", checkRC, "/etc/rc.d/sshd\n" + serviceSectionMark + "\n" + serviceSectionMark + "\n", true},
		{"rc, two sections", checkRC, "/etc/rc.d/sshd\n" + serviceSectionMark + "\n", false},
		{"launchd, a services block", checkLaunchd, "system = {\n\tservices = {\n\t}\n}\n" + serviceSectionMark + "\n", true},
		{"launchd, a layout it does not read", checkLaunchd, "system = {\n}\n" + serviceSectionMark + "\n", false},
		{"windows, an array", checkWindowsArray, "[]\r\n", true},
		{"windows, text before the array", checkWindowsArray, "AutoRun says hello\r\n[{\"name\":\"a\"}]", false},
		{"windows, an array cut short", checkWindowsArray, `[{"name":"a"},{"na`, false},
		{"windows, nothing", checkWindowsArray, "", false},
		{"linux ports, every section", checkLinuxPorts, strings.Repeat(portSectionMark+"\n", 6), true},
		{"linux ports, cut short", checkLinuxPorts, strings.Repeat(portSectionMark+"\n", 3), false},
	}
	for _, tc := range cases {
		if err := tc.check(tc.out); (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

// TestParseWindowsObject_OnlyAWholeObject: an object cut short after a
// complete member, or followed by other text, is no answer; a whole one is.
func TestParseWindowsObject_OnlyAWholeObject(t *testing.T) {
	for _, out := range []string{`{"ansible_hostname":"web1",`, `{"ansible_hostname":"web1"} trailing`, `{"ansible_hostname":"web1"}{}`} {
		if got := parseWindowsObject(out); got != nil {
			t.Errorf("parseWindowsObject(%q) = %v, want nothing", out, got)
		}
	}
	if got := parseWindowsObject(`{"ansible_hostname":"web1"}` + "\r\n"); len(got) != 1 {
		t.Errorf("a whole object: %v", got)
	}
}

// TestParseOpenRC_ASupervisedServiceIsRunning: supervise-daemon's status
// carries the uptime, which is not part of the state (sweep item 9).
func TestParseOpenRC_ASupervisedServiceIsRunning(t *testing.T) {
	out := "sshd\n" + serviceSectionMark + "\nsshd | default\n" + serviceSectionMark + "\n[default]\nsshd =  started 1 day(s) 02:13:07 (0)\n"
	got := parseOpenRC(out)
	if len(got) != 1 || got[0].value.(map[string]any)["state"] != "running" {
		t.Fatalf("parseOpenRC = %+v, want sshd running", got)
	}
}

// TestParseRC_AHiddenDaemonIsUnknown: where this login cannot see other
// users' processes, rc's "not running" is unknown, not stopped (sweep item
// 11); where it can, it is stopped.
func TestParseRC_AHiddenDaemonIsUnknown(t *testing.T) {
	base := "sshd\n" + serviceSectionMark + "\n/etc/rc.d/sshd\n" + serviceSectionMark + "\n"
	for prefix, want := range map[string]string{"": "stopped", procsHiddenMark + "\n": "unknown"} {
		got := parseRC(base + prefix + "sshd\tsshd is not running.\n")
		if len(got) != 1 || got[0].value.(map[string]any)["state"] != want {
			t.Errorf("with %q: %+v, want %s", prefix, got, want)
		}
	}
}

// TestParseBrew_SkipsHomebrewsOwnLines: a status line Homebrew prints to
// stdout is not a package.
func TestParseBrew_SkipsHomebrewsOwnLines(t *testing.T) {
	got := parseBrew("==> Downloading https://ghcr.io/v2/homebrew/portable-ruby\njq 1.7.1\n")
	if len(got) != 1 || got[0].name != "jq" {
		t.Fatalf("parseBrew = %+v, want jq alone", got)
	}
}
