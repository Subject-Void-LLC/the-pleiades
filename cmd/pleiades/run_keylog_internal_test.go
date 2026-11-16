// Tests for --tls-keylog in process: the flag as it is read, the refusals
// made before anything loads, the file the run creates as it starts and the
// warning it gives, and the refusal of a file the run cannot create, which
// ends the run before any task.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTLSKeyLogFlag_ReadsOneNewFile holds the flag to one file name: given
// once it is the run's key log, and an empty name, standard output and a
// second name are refused as the flag is read.
func TestTLSKeyLogFlag_ReadsOneNewFile(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	path := filepath.Join(t.TempDir(), "run.keylog")
	project := t.TempDir()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	f := addTLSKeyLogFlag(fs)
	if err := fs.Parse([]string{"--tls-keylog", path}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.option(project); err != nil || got != path {
		t.Errorf("option() = %q, %v, want %q", got, err, path)
	}
	none := &tlsKeyLogFlag{}
	if got, err := none.option(project); err != nil || got != "" {
		t.Errorf("no flag: option() = %q, %v", got, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tls-keylog="}, "needs a file name"},
		{[]string{"--tls-keylog", "-"}, "never to standard output"},
		{[]string{"--tls-keylog", path, "--tls-keylog", path + "2"}, "given twice"},
	} {
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(new(strings.Builder))
		addTLSKeyLogFlag(fs)
		if err := fs.Parse(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
}

// TestTLSKeyLogFlag_RefusesBeforeLoading holds option to refusing an
// existing file, and a name the system cannot look up, before the run loads
// anything, as run and adhoc both end.
func TestTLSKeyLogFlag_RefusesBeforeLoading(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	dir := t.TempDir()
	keys := t.TempDir()
	existing := filepath.Join(keys, "existing.keylog")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &tlsKeyLogFlag{path: existing}
	if _, err := f.option(dir); err == nil || !strings.Contains(err.Error(), existing) || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing file: %v", err)
	}
	if runtime.GOOS != "windows" {
		// A name beneath a regular file is not a missing name: the lookup
		// itself fails, and the run does not start on a guess.
		under := &tlsKeyLogFlag{path: filepath.Join(existing, "run.keylog")}
		if _, err := under.option(dir); err == nil || strings.Contains(err.Error(), "already exists") {
			t.Errorf("a name beneath a file: %v", err)
		}
	}
	for name, run := range map[string]func() error{
		"run": func() error { return runRunbook([]string{"missing.yaml", "--dir", dir, "--tls-keylog", existing}) },
		"adhoc": func() error {
			return runAdhoc([]string{"-", "net.pcap.summary", "--dir", dir, "--tls-keylog", existing})
		},
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("%s: %v, want the existing key log refused first", name, err)
		}
	}
}

// TestTLSKeyLog_ARunCreatesItAndWarns runs a method through the whole
// pipeline with --tls-keylog: the run creates the file, mode 0600, and its
// report carries one tls_keylog warning naming it, given at the start. A
// file the run cannot create ends the run before its task, with the reason.
func TestTLSKeyLog_ARunCreatesItAndWarns(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "wan.pcap"), emptyCapture(), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "run.keylog")
	var err error
	out := captureStdout(t, func() {
		err = runAdhoc([]string{"-", "net.pcap.summary", "src=captures/wan.pcap", "--dir", dir, "--json", "--tls-keylog", path})
	})
	if err != nil {
		t.Fatalf("adhoc: %v\n%s", err, out)
	}
	var rep runReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("adhoc --json printed %q: %v", out, err)
	}
	var warned int
	for _, w := range rep.Warnings {
		if w.Kind == "tls_keylog" && w.When == "start" && strings.Contains(w.Message, path) {
			warned++
		}
	}
	if warned != 1 {
		t.Errorf("warnings %+v, want one tls_keylog warning at the start naming %s", rep.Warnings, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the key log's mode is %v, want 0600", info.Mode().Perm())
	}
	// No TLS session ran, so the file holds nothing.
	if info.Size() != 0 {
		t.Errorf("a run with no TLS session wrote %d bytes to the key log", info.Size())
	}

	// A directory this user cannot write passes the flag's check, which
	// cannot know that without writing, and is refused as the run starts.
	if os.Geteuid() == 0 {
		return // root writes into any directory, so this case cannot be made
	}
	locked := t.TempDir()
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	unwritable := filepath.Join(locked, "run.keylog")
	out = captureStdout(t, func() {
		err = runAdhoc([]string{"-", "net.pcap.summary", "src=captures/wan.pcap", "--dir", dir, "--json", "--tls-keylog", unwritable})
	})
	if err == nil || !strings.Contains(err.Error(), "creating the TLS key log") {
		t.Fatalf("a key log the run cannot create: %v\n%s", err, out)
	}
	var refused runReport
	if err := json.Unmarshal([]byte(out), &refused); err != nil {
		t.Fatalf("adhoc --json printed %q: %v", out, err)
	}
	if refused.Outcome.Status != "error" || len(refused.Tasks) != 1 || refused.Tasks[0].Status != statusNotRun {
		t.Errorf("the run refused its key log and reported %+v with tasks %+v, want its one task not run", refused.Outcome, refused.Tasks)
	}
	closeTLSKeyLog(nil)
}

