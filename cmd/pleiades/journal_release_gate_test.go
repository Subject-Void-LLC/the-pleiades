// Package main_test: Phase 40's Crawl-tier Release Gate for the run
// journal.
//
// The claim this proves is narrow and absolute: no value the platform
// obtained from a device can reach a journal entry. Not "no value an
// author annotated", which is why the runbook below carries no
// register_mask and no secret_mask at all. Annotations are irrelevant to
// the guarantee, and a gate whose runbook used one would be proving the
// annotation worked instead.
//
// It runs the real built binary against a real, independently
// implemented sshd, and then reads the journal back with os.ReadFile and
// a plain JSON decode, never through internal/journal. Reading the store
// back through its own writer would prove the writer agreed with itself.
//
// Both halves are asserted. That no sentinel survived is the first, and
// on its own it is satisfied by a journal that recorded nothing at all:
// FAILURE_PATTERNS.md #120 records exactly that, an over-masking change
// that shipped green because every test only checked for absence. So the
// second half asserts the journal still names the FQCNs, the device, the
// failing node and the stage it failed at.
package main_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// The four sentinels, one per route a device-obtained value takes into an
// ActionResult. Each is a distinct string so a failure names which route
// leaked rather than just that something did.
const (
	// sentinelCmd is written into exec.command's cmd parameter, which
	// the method reports back as its own cmd stat.
	sentinelCmd = "SENTINEL-IN-THE-COMMAND-LINE"

	// sentinelStdout sits in a file a task reads, so it arrives in the
	// stdout stat as bytes the device produced.
	sentinelStdout = "SENTINEL-READ-OFF-THE-DEVICE"

	// sentinelBefore is the text file.line.set finds in place, which
	// lands in diff.before.content and in the recorded inverse's own
	// content parameter.
	sentinelBefore = "SENTINEL-THE-FILE-ALREADY-HELD"

	// sentinelAfter is the text file.line.set writes, which lands in
	// diff.after.content.
	sentinelAfter = "SENTINEL-THE-TASK-WROTE-IT"

	// sentinelStderr names a path that does not exist, so the device's
	// own error text quotes it back and it reaches NodeResult.Err raw.
	sentinelStderr = "SENTINEL-ONLY-ON-STANDARD-ERROR"
)

// gateReadPath and gateEditPath are the two files the runbook works
// with on the device.
const (
	gateReadPath = "/tmp/pleiades-journal-gate-read.txt"
	gateEditPath = "/tmp/pleiades-journal-gate-edit.conf"
)

// journalGateRunbook is the runbook the gate runs: maximally dense in
// device-obtained values, and carrying no masking annotation of any
// kind.
//
// The failing task is last on purpose. A failed node ends the walk at
// its own level, and the journal is written at the level barrier before
// the scan that ends it, so the failure is recorded rather than lost.
func journalGateRunbook() string {
	return "id: journal-release-gate\n" +
		"tasks:\n" +
		"  - name: plant-the-values-on-the-device\n" +
		"    exec.shell:\n" +
		"      target: container1\n" +
		"      cmd: \"printf '%s' '" + sentinelStdout + "' > " + gateReadPath +
		" && printf 'setting = %s\\n' '" + sentinelBefore + "' > " + gateEditPath + "\"\n" +
		"  - name: put-a-value-in-the-command-line\n" +
		"    exec.command:\n" +
		"      target: container1\n" +
		"      cmd: \"/bin/echo " + sentinelCmd + "\"\n" +
		"  - name: read-a-value-off-the-device\n" +
		"    exec.command:\n" +
		"      target: container1\n" +
		"      cmd: \"/bin/cat " + gateReadPath + "\"\n" +
		"  - name: edit-a-file-that-holds-a-value\n" +
		"    file.line.set:\n" +
		"      target: container1\n" +
		"      path: " + gateEditPath + "\n" +
		"      regexp: \"^setting\"\n" +
		"      line: \"setting = " + sentinelAfter + "\"\n" +
		"  - name: fail-with-a-value-on-standard-error\n" +
		"    exec.command:\n" +
		"      target: container1\n" +
		"      cmd: \"/bin/cat /tmp/" + sentinelStderr + "\"\n"
}

