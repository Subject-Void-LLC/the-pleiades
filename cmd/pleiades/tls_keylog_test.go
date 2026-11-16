// Tests for --tls-keylog through the real binary, against a gNMI server and
// a RESTCONF server over real TLS: run and adhoc write the NSS key log lines
// of each session they open (TLS 1.3's traffic secrets from gNMI, TLS 1.2's
// CLIENT_RANDOM from a RESTCONF server that offers no more), into a new file,
// mode 0600, with one warning at the start and in the report; an existing
// file is refused before any device is contacted; nothing is written without
// the flag, whatever the environment says; and no line of the key log
// reaches anything the run prints or leaves in the project.
package main_test

import (
	"crypto/tls"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
)

// The throwaway logins of the two test servers.
const (
	keyLogGNMIPassword     = "gnmi-Keylog-4"
	keyLogRESTCONFPassword = "rc-Keylog-5"
)

// keyLogRESTCONFRunbook reads rc1's RESTCONF discovery.
const keyLogRESTCONFRunbook = `# Reads what rc1's RESTCONF server says about itself, writing the run's TLS
# key log for a capture.
# Blast radius: rc1 alone. Changes: nothing on the device. Undo: none needed.
id: restconf-keylog
hosts: rc1
tasks:
  - name: Discover the device's YANG modules over RESTCONF
    net.restconf.facts:
`

// keyLogReport is the part of a --json report these tests read.
type keyLogReport struct {
	Warnings []struct {
		Kind    string `json:"kind"`
		When    string `json:"when"`
		Message string `json:"message"`
	} `json:"warnings"`
	Outcome struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
	} `json:"outcome"`
}

// keyLogProject makes a project holding g1, a generic_gnmi device served by
// a gNMI test server over TLS 1.3, and rc1, a generic_restconf device served
// by a RESTCONF test server that offers no more than TLS 1.2, each pinned,
// given its login and onboarded. It returns the project and both servers.
func keyLogProject(t *testing.T) (string, *gnmitest.Server, *restconftest.Server) {
	t.Helper()
	g := gnmitest.New(t, gnmitest.Options{Username: "admin", Password: keyLogGNMIPassword})
	rc := restconftest.New(t, restconftest.Options{Username: "admin", Password: keyLogRESTCONFPassword, MaxTLSVersion: tls.VersionTLS12})
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "g1", "--type", "generic_gnmi", "--set", "target=" + g.Addr(), "--set", "tls_fingerprint=" + devicetls.Fingerprint(g.Certificate())},
		{"add-host", "rc1", "--type", "generic_restconf", "--set", "base_url=" + rc.URL, "--set", "tls_fingerprint=" + devicetls.Fingerprint(rc.Certificate())},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// The passwords on standard input, never argv.
	for name, password := range map[string]string{"g1": keyLogGNMIPassword, "rc1": keyLogRESTCONFPassword} {
		if out, err := runPleiadesWithStdin(t, dir, password+"\n", "add-credential", name, "--username", "admin", "--password-stdin"); err != nil {
			t.Fatalf("add-credential %s: %v\n%s", name, err, out)
		}
		if res := onboardJSON(t, dir, nil, name); res.State != "active" {
			t.Fatalf("onboarding %s: %+v", name, res)
		}
	}
	writeFile(t, dir, "runbooks/restconf.yaml", keyLogRESTCONFRunbook)
	return dir, g, rc
}

// runSplit runs the binary in dir, returning standard output and everything
// it printed, both streams, and the error it ended with.
func runSplit(t *testing.T, dir string, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...) // #nosec G204 -- the test's own binary
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return string(out), string(out) + stderr.String(), err
}

// keyLogLabels counts each label of the key log at path, failing on a line
// that is not label, client random and secret.
func keyLogLabels(t *testing.T, path string) (map[string]int, []string) {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- the test's own file
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var values []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || len(fields[1]) != 64 {
			t.Fatalf("the key log holds a line that is not an NSS key log line (%d fields)", len(fields))
		}
		counts[fields[0]]++
		values = append(values, fields[1], fields[2])
	}
	return counts, values
}

