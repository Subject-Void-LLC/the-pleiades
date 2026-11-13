// Tests for what validate says of a grant that grants nothing: one whose
// program is gone, and one whose definition changed after it was made.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// The runbooks these tests validate against w1: one calling the program's
// method, which needs the grant, and one calling a built-in, which does not.
const (
	widgetRunbook = "id: widgets\nhosts: w1\ntasks:\n  - name: read the widget\n    clitest.widget.read: {}\n"
	pingRunbook   = "id: ping\nhosts: w1\ntasks:\n  - name: reach it\n    net.ssh.ping: {}\n"
)

// capabilityProgramWith is capabilityProgram with extra (JSON members and a
// trailing comma) added to its capability's definition.
func capabilityProgramWith(extra string) string {
	return strings.Replace(capabilityProgram, `{"name":"clitest.widget_api",`, `{"name":"clitest.widget_api",`+extra, 1)
}

// writeTestRunbook writes body as dir's runbook name and returns its path.
func writeTestRunbook(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, "runbooks", name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertWidget asserts that w1 holds clitest.widget_api, as an operator
// does, then forgets every registration the program's load made, as the
// command's process ending would.
func assertWidget(t *testing.T, dir string) {
	t.Helper()
	restoreCaps, restoreMethods := capability.SnapshotForTest(), collection.SnapshotForTest()
	var out string
	err := captureRun(t, &out, func() error {
		return runGrantCapability([]string{"w1", "clitest.widget_api", "--dir", dir, "--set", "endpoint=https://w", "--reason", "the lab widget controller"})
	})
	if err != nil && strings.Contains(err.Error(), "Landlock") {
		t.Skipf("needs Landlock to load a program: %v", err)
	}
	if err != nil {
		t.Fatalf("grant-capability = %v\n%s", err, out)
	}
	restoreCaps()
	restoreMethods()
	resetLoadedCollections()
}

// replaceProgram writes program as the widgets program and approves it, as
// an operator installing a new build does.
func replaceProgram(t *testing.T, program string) {
	t.Helper()
	path := filepath.Join(os.Getenv(collectionsDirEnv), "widgets")
	if err := os.WriteFile(path, []byte(program), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	var out string
	if err := captureRun(t, &out, func() error { return runCollection([]string{"approve", "widgets", "--yes"}) }); err != nil {
		t.Fatalf("approving the new build: %v\n%s", err, out)
	}
}

// validateOnce runs validate on path in a process of its own, as far as
// the registries go, and returns what it printed and its error.
func validateOnce(t *testing.T, dir, path string) (string, error) {
	t.Helper()
	restoreCaps, restoreMethods := capability.SnapshotForTest(), collection.SnapshotForTest()
	defer func() {
		restoreCaps()
		restoreMethods()
		resetLoadedCollections()
	}()
	var out string
	err := captureRun(t, &out, func() error { return runValidate([]string{path, "--dir", dir}) })
	return out, err
}

// TestValidate_WarnsOfAGrantNoLoadedProgramDefines: with the program that
// defined it gone, a device's grant grants nothing, and validating a
// runbook that reaches the device warns, naming the device and the
// capability, without failing.
func TestValidate_WarnsOfAGrantNoLoadedProgramDefines(t *testing.T) {
	dir := grantProject(t)
	assertWidget(t, dir)
	t.Setenv(collectionsDirEnv, "")
	out, err := validateOnce(t, dir, writeTestRunbook(t, dir, "ping.yaml", pingRunbook))
	if err != nil {
		t.Fatalf("validate = %v\n%s", err, out)
	}
	if want := "[warning] grants: device w1's grant of clitest.widget_api grants nothing here: no loaded program defines clitest.widget_api"; !strings.Contains(out, want) {
		t.Errorf("validate printed:\n%s\nwant the warning %q", out, want)
	}
}

// TestRun_ReportsTheGrantWarningsValidationGives: `pleiades run` reports
// the same grant warning validate does, as a run warning of kind grant
// naming the device, before it touches anything; here the run is refused
// for an undeclared parameter, so no device is contacted.
func TestRun_ReportsTheGrantWarningsValidationGives(t *testing.T) {
	dir := grantProject(t)
	assertWidget(t, dir)
	t.Setenv(collectionsDirEnv, "")
	path := writeTestRunbook(t, dir, "refused.yaml", "id: refused\nhosts: w1\ntasks:\n  - name: reach it\n    net.ssh.ping:\n      not_a_parameter: 1\n")
	restoreCaps, restoreMethods := capability.SnapshotForTest(), collection.SnapshotForTest()
	defer func() {
		restoreCaps()
		restoreMethods()
		resetLoadedCollections()
	}()
	var out string
	if err := captureRun(t, &out, func() error { return runRunbook([]string{path, "--dir", dir, "--json"}) }); err == nil {
		t.Fatalf("a run of a runbook with an undeclared parameter was not refused:\n%s", out)
	}
	var rep struct {
		Warnings []struct {
			Kind    string   `json:"kind"`
			When    string   `json:"when"`
			Message string   `json:"message"`
			Devices []string `json:"devices"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("run --json printed no report: %v\n%s", err, out)
	}
	for _, w := range rep.Warnings {
		if w.Kind == "grant" && w.When == "start" && strings.Contains(w.Message, "no loaded program defines clitest.widget_api") && len(w.Devices) == 1 && w.Devices[0] == "w1" {
			return
		}
	}
	t.Errorf("run --json warnings %+v, want the grant warning on w1", rep.Warnings)
}

// TestValidate_ALapsedGrantNamesWhyAndWhatToDo: a new build of the program
// whose definition changed only in prose keeps the grant; one whose author
// bumped the definition's revision withdraws it, and validate refuses the
// runbook needing it, naming the capability, the program, the revision and
// how to grant it again.
func TestValidate_ALapsedGrantNamesWhyAndWhatToDo(t *testing.T) {
	dir := grantProject(t)
	assertWidget(t, dir)
	path := writeTestRunbook(t, dir, "widgets.yaml", widgetRunbook)
	if out, err := validateOnce(t, dir, path); err != nil {
		t.Fatalf("validate with the build that granted = %v\n%s", err, out)
	}

	replaceProgram(t, capabilityProgramWith(`"doc":"Reaches a widget controller, now in better words.",`))
	if out, err := validateOnce(t, dir, path); err != nil {
		t.Fatalf("validate after a change of prose = %v\n%s", err, out)
	}

	replaceProgram(t, capabilityProgramWith(`"revision":1,`))
	out, err := validateOnce(t, dir, path)
	if err == nil {
		t.Fatalf("validate passed after the definition's revision was bumped:\n%s", out)
	}
	for _, want := range []string{"clitest.widget_api", `program "widgets"`, "revision 1", "assert it again", "pleiades grant-capability w1 clitest.widget_api"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate printed:\n%s\nwant it to say %q", out, want)
		}
	}
}