// journalRecord is the subset of a written entry this gate asserts on.
//
// Decoded into a struct of its own rather than into engine.JournalEntry,
// so the assertion reads the names that are actually on disk. Decoding
// through the producing type would pass just as happily if every tag
// were renamed at once.
type journalRecord struct {
	RunID        string   `json:"run_id"`
	Sequence     int      `json:"sequence"`
	NodeID       string   `json:"node_id"`
	DAGID        string   `json:"dag_id"`
	FQCN         string   `json:"fqcn"`
	TaskName     string   `json:"task_name"`
	DeviceID     string   `json:"device_id"`
	Outcome      string   `json:"outcome"`
	FailureStage string   `json:"failure_stage"`
	StatKeys     []string `json:"stat_keys"`
	ParamKeys    []string `json:"param_keys"`
	InverseFQCN  string   `json:"inverse_fqcn"`
	DiffRecorded bool     `json:"diff_recorded"`

	// Collection and InverseSpecDigest are the identity of the Collection
	// the task's method came from and the digest of its undo's declaration
	// (Phase 136b).
	Collection struct {
		Group     string `json:"group"`
		Version   string `json:"version"`
		Program   bool   `json:"program"`
		Manifests string `json:"manifests"`
	} `json:"collection"`
	InverseSpecDigest string `json:"inverse_spec_digest"`
}

// readJournal returns every byte the journal directory holds and every
// record decoded out of it.
//
// The raw bytes are returned alongside the records because the absence
// assertion has to run over the bytes. A sentinel that reached a field
// this struct does not name would be invisible to a decoded view, and
// the fields it does not name are exactly where a leak would hide.
func readJournal(t *testing.T, projectDir string) (string, []journalRecord) {
	t.Helper()
	dir := filepath.Join(projectDir, ".pleiades", "journal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the run wrote no journal directory at %s: %v", dir, err)
	}

	var raw strings.Builder
	var records []journalRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) // #nosec G304 -- a path built from this test's own t.TempDir
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		raw.Write(data)
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			var rec journalRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("a journal line is not valid JSON: %v\n%s", err, line)
			}
			records = append(records, rec)
		}
	}
	if len(records) == 0 {
		t.Fatal("the journal directory holds no records at all, so every assertion below would pass by examining nothing")
	}
	return raw.String(), records
}

