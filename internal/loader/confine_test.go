//go:build linux

// Package loader: tests of the confinement every program runs under.
package loader

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// The confinement tests: a real program built with pkg/external
// (testdata/extprog's loadertest.goprog.reach) tries to open every secret
// a hostile program would go for, and to signal The Pleiades, and the tests
// assert on what it actually got. Each confined assertion has a control
// run through the same code with confinement switched off, which reaches
// what the confined run could not, so the test can fail.

// reachWorld is a home directory and a project laid out the way an
// operator's are, each holding something a program must not read, plus a
// granted directory it may.
type reachWorld struct {
	home       string
	sshKey     string
	knownHosts string
	store      string
	masterKey  string
	creds      string
	grant      string
	grantFile  string
}

// newReachWorld builds a reachWorld in temporary directories and points
// HOME at its home directory, so both this process and the program it
// starts see that one.
func newReachWorld(t *testing.T) reachWorld {
	t.Helper()
	w := reachWorld{home: t.TempDir(), grant: t.TempDir()}
	project := t.TempDir()
	w.store = filepath.Join(project, ".pleiades")
	w.sshKey = filepath.Join(w.home, ".ssh", "id_ed25519")
	w.knownHosts = filepath.Join(w.home, ".ssh", "known_hosts")
	w.masterKey = filepath.Join(w.store, "master.key")
	w.creds = filepath.Join(w.store, "credentials.yaml")
	w.grantFile = filepath.Join(w.grant, "upload.txt")
	for _, f := range []string{w.sshKey, w.knownHosts, w.masterKey, w.creds, w.grantFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", w.home)
	t.Setenv("PLEIADES_KNOWN_HOSTS", "")
	return w
}

// secrets are the paths a program must never open: the credential store
// and its key, a private SSH key, and this process's own starting
// environment and memory.
func (w reachWorld) secrets() []string {
	return []string{
		w.masterKey,
		w.creds,
		w.sshKey,
		fmt.Sprintf("/proc/%d/environ", os.Getpid()),
		fmt.Sprintf("/proc/%d/mem", os.Getpid()),
	}
}

// probe calls loadertest.goprog.reach against paths and returns the stats
// it reported.
func probe(t *testing.T, d collection.Descriptor, paths []string) map[string]any {
	t.Helper()
	list := make([]any, len(paths))
	for i, p := range paths {
		list[i] = p
	}
	rc := newRecordingContext()
	if _, err := d.Invoke(t.Context(), rc, newSSHDevice(), map[string]any{"paths": list}); err != nil {
		t.Fatalf("reach: %v", err)
	}
	return rc.stats
}

// dumpable reports this process's dumpable flag.
func dumpable(t *testing.T) int {
	t.Helper()
	v, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// makeDumpable sets this process dumpable again, now and when the test
// ends, so one test's Load cannot decide what the next one observes.
func makeDumpable(t *testing.T) {
	t.Helper()
	set := func() {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	set()
	t.Cleanup(set)
}

// TestConfinement_AProgramReachesOnlyWhatItWasHanded is the confined half:
// every secret is denied, The Pleiades cannot be signalled, and what a program
// legitimately needs (the known_hosts file, a granted path, its own
// temporary directory) still works.
func TestConfinement_AProgramReachesOnlyWhatItWasHanded(t *testing.T) {
	makeDumpable(t)
	// Built before HOME moves, so the go command keeps its own caches.
	dir := installExtprog(t)
	w := newReachWorld(t)
	opts := testOptions()
	opts.ProtectedPaths = []string{w.store}
	opts.ReadPaths = []string{w.grant}
	d := loadOne(t, dir, "loadertest.goprog.reach", opts)

	if got := dumpable(t); got != 0 {
		t.Errorf("after loading a program this process is dumpable (%d), so the program could read its environment", got)
	}

	stats := probe(t, d, append(w.secrets(), w.knownHosts, w.grantFile))
	for _, secret := range w.secrets() {
		if got := stats["open:"+secret]; got == "ok" || !strings.Contains(fmt.Sprint(got), "permission denied") {
			t.Errorf("open %s = %v, want permission denied", secret, got)
		}
	}
	for _, allowed := range []string{w.knownHosts, w.grantFile} {
		if got := stats["open:"+allowed]; got != "ok" {
			t.Errorf("open %s = %v, want ok: a confined program must still reach what it needs", allowed, got)
		}
	}
	if got := stats["write_tmpdir"]; got != "ok" {
		t.Errorf("writing in its own temporary directory = %v, want ok", got)
	}
	if abi, _ := probeLandlock(); abi >= 6 {
		if got := stats["signal_parent"]; got == "ok" {
			t.Error("a confined program signalled the process that started it")
		}
	}
}

// TestConfinement_NeverOnTheMainThread proves confineAndStart never
// restricts the process's main thread. A goroutine only rarely lands
// there, so this does not wait for luck: it runs this test binary again
// with its main goroutine pinned to the main thread (mainThreadProbe), has
// that goroutine start a confined program trying to signal its parent,
// and reads the answer back. Restricting the main thread would put the
// parent inside the program's own domain, and the signal would land.
func TestConfinement_NeverOnTheMainThread(t *testing.T) {
	abi, err := probeLandlock()
	testsupport.Require(t, "landlock-scoping", err == nil && abi >= 6,
		fmt.Sprintf("Landlock ABI %d has no signal scoping, which Linux 6.12 added (ABI 6)", abi))
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), mainThreadProbeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the main-thread probe failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "refused" {
		t.Fatalf("a program confined from the main thread %s its parent (probe output %q)", got, out)
	}
}

// mainThreadProbeEnv switches this test binary into mainThreadProbe.
const mainThreadProbeEnv = "LOADER_TEST_MAIN_THREAD_PROBE"

// init pins the main goroutine to the main thread, in the probe run only,
// so TestMain runs mainThreadProbe on that thread and nowhere else.
func init() {
	if os.Getenv(mainThreadProbeEnv) != "" {
		runtime.LockOSThread()
		testProbes = append(testProbes, mainThreadProbe)
	}
}

// mainThreadProbe is the probe run's whole job: from the main thread,
// start a confined shell that tries to signal this process, and print
// whether it could.
func mainThreadProbe() int {
	if unix.Gettid() != unix.Getpid() {
		fmt.Println("the probe is not on the main thread")
		return 1
	}
	abi, err := probeLandlock()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	cmd := exec.Command("/bin/sh", "-c", "kill -0 $PPID 2>/dev/null && echo signalled || echo refused")
	cmd.Stdout = os.Stdout
	errc := make(chan error, 1)
	confineAndStart(cmd, systemRules, abi, errc)
	if err := <-errc; err != nil {
		fmt.Println(err)
		return 1
	}
	_ = cmd.Wait()
	return 0
}

// TestConfinement_TheControlReachesEverything runs the same program
// through the same proxy with confinement off. It opens every secret the
// confined run could not (except /proc/<pid>/mem, which Yama already
// guards on this kind of host), which is what makes the confined test's
// denials evidence rather than a program that failed for some other
// reason.
func TestConfinement_TheControlReachesEverything(t *testing.T) {
	makeDumpable(t)
	dir := installExtprog(t)
	w := newReachWorld(t)
	opts := testOptions()
	opts.unconfined = true
	d := loadOne(t, dir, "loadertest.goprog.reach", opts)

	if got := dumpable(t); got != 1 {
		t.Errorf("an unconfined load changed this process's dumpable flag to %d", got)
	}
	stats := probe(t, d, w.secrets())
	for _, secret := range w.secrets() {
		if strings.HasSuffix(secret, "/mem") {
			continue
		}
		if got := stats["open:"+secret]; got != "ok" {
			t.Errorf("unconfined open %s = %v, want ok: without that, the confined test proves nothing", secret, got)
		}
	}
	if got := stats["signal_parent"]; got != "ok" {
		t.Errorf("unconfined signal_parent = %v, want ok", got)
	}
}

// TestConfinement_ProcessIsProtectedOnlyWhenAProgramWillRun proves the
// cost of protectProcess (no core file, no debugger) is paid only by a
// process that is about to start a program.
func TestConfinement_ProcessIsProtectedOnlyWhenAProgramWillRun(t *testing.T) {
	requireConfinement(t)
	makeDumpable(t)
	t.Cleanup(collection.SnapshotForTest())

	if _, err := Load(t.Context(), programDir(t), testOptions()); err != nil {
		t.Fatalf("Load of an empty directory: %v", err)
	}
	if got := dumpable(t); got != 1 {
		t.Errorf("loading an empty directory made the process not dumpable (%d)", got)
	}

	dir := programDir(t)
	oneMethodProgram(t, dir, "loadertest.protect.run", false, `printf '{}' >&3`)
	if _, err := Load(t.Context(), dir, testOptions()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := dumpable(t); got != 0 {
		t.Errorf("loading a program left the process dumpable (%d)", got)
	}
}

// TestConfinement_RefusedWithoutLandlock proves a kernel without Landlock
// gets a refusal naming the reason, and that nothing ran and nothing
// changed before it.
func TestConfinement_RefusedWithoutLandlock(t *testing.T) {
	makeDumpable(t)
	t.Cleanup(collection.SnapshotForTest())
	real := probeLandlock
	probeLandlock = func() (int, error) { return 0, unix.ENOSYS }
	t.Cleanup(func() { probeLandlock = real })

	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.nolandlock.run", false, `printf '{}' >&3`)
	writeProgram(t, dir, "marker", "#!/bin/sh\ntouch '"+record+"/ran'\n")

	_, err := Load(t.Context(), dir, testOptions())
	if err == nil || !strings.Contains(err.Error(), "does not offer Landlock") {
		t.Fatalf("Load = %v, want a refusal saying the kernel has no Landlock", err)
	}
	if _, statErr := os.Stat(filepath.Join(record, "ran")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a program ran before Load refused")
	}
	if got := dumpable(t); got != 1 {
		t.Errorf("a refused Load changed the process's dumpable flag to %d", got)
	}
	if _, ok := collection.Lookup("loadertest.nolandlock.run"); ok {
		t.Error("a refused Load registered a method")
	}
}

// TestConfinement_WhatMayBeGranted covers checkReach: a granted path and
// the collections directory may not expose a credential store or the
// home directory, however they are spelled.
func TestConfinement_WhatMayBeGranted(t *testing.T) {
	w := newReachWorld(t)
	project := filepath.Dir(w.store)
	link := filepath.Join(t.TempDir(), "innocent")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		grant string
		want  string
	}{
		{name: "a directory beside the store", grant: w.grant},
		{name: "the store itself", grant: w.store, want: "holds credentials"},
		{name: "the project holding the store", grant: project, want: "holds credentials"},
		{name: "inside the store", grant: w.masterKey, want: "holds credentials"},
		{name: "the project through a symlink", grant: link, want: "holds credentials"},
		{name: "the home directory", grant: w.home, want: "home directory"},
		{name: "the root directory", grant: "/", want: "holds credentials"},
		{name: "inside the home directory", grant: filepath.Join(w.home, ".ssh")},
		{name: "a relative path", grant: "files", want: "not absolute"},
		{name: "a missing path", grant: filepath.Join(w.grant, "absent"), want: "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := testOptions()
			o.ProtectedPaths = []string{w.store}
			o.ReadPaths = []string{tc.grant}
			err := checkReach(programDir(t), o)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("checkReach refused %s: %v", tc.grant, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("checkReach(%s) = %v, want a refusal mentioning %q", tc.grant, err, tc.want)
			}
		})
	}

	t.Run("a collections directory inside the store", func(t *testing.T) {
		o := testOptions()
		o.ProtectedPaths = []string{w.store}
		if err := checkReach(w.store, o); err == nil || !strings.Contains(err.Error(), "the collections directory") {
			t.Errorf("checkReach = %v, want the collections directory refused", err)
		}
	})
}
