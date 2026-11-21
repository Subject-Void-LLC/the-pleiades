// The Crawl tier's Release Gate for reading stored facts in a condition
// (Phase 41's read half): runbooks run through the real binary against the
// real sshd container the facts gate uses. A device never gathered fails the
// task reading a fact, and the device shows nothing ran; a gather then a read
// in one run decides on the gathered value, with its facts held for a
// secret_mask; the next run decides on the stored value, numbers included;
// no fact value reaches the run's text, its JSON report or its journal, a
// condition failing on one included; a check reads what a gather observes;
// and with no master key validation allows only what the run gathers first.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// factsWhenRunbook is a runbook named id on container1 holding tasks.
func factsWhenRunbook(id, tasks string) string {
	return "# Decides on container1's facts and leaves a marker where a condition held.\n" +
		"# Blast radius: the gate's one container.\n# Changes: marker files under /tmp.\n# Undo: none needed; the markers are the test's.\n" +
		"id: " + id + "\nhosts: container1\ntasks:\n" + tasks
}

// marker is a task that touches /tmp/<name> when cond holds.
func marker(name, cond string) string {
	return "  - name: mark " + name + "\n    when: " + cond + "\n    exec.command:\n      cmd: touch /tmp/" + name + "\n"
}

func TestCLI_FactsInConditionsReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the facts-in-conditions Release Gate container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)
	g := newRollbackGate(t, container, host, port)
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(g.dir, "runbooks", name+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return "runbooks/" + name + ".yaml"
	}
	exists := func(name string) bool {
		t.Helper()
		return g.device(t, "test -e /tmp/"+name+" && echo yes || echo no") == "yes"
	}

	// Never gathered: the reading task fails, naming the key and to gather,
	// and the device shows it did not run.
	never := write("never", factsWhenRunbook("never", marker("fw-never", `facts["os.kernel.name"] == "linux"`)))
	if out, code := g.pleiades(t, "validate", never); code != 0 {
		t.Fatalf("validate of a read in a project with its key: exit %d\n%s", code, out)
	}
	out, code := g.pleiades(t, "run", never)
	if code == 0 || !strings.Contains(out, `facts["os.kernel.name"]`) || !strings.Contains(out, "says whether the device has it") {
		t.Fatalf("a read on a device never gathered: exit %d\n%s", code, out)
	}
	if exists("fw-never") {
		t.Error("the task reading an unknown fact ran on the device")
	}

	// A gather then a read in one run, its facts held for a secret_mask:
	// the condition reads what the gather found, and a false one skips.
	gathered := write("gathered", factsWhenRunbook("gathered", "  - name: gather\n    register: g\n    facts.gather: {}\n"+
		marker("fw-gathered", `facts["os.kernel.name"] == "linux"`)+marker("fw-wrong", `facts["os.kernel.name"] == "windows"`)+
		"  - name: mask\n    secret_mask: {register: g, fields: [host.nodename]}\n    exec.command:\n      cmd: \"true\"\n"))
	if out, code := g.pleiades(t, "run", gathered); code != 0 {
		t.Fatalf("a gather then a read: exit %d\n%s", code, out)
	}
	if !exists("fw-gathered") || exists("fw-wrong") {
		t.Errorf("after a gather then a read: the linux marker %v, the windows marker %v; want only linux", exists("fw-gathered"), exists("fw-wrong"))
	}

	// The next run, with no gather, reads the stored values; a number
	// compares as a number. Its report, text and journal carry no value,
	// a condition that fails on one included.
	kernel := g.device(t, "uname -r")
	if strings.Trim(kernel, "0123456789abcdef.-") == "" {
		t.Fatalf("the kernel release %q is all hex, so a search for it could match a digest", kernel)
	}
	stored := write("stored", factsWhenRunbook("stored", marker("fw-stored", `facts["os.kernel.name"] == "linux"`)+
		marker("fw-memory", `facts["hardware.memory_mb"] > 0`)+marker("fw-skip", `facts["os.kernel.release"] == "nope"`)+
		"  - name: fail on the value\n    when_cel: '{\"a\": 1}[facts[\"os.kernel.release\"]] == 1'\n    exec.command:\n      cmd: touch /tmp/fw-failed\n"))
	text, code := g.pleiades(t, "run", stored)
	if code == 0 || !strings.Contains(text, "CEL's own message is not shown") {
		t.Errorf("a condition failing on a stored value: exit %d\n%s", code, text)
	}
	if !exists("fw-stored") || !exists("fw-memory") || exists("fw-skip") || exists("fw-failed") {
		t.Errorf("the stored run's markers: kernel %v, memory %v, skip %v, failed %v; want the first two only",
			exists("fw-stored"), exists("fw-memory"), exists("fw-skip"), exists("fw-failed"))
	}
	asJSON, _ := g.pleiades(t, "run", stored, "--json")
	if !strings.Contains(asJSON, `"tasks"`) {
		t.Fatalf("run --json printed no report:\n%s", asJSON)
	}
	journals, _ := filepath.Glob(filepath.Join(g.dir, ".pleiades", "journal", "*.jsonl"))
	var journalText strings.Builder
	for _, j := range journals {
		raw, err := os.ReadFile(j) // #nosec G304 -- the test's own project
		if err != nil {
			t.Fatal(err)
		}
		journalText.Write(raw)
	}
	if journalText.Len() == 0 {
		t.Fatal("no journal was written, so a search of it proves nothing")
	}
	for where, body := range map[string]string{"the run's text": text, "its JSON report": asJSON, "the journal": journalText.String()} {
		if strings.Contains(body, kernel) {
			t.Errorf("%s holds the stored kernel release %q", where, kernel)
		}
	}
	// A check reads what its read-only gather observes, and stores nothing.
	checked := write("checked", factsWhenRunbook("checked", "  - name: gather\n    facts.gather: {}\n"+
		"  - name: on linux\n    when: facts[\"os.kernel.name\"] == \"linux\"\n    noop: {}\n"))
	before := factLines(t, g.dir)
	if out, code := g.pleiades(t, "run", checked, "--mode", "check"); code != 0 || strings.Contains(out, "unchecked") {
		t.Errorf("a check of a gather then a read: exit %d\n%s", code, out)
	}
	if after := factLines(t, g.dir); after != before {
		t.Errorf("a check stored %d fact lines", after-before)
	}

	// With no master key nothing stored can be read: validation refuses a
	// read nothing before it gathers, naming the key, and allows a gather
	// then a read.
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(g.dir, ".pleiades", "master.key")
	if err := os.Rename(keyFile, keyFile+".away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(keyFile+".away", keyFile) })
	if out, code := g.pleiades(t, "validate", never); code == 0 || !strings.Contains(out, "PLEIADES_MASTER_KEY") {
		t.Errorf("validate of a read with no key: exit %d\n%s", code, out)
	}
	if out, code := g.pleiades(t, "validate", gathered); code != 0 {
		t.Errorf("validate of a gather then a read with no key: exit %d\n%s", code, out)
	}
}

// factLines counts the lines of every fact file under dir's store.
func factLines(t *testing.T, dir string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, ".pleiades", "facts", "*.jsonl"))
	n := 0
	for _, f := range files {
		raw, err := os.ReadFile(f) // #nosec G304 -- the test's own project
		if err != nil {
			t.Fatal(err)
		}
		n += strings.Count(string(raw), "\n")
	}
	return n
}
