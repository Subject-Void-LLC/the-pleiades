// The release gate for converted fact reads (Phase 41h): a playbook that
// gathers facts and reads Ansible's names converts with no blocked task,
// through the real binary, and the runbook decides per host as Ansible does:
// a Debian device runs the task gated on ansible_os_family == "Debian" and
// skips the one gated on "Alpine", and an Alpine device in the same run does
// the opposite; a condition on a fact Alpine does not give (its codename)
// runs there as Ansible's comparison with its NA placeholder would; a
// condition on ansible_distribution_major_version | int orders Jinja's
// integer on each host; and a setup task's filter, written as the native
// keys its patterns match, gathers on both.
package main_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// factsPlaybook gathers facts (gather_facts left at its default, on) and
// gates two tasks on Ansible's family, one for each device's.
const factsPlaybook = `- hosts: web
  tasks:
    - name: mark a Debian host
      ansible.builtin.copy:
        dest: /tmp/pleiades-debian-marker
        content: "debian\n"
      when: ansible_os_family == "Debian"
    - name: mark an Alpine host
      ansible.builtin.copy:
        dest: /tmp/pleiades-alpine-marker
        content: "alpine\n"
      when: ansible_os_family == "Alpine"
    - name: mark every host whose release is not one that does not exist
      ansible.builtin.copy:
        dest: /tmp/pleiades-release-marker
        content: "ran\n"
      when: ansible_distribution_release != "no-such-release"
    - name: mark a host whose major version is at least 10
      ansible.builtin.copy:
        dest: /tmp/pleiades-major-high-marker
        content: "high\n"
      when: ansible_distribution_major_version | int >= 10
    - name: mark a host whose major version is under 10
      ansible.builtin.copy:
        dest: /tmp/pleiades-major-low-marker
        content: "low\n"
      when: 10 > ansible_distribution_major_version | int
    - name: gather the distribution facts alone
      ansible.builtin.setup:
        filter: [ansible_os_family, "ansible_distribution*"]
`

func TestMigratePlaybook_AFactReadDecidesPerHostAsAnsibleDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the converted fact read gate, which builds and runs a real Debian and a real Alpine sshd, in short mode")
	}
	debian := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	alpine := startTestdataDevice(t, "alpine-sshd", alpineGatePassword, nil)
	devices := []struct {
		name, classes string
		d             *testdataDevice
	}{{"deb", "linux_server,debian_family", debian}, {"alp", "linux_server", alpine}}

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, dev := range devices {
		lines = append(lines, knownhosts.Line([]string{dev.d.addr()}, testsupport.CaptureHostKey(t, dev.d.addr(), "root", dev.d.password)))
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, home, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, dev := range devices {
		args := []string{"add-host", dev.name, "--classify", dev.classes, "--set", "host=" + dev.d.host, "--set", "port=" + strconv.Itoa(dev.d.port), "--tag", "web"}
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if out, err := runPleiadesWithStdin(t, dir, dev.d.password+"\n", "add-credential", dev.name, "--username", "root", "--password-stdin"); err != nil {
			t.Fatalf("add-credential %s: %v\n%s", dev.name, err, out)
		}
	}

	writeFile(t, dir, "site.yml", factsPlaybook)
	stdout, stderr, _ := runMigrate(t, dir, "site.yml")
	if !strings.Contains(stdout, ", 0 blocked") {
		t.Fatalf("the conversion blocked a task:\n%s%s", stdout, stderr)
	}
	runbook, err := os.ReadFile(filepath.Join(dir, "runbooks", "site.yaml")) // #nosec G304 -- a file this test's conversion wrote
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runbook), "facts.gather") || !strings.Contains(string(runbook), `["os.family"] == "debian"`) {
		t.Fatalf("the runbook does not gather and read the native family:\n%s", runbook)
	}
	// The setup task's filter is the native keys its patterns match, never
	// the Ansible pattern, which facts.gather would refuse on every host.
	var parsed struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(runbook, &parsed); err != nil {
		t.Fatalf("the runbook does not parse: %v\n%s", err, runbook)
	}
	var filter []any
	for _, task := range parsed.Tasks {
		if task["name"] == "gather the distribution facts alone" {
			gather, _ := task["facts.gather"].(map[string]any)
			filter, _ = gather["filter"].([]any)
		}
	}
	if want := []any{"os.codename", "os.distribution", "os.edition", "os.family", "os.version", "os.version_major"}; !reflect.DeepEqual(filter, want) {
		t.Fatalf("the setup task's filter is %v, want %v:\n%s", filter, want, runbook)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/site.yaml"); err != nil {
		t.Fatalf("pleiades run: %v\n%s", err, out)
	}

	// Alpine has no codename: the absent fact is unequal to anything, as
	// Ansible's NA is, so the task runs there too rather than failing.
	for _, d := range []*testdataDevice{debian, alpine} {
		if got, code := d.exec(t, "cat", "/tmp/pleiades-release-marker"); code != 0 || got != "ran" {
			t.Errorf("a device's release marker = %q (exit %d), want the != task to have run on both", got, code)
		}
	}
	// Jinja's int of the major version: Debian bookworm's 12 is at least 10,
	// Alpine 3.22's 3 is under it.
	for _, tc := range []struct {
		d          *testdataDevice
		own, other string
	}{{debian, "major-high", "major-low"}, {alpine, "major-low", "major-high"}} {
		if got, code := tc.d.exec(t, "cat", "/tmp/pleiades-"+tc.own+"-marker"); code != 0 || got == "" {
			t.Errorf("the %s marker = %q (exit %d), want the task gated on | int to have run", tc.own, got, code)
		}
		if got, code := tc.d.exec(t, "cat", "/tmp/pleiades-"+tc.other+"-marker"); code == 0 {
			t.Errorf("a device holds the %s marker %q: | int did not decide per host", tc.other, got)
		}
	}
	for _, tc := range []struct {
		d          *testdataDevice
		own, other string
	}{{debian, "debian", "alpine"}, {alpine, "alpine", "debian"}} {
		if got, code := tc.d.exec(t, "cat", "/tmp/pleiades-"+tc.own+"-marker"); code != 0 || got != tc.own {
			t.Errorf("the %s device's own marker = %q (exit %d), want the task gated on its family to have run", tc.own, got, code)
		}
		if got, code := tc.d.exec(t, "cat", "/tmp/pleiades-"+tc.other+"-marker"); code == 0 {
			t.Errorf("the %s device holds the %s marker %q: the condition did not decide per host", tc.own, tc.other, got)
		}
	}
}