// TestCLI_JournalHoldsNoDeviceValue is the Release Gate.
func TestCLI_JournalHoldsNoDeviceValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the journal Release Gate container test in short mode")
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	realKey := captureRealHostKey(t, addr)
	line := knownhosts.Line([]string{addr}, realKey)
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", "container1", "--type", "linux_server",
		"--set", "host="+host, "--set", "port="+strconv.Itoa(port)); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", "container1",
		"--username", releaseGateSSHUser, "--password", releaseGateSSHPassword); err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}

	runbook := filepath.Join(dir, "runbooks", "journal.yaml")
	if err := os.WriteFile(runbook, []byte(journalGateRunbook()), 0o600); err != nil {
		t.Fatalf("failed to write the fixture runbook: %v", err)
	}

	// The run is expected to fail: its last task fails on purpose, so
	// that a failing node's own entry is part of what is asserted.
	//
	// Run with --verbose, which prints each task's own stats, because
	// that output is this gate's control. An absence assertion is only
	// worth something if the value was really there to be absent, and a
	// runbook that quietly failed to obtain any of its sentinels would
	// satisfy the leak check perfectly.
	out, runErr := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/journal.yaml", "--verbose")
	if runErr == nil {
		t.Fatalf("expected the run to fail on its last task, got success:\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("expected the last task to report FAILED, got:\n%s", out)
	}

	// The control. These same values must be visible in the platform's
	// own verbose output, which is what proves the run genuinely obtained
	// them and that the journal's silence below is a decision rather than
	// an accident. The decision this phase rests on says exactly this:
	// the journal cannot answer what the device said, and --verbose
	// still can.
	for name, sentinel := range map[string]string{
		"the command line":      sentinelCmd,
		"a value read off disk": sentinelStdout,
		"the file's old text":   sentinelBefore,
		"the file's new text":   sentinelAfter,
	} {
		if !strings.Contains(out, sentinel) {
			t.Fatalf("%s never reached the platform at all (%q absent from --verbose output), so the leak check below would pass by proving nothing:\n%s",
				name, sentinel, out)
		}
	}

	raw, records := readJournal(t, dir)

	// Half one: no value the device produced, and no value that reached
	// the device, survived into the journal.
	for name, sentinel := range map[string]string{
		"the command line":      sentinelCmd,
		"a value read off disk": sentinelStdout,
		"the file's old text":   sentinelBefore,
		"the file's new text":   sentinelAfter,
		"the device's stderr":   sentinelStderr,
	} {
		if strings.Contains(raw, sentinel) {
			t.Errorf("%s reached the journal: %q appears in the written bytes", name, sentinel)
		}
	}
	// The stored credential is not one of the four routes, and it is
	// checked anyway: it is the value whose disclosure would matter most.
	if strings.Contains(raw, releaseGateSSHPassword) {
		t.Error("the stored password reached the journal")
	}

	// Half two: the journal still says what happened. This is the
	// assertion FAILURE_PATTERNS.md #120 records as the one missing when
	// over-masking shipped and passed every test it had.
	byName := make(map[string]journalRecord, len(records))
	for _, rec := range records {
		byName[rec.TaskName] = rec
	}

	for _, want := range []struct{ task, fqcn string }{
		{"put-a-value-in-the-command-line", "exec.command"},
		{"read-a-value-off-the-device", "exec.command"},
		{"edit-a-file-that-holds-a-value", "file.line.set"},
		{"fail-with-a-value-on-standard-error", "exec.command"},
	} {
		rec, ok := byName[want.task]
		if !ok {
			t.Errorf("the journal has no record for task %q", want.task)
			continue
		}
		if rec.FQCN != want.fqcn {
			t.Errorf("task %q recorded fqcn %q, want %q", want.task, rec.FQCN, want.fqcn)
		}
		if rec.DeviceID == "" {
			t.Errorf("task %q recorded no device id", want.task)
		}
		if rec.DAGID != "journal-release-gate" {
			t.Errorf("task %q recorded dag_id %q, want the runbook's own id", want.task, rec.DAGID)
		}
		if rec.RunID == "" {
			t.Errorf("task %q recorded no run id", want.task)
		}
	}

	// The failing task names its outcome and the stage it failed at,
	// which is the whole reason an operator would open this file.
	failed := byName["fail-with-a-value-on-standard-error"]
	if failed.Outcome != "failed" {
		t.Errorf("the failing task recorded outcome %q, want %q", failed.Outcome, "failed")
	}
	if failed.FailureStage != "action" {
		t.Errorf("the failing task recorded failure_stage %q, want %q", failed.FailureStage, "action")
	}

	// The read task names the stat keys its method declares, without
	// carrying any of their values.
	read := byName["read-a-value-off-the-device"]
	if !containsAll(read.StatKeys, "stdout", "rc", "cmd") {
		t.Errorf("the read task recorded stat keys %v, want stdout, rc and cmd named", read.StatKeys)
	}

	// The editing task recorded that a diff exists and what would undo
	// it, which is the pair a rollback engine eventually reads.
	edit := byName["edit-a-file-that-holds-a-value"]
	if !edit.DiffRecorded {
		t.Error("the file.line.set task did not record that a diff exists")
	}
	if edit.InverseFQCN == "" {
		t.Error("the file.line.set task recorded no inverse fqcn, so nothing names what would undo it")
	}

	// Each task names the Collection its method came from (Phase 136b): its
	// first-party group, the build that ran it, and the digest of the
	// group's manifests as this same build registers them, which the test
	// binary links too; and the undo names the digest of its declaration.
	assertJournalCollectionIdentity(t, records, edit)

	// And the whole run is ordered and attributable.
	for i, rec := range records {
		if rec.Sequence != i+1 {
			t.Errorf("record %d carries sequence %d: the run's own order is not recoverable from the file", i, rec.Sequence)
			break
		}
	}
}