// TestCLI_TLSKeyLogWritesTheRunsSessions runs net.gnmi.facts through adhoc
// and net.restconf.facts through run, each with its own key log, and holds
// each file to its sessions' NSS lines and mode 0600, each run to one warning
// naming the file, and every key log value to appearing nowhere the runs
// printed or left in the project.
func TestCLI_TLSKeyLogWritesTheRunsSessions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a platform that honors a file's mode: --tls-keylog is refused on Windows, as run_keylog_internal_test.go holds")
	}
	dir, _, _ := keyLogProject(t)
	logs := t.TempDir()
	gnmiLog, restconfLog := filepath.Join(logs, "gnmi.keylog"), filepath.Join(logs, "restconf.keylog")

	stdout, printedGNMI, err := runSplit(t, dir, nil, "adhoc", "g1", "net.gnmi.facts", "--tls-keylog", gnmiLog, "--json")
	if err != nil {
		t.Fatalf("adhoc: %v\n%s", err, printedGNMI)
	}
	var rep keyLogReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("adhoc --json printed %q: %v", stdout, err)
	}
	var warned int
	for _, w := range rep.Warnings {
		if w.Kind == "tls_keylog" {
			warned++
			if w.When != "start" || !strings.Contains(w.Message, gnmiLog) || !strings.Contains(w.Message, "decrypts") {
				t.Errorf("the key log warning %+v", w)
			}
		}
	}
	if warned != 1 || rep.Outcome.ExitCode != 0 {
		t.Errorf("the report holds %d key log warnings and ended %+v, want one and a complete run", warned, rep.Outcome)
	}

	printedRESTCONF, err := runPleiadesWithEnv(t, dir, nil, "run", "runbooks/restconf.yaml", "--tls-keylog", restconfLog)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, printedRESTCONF)
	}
	if n := strings.Count(printedRESTCONF, "WARNING: this run writes a TLS key log to "+restconfLog); n != 1 {
		t.Errorf("the text run printed the key log warning %d times, want once:\n%s", n, printedRESTCONF)
	}

	gnmiLabels, gnmiValues := keyLogLabels(t, gnmiLog)
	for _, label := range []string{"CLIENT_HANDSHAKE_TRAFFIC_SECRET", "SERVER_HANDSHAKE_TRAFFIC_SECRET", "CLIENT_TRAFFIC_SECRET_0", "SERVER_TRAFFIC_SECRET_0"} {
		if gnmiLabels[label] == 0 {
			t.Errorf("the gNMI run's key log has no %s line: %v", label, gnmiLabels)
		}
	}
	restconfLabels, restconfValues := keyLogLabels(t, restconfLog)
	if restconfLabels["CLIENT_RANDOM"] == 0 || len(restconfLabels) != 1 {
		t.Errorf("the RESTCONF run's key log holds %v, want TLS 1.2's CLIENT_RANDOM lines alone", restconfLabels)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{gnmiLog, restconfLog} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %v, want 0600", path, info.Mode().Perm())
			}
		}
	}

	// No client random or secret reaches what either run printed, or any file
	// the runs left in the project: its journal, its facts, its state.
	values := append(gnmiValues, restconfValues...)
	places := map[string]string{"what adhoc printed": printedGNMI, "what run printed": printedRESTCONF}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path) // #nosec G304 -- the test's own project
		places[path] = string(raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for place, text := range places {
		for _, v := range values {
			if strings.Contains(text, v) {
				t.Errorf("%s holds a value of the key log", place)
				break
			}
		}
	}
}