// keyLogRefusedHere reports whether this platform refuses --tls-keylog
// (Windows, which ignores a file's mode), having held the flag's check to
// refusing it saying so, so a test of what the flag does ends with that
// check there rather than a skip.
func keyLogRefusedHere(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return false
	}
	f := &tlsKeyLogFlag{path: filepath.Join(t.TempDir(), "run.keylog")}
	if _, err := f.option(t.TempDir()); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("--tls-keylog on Windows: %v, want it refused", err)
	}
	return true
}

// TestTLSKeyLogFlag_RefusesAMissingDirectoryBeforeLoading refuses a key log
// whose directory does not exist, or is a file, as the flag is read: run
// before the runbook is looked for, and adhoc, given a call that would run,
// before its pipeline begins, so --json prints no report at all where a
// refusal at the run's start (after the journal and the concurrency record
// are begun) would print one.
func TestTLSKeyLogFlag_RefusesAMissingDirectoryBeforeLoading(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "wan.pcap"), emptyCapture(), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
	}{
		{filepath.Join(t.TempDir(), "missing", "run.keylog"), "does not exist"},
		{filepath.Join(file, "run.keylog"), "is not a directory"},
	} {
		// The runbook does not exist either, and the key log is what is refused.
		if err := runRunbook([]string{"missing.yaml", "--dir", dir, "--tls-keylog", tc.path}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("run with --tls-keylog %s: %v, want %q before the runbook is read", tc.path, err, tc.want)
		}
		var err error
		out := captureStdout(t, func() {
			err = runAdhoc([]string{"-", "net.pcap.summary", "src=captures/wan.pcap", "--dir", dir, "--json", "--tls-keylog", tc.path})
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("adhoc with --tls-keylog %s: %v, want %q", tc.path, err, tc.want)
		}
		if out != "" {
			t.Errorf("adhoc with --tls-keylog %s printed a report, so it was refused only once its run had begun:\n%s", tc.path, out)
		}
	}
}

// TestTLSKeyLogFlag_RefusesWhatExternalCollectionsRead refuses a key log in
// the external Collections directory, below it, and in each path they are
// granted, since those third-party programs run during the run and read
// them; with no Collections loaded, a granted path is an ordinary one.
func TestTLSKeyLogFlag_RefusesWhatExternalCollectionsRead(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	project, collections, grantA, grantB, outside := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(collections, "program"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(collectionsReadPathsEnv, grantA+string(filepath.ListSeparator)+grantB)
	if got, err := (&tlsKeyLogFlag{path: filepath.Join(grantA, "run.keylog")}).option(project); err != nil || got == "" {
		t.Errorf("a granted path with no Collections loaded: %q, %v", got, err)
	}
	t.Setenv(collectionsDirEnv, collections)
	for p, want := range map[string]string{
		filepath.Join(collections, "run.keylog"):            "inside the external Collections directory",
		filepath.Join(collections, "program", "run.keylog"): "inside the external Collections directory",
		filepath.Join(grantA, "run.keylog"):                 "inside a path external Collections may read",
		filepath.Join(grantB, "run.keylog"):                 "inside a path external Collections may read",
	} {
		if _, err := (&tlsKeyLogFlag{path: p}).option(project); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", p, err, want)
		}
	}
	ok := filepath.Join(outside, "run.keylog")
	if got, err := (&tlsKeyLogFlag{path: ok}).option(project); err != nil || got != ok {
		t.Errorf("a key log outside every kept-out directory: %q, %v", got, err)
	}
}

// TestTLSKeyLogFlag_RefusesAFileInsideTheProject refuses a key log the run's
// methods could read back out of the project (file.copy's src) and send to a
// device: a name inside the project, below it, and a name outside reached
// through a link into it; a name outside the project is taken.
func TestTLSKeyLogFlag_RefusesAFileInsideTheProject(t *testing.T) {
	if keyLogRefusedHere(t) {
		return
	}
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	refused := []string{
		filepath.Join(project, "run.keylog"),
		filepath.Join(project, "captures", "run.keylog"),
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(outside, "into-project")
		if err := os.Symlink(project, link); err != nil {
			t.Fatal(err)
		}
		refused = append(refused, filepath.Join(link, "run.keylog"))
	}
	for _, p := range refused {
		f := &tlsKeyLogFlag{path: p}
		if _, err := f.option(project); err == nil || !strings.Contains(err.Error(), "inside the project") {
			t.Errorf("%s: %v, want it refused as inside the project", p, err)
		}
	}
	ok := filepath.Join(outside, "run.keylog")
	if got, err := (&tlsKeyLogFlag{path: ok}).option(project); err != nil || got != ok {
		t.Errorf("a key log outside the project: %q, %v", got, err)
	}
	if err := runAdhoc([]string{"-", "net.pcap.summary", "--dir", project, "--tls-keylog", refused[0]}); err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Errorf("adhoc with a key log inside the project: %v", err)
	}
}