// assertJournalCollectionIdentity holds every record of a method to the
// identity of its first-party group at the version `pleiades version --json`
// reports, and edit's undo to the digest of file.line.set's declaration of
// it, both computed here from the registry the test binary links.
func assertJournalCollectionIdentity(t *testing.T, records []journalRecord, edit journalRecord) {
	t.Helper()
	out, err := runPleiades(t, t.TempDir(), "version", "--json")
	if err != nil {
		t.Fatalf("version --json: %v\n%s", err, out)
	}
	var report clireport.VersionReport
	if err := json.Unmarshal([]byte(out), &report); err != nil || report.Version == "" {
		t.Fatalf("version --json gave no version (%v):\n%s", err, out)
	}
	for _, rec := range records {
		group, ok := collection.FirstPartyGroupOf(rec.FQCN)
		if !ok {
			t.Fatalf("task %q ran %s, which is in no first-party group", rec.TaskName, rec.FQCN)
		}
		digest, err := collection.GroupDigest(group.Name)
		if err != nil {
			t.Fatal(err)
		}
		got := rec.Collection
		if got.Group != group.Name || got.Program || got.Version != report.Version || got.Manifests != digest {
			t.Errorf("task %q (%s) recorded collection %+v, want group %s at %s with digest %s",
				rec.TaskName, rec.FQCN, got, group.Name, report.Version, digest)
		}
	}

	emitter, ok := collection.Lookup("file.line.set")
	if !ok {
		t.Fatal("file.line.set is not registered in the test binary")
	}
	target, ok := collection.Lookup(edit.InverseFQCN)
	if !ok {
		t.Fatalf("the edit's undo %q is not registered", edit.InverseFQCN)
	}
	var want string
	for _, spec := range emitter.Manifest.Reversibility.Inverses {
		if spec.FQCN == edit.InverseFQCN {
			want = collection.UndoDigest(spec, fragment.ByName(target))
		}
	}
	if want == "" {
		t.Fatalf("file.line.set declares no undo through %s, so its digest is not checkable here", edit.InverseFQCN)
	}
	if edit.InverseSpecDigest != want {
		t.Errorf("the edit's undo recorded inverse_spec_digest %q, want %q", edit.InverseSpecDigest, want)
	}
}

// containsAll reports whether haystack holds every needle.
func containsAll(haystack []string, needles ...string) bool {
	present := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		present[h] = true
	}
	for _, n := range needles {
		if !present[n] {
			return false
		}
	}
	return true
}

// TestCLI_RunFailsBeforeAnyOutputWhenTheJournalCannotBeOpened proves the
// journal's fail-closed decision lands where it should.
//
// The journal is deliberately fail-closed: one that cannot be written
// records nothing, silently, which is worse than a run that refuses.
// Where the refusal happens is then part of the design rather than an
// accident. This asserts it happens before the command prints anything,
// so a read-only project directory never produces a run that announces a
// plan and then abandons it.
func TestCLI_RunFailsBeforeAnyOutputWhenTheJournalCannotBeOpened(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test depends on")
	}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	// Made read-only AFTER init, so everything the runbook needs already
	// exists and the journal is the only thing that cannot be created.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the project directory read-only: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("restoring the project directory: %v", err)
		}
	})

	out, err := runPleiades(t, dir, "run", "runbooks/sample.yaml")
	if err == nil {
		t.Fatalf("run succeeded against a project directory it cannot journal into:\n%s", out)
	}
	if !strings.Contains(out, "run journal") {
		t.Errorf("the failure does not say the journal is what failed:\n%s", out)
	}
	for _, printed := range []string{"plan for", "executing:"} {
		if strings.Contains(out, printed) {
			t.Errorf("the command printed %q before refusing, so it announced work it never attempted:\n%s", printed, out)
		}
	}
}