// TestCLI_TLSKeyLogRefusesAFileThatExists names an existing file, and a link
// to one, as the key log of run and of adhoc: each is refused naming it, no
// device is contacted, and the file is unchanged.
func TestCLI_TLSKeyLogRefusesAFileThatExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a platform that honors a file's mode: --tls-keylog is refused on Windows, as run_keylog_internal_test.go holds")
	}
	dir, g, rc := keyLogProject(t)
	existing := filepath.Join(t.TempDir(), "existing.keylog")
	if err := os.WriteFile(existing, []byte("do not touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{existing}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "link.keylog")
		if err := os.Symlink(existing, link); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, link)
	}
	before, restconfBefore := len(g.RPCs()), len(rc.Paths())
	for _, path := range paths {
		for _, args := range [][]string{
			{"adhoc", "g1", "net.gnmi.facts", "--tls-keylog", path},
			{"run", "runbooks/restconf.yaml", "--tls-keylog", path},
		} {
			out, err := runPleiadesWithEnv(t, dir, nil, args...)
			if err == nil || !strings.Contains(out, path) || !strings.Contains(out, "already exists") {
				t.Errorf("%v: %v\n%s", args, err, out)
			}
		}
	}
	if after := len(g.RPCs()); after != before {
		t.Errorf("the gNMI server answered %d calls while every key log was refused, want none", after-before)
	}
	if raw, _ := os.ReadFile(existing); string(raw) != "do not touch\n" { // #nosec G304 -- the test's own file
		t.Errorf("the existing file now holds %q", raw)
	}
	// An empty name, standard output and a second file are refused as the
	// flag is read; rollback does not take the flag at all.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"adhoc", "g1", "net.gnmi.facts", "--tls-keylog="}, "needs a file name"},
		{[]string{"adhoc", "g1", "net.gnmi.facts", "--tls-keylog", "-"}, "never to standard output"},
		{[]string{"adhoc", "g1", "net.gnmi.facts", "--tls-keylog", filepath.Join(t.TempDir(), "a"), "--tls-keylog", filepath.Join(t.TempDir(), "b")}, "given twice"},
		{[]string{"rollback", "some-run", "--tls-keylog", filepath.Join(t.TempDir(), "rollback.keylog")}, "flag provided but not defined: -tls-keylog"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, tc.args...); err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("%v: %v, want it refused with %q:\n%s", tc.args, err, tc.want, out)
		}
	}
	if after := len(g.RPCs()); after != before {
		t.Errorf("the gNMI server answered %d calls while every key log was refused, want none", after-before)
	}
	if after := len(rc.Paths()); after != restconfBefore {
		t.Errorf("the RESTCONF server answered %d requests while every key log was refused, want none", after-restconfBefore)
	}
}

// TestCLI_NoKeyLogWithoutTheFlag runs the same calls without --tls-keylog,
// with SSLKEYLOGFILE set as other tools read it: nothing is written there or
// anywhere in the project, and no report warns of a key log.
func TestCLI_NoKeyLogWithoutTheFlag(t *testing.T) {
	dir, _, _ := keyLogProject(t)
	sslKeyLog := filepath.Join(t.TempDir(), "sslkeylogfile")
	env := []string{"SSLKEYLOGFILE=" + sslKeyLog}
	stdout, printed, err := runSplit(t, dir, env, "adhoc", "g1", "net.gnmi.facts", "--json")
	if err != nil {
		t.Fatalf("adhoc: %v\n%s", err, printed)
	}
	if _, printedRun, err := runSplit(t, dir, env, "run", "runbooks/restconf.yaml"); err != nil {
		t.Fatalf("run: %v\n%s", err, printedRun)
	}
	var rep keyLogReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("adhoc --json printed %q: %v", stdout, err)
	}
	for _, w := range rep.Warnings {
		if w.Kind == "tls_keylog" {
			t.Errorf("a run without the flag warned %q", w.Message)
		}
	}
	if _, err := os.Lstat(sslKeyLog); !os.IsNotExist(err) {
		t.Errorf("SSLKEYLOGFILE was written: %v", err)
	}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path) // #nosec G304 -- the test's own project
		if text := string(raw); strings.Contains(text, "TRAFFIC_SECRET_0 ") || strings.Contains(text, "CLIENT_RANDOM ") {
			t.Errorf("%s holds key log lines", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
