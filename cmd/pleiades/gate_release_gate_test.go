// Release gate for gate.run and gate.list: through the real binary, against
// a real sshd and a real Docker engine (the engine host fixture, onboarded
// as a linux_server, so the device both runs commands and has an engine).
// Directory checks run on the device under its own timeout command, image
// checks run in a one-shot container from a digest-pinned image with no
// network and a read-only root, a precondition skips, a check of the run
// runs nothing, a failing run says why, and gate.list runs nothing either.
package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// gateRunbook writes a runbook calling method on host with the manifest
// at manifest, and returns its path.
func gateRunbook(t *testing.T, dir, id, host, method, manifest string) string {
	t.Helper()
	path := filepath.Join(dir, "runbooks", id+".yaml")
	body := "id: " + id + "\nhosts: " + host + "\ntasks:\n  - name: " + id + "\n    " + method + ":\n      manifest: " + manifest + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCLI_GateRunsDirectoryAndImageChecks is the release gate.
func TestCLI_GateRunsDirectoryAndImageChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	d.StartContainer(t, containerGateImage, "")
	pinned, err := d.RootExec(ctx, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", containerGateImage)
	pinned = strings.TrimSpace(pinned)
	if err != nil || !strings.Contains(pinned, "@sha256:") {
		t.Fatalf("the fixture's copy of %s has no digest to pin (%q, %v)", containerGateImage, pinned, err)
	}

	// The project on the device, owned by the account the platform logs in
	// as, with a marker a check writes when it runs.
	manifest := `version: 1
defaults: {timeout: 2m}
checks:
  - {name: unit, kind: pure, dir: sub, shell: "test \"$(basename \"$PWD\")\" = sub && touch ran-marker"}
  - {name: argv, kind: pure, command: [uname, -s]}
  - {name: release, kind: pure, image: '` + pinned + `', command: [cat, /etc/alpine-release]}
  - {name: isolated, kind: network, image: '` + pinned + `', shell: "if ip route 2>/dev/null | grep -q default; then echo has a route; exit 1; fi"}
  - {name: readonly, kind: pure, image: '` + pinned + `', shell: "if touch /probe 2>/dev/null; then echo root is writable; exit 1; fi; touch /tmp/scratch"}
  - {name: no-psql, kind: db, shell: "true", requires: {commands: [pleiades-no-such-tool]}}
`
	failing := "version: 1\ndefaults: {timeout: 1m}\nchecks:\n  - {name: broken, kind: pure, shell: \"echo the reason it broke; exit 4\"}\n"
	setup := "mkdir -p /srv/gate/sub && cat > /srv/gate/gates.yaml <<'EOF'\n" + manifest + "EOF\n" +
		"cat > /srv/gate/failing.yaml <<'EOF'\n" + failing + "EOF\n" +
		"chown -R " + testsupport.DockerHostUser + " /srv/gate"
	if out, err := d.RootExec(ctx, "sh", "-c", setup); err != nil {
		t.Fatalf("writing the project: %v\n%s", err, out)
	}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}
	if out, err := runPleiades(t, dir, "add-host", "gatebox", "--type", "linux_server", "--set", "host="+d.Host, "--set", "port="+strconv.Itoa(d.Port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, testsupport.DockerHostPassword, "add-credential", "gatebox", "--username", testsupport.DockerHostUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	onboardJSON(t, dir, env, "gatebox")

	report := func(args ...string) (jsonReport, string) {
		t.Helper()
		out, _ := runPleiadesWithEnv(t, dir, env, append(args, "--json")...)
		// The report is the first document; a line on standard error after
		// it (the outcome, for a person) is not part of it.
		var r jsonReport
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("pleiades %v printed %q: %v", args, out, err)
		}
		return r, out
	}
	marker := "/srv/gate/sub/ran-marker"
	exists := func() bool {
		out, _ := d.RootExec(ctx, "sh", "-c", "test -e "+marker+" && echo yes || echo no")
		return strings.TrimSpace(out) == "yes"
	}

	// A check of the run reads and validates the manifest and runs nothing.
	runbook := gateRunbook(t, dir, "gate", "gatebox", "gate.run", "/srv/gate/gates.yaml")
	checked, out := report("run", runbook, "--mode", "check")
	if checked.Outcome.Unchecked != 1 || exists() {
		t.Fatalf("a check of gate.run: unchecked %d, marker written %v\n%s", checked.Outcome.Unchecked, exists(), out)
	}

	// The run itself.
	ran, out := report("run", runbook)
	if ran.Outcome.ExitCode != 0 || len(ran.Tasks) != 1 {
		t.Fatalf("the gate failed:\n%s", out)
	}
	stats := ran.Tasks[0].Stats
	byName := map[string]map[string]any{}
	checks, _ := stats["checks"].([]any)
	for _, c := range checks {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m
	}
	for name, want := range map[string]string{"unit": "PASS", "argv": "PASS", "release": "PASS", "isolated": "PASS", "readonly": "PASS", "no-psql": "SKIP"} {
		if byName[name]["status"] != want {
			t.Errorf("%s = %v, want %s", name, byName[name], want)
		}
	}
	if stats["summary"] != "5 passed, 0 failed, 1 skipped" || !exists() {
		t.Errorf("summary %v, marker written %v", stats["summary"], exists())
	}

	// A failing run says which check failed and why, in its error.
	failed, out := report("run", gateRunbook(t, dir, "gate-failing", "gatebox", "gate.run", "/srv/gate/failing.yaml"))
	if failed.Outcome.ExitCode == 0 || len(failed.Tasks) != 1 || !strings.Contains(failed.Tasks[0].Error, "broken (pure): exited 4") || !strings.Contains(failed.Tasks[0].Error, "the reason it broke") {
		t.Errorf("a failing gate reported:\n%s", out)
	}

	// gate.list classifies and runs nothing.
	if _, err := d.RootExec(ctx, "rm", "-f", marker); err != nil {
		t.Fatal(err)
	}
	listed, out := report("run", gateRunbook(t, dir, "gate-list", "gatebox", "gate.list", "/srv/gate/gates.yaml"))
	if listed.Outcome.ExitCode != 0 || listed.Tasks[0].Stats["selected"] != float64(6) || exists() {
		t.Errorf("gate.list = selected %v, marker written %v\n%s", listed.Tasks[0].Stats["selected"], exists(), out)
	}

	// The method's reference reaches the command line.
	doc, err := runPleiades(t, dir, "doc", "gate.run")
	if err != nil || !strings.Contains(doc, "Runs a project's gate manifest of named checks") {
		t.Errorf("pleiades doc gate.run: %v\n%s", err, doc)
	}
}
