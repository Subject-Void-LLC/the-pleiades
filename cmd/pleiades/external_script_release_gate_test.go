// Package main_test: the Release Gate for a script as an external Collection
// (forge new-external --script): a script scaffolded into a program, built
// by the program's own README, approved, and run through the real pleiades
// binary and its loader, confined.
package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// scriptGateMethod is the method the gate's program provides.
const scriptGateMethod = "acme.probe.run"

// scriptGateProgram is the program's file name: the method name with its
// dots as hyphens, which the README builds it under.
const scriptGateProgram = "acme-probe-run"

// TestCLI_AScriptRunsThroughTheLoader is the Release Gate. The script runs
// once with no device, under the loader's confinement: it writes into its
// own temporary directory and cannot read a file outside it that this test
// can, until an operator grants that file's directory
// (PLEIADES_COLLECTIONS_READ_PATHS), which is the control. What it prints comes back as the task's stats, and a
// check reports it unchecked, with the reason its wrapper declares, rather
// than running it.
func TestCLI_AScriptRunsThroughTheLoader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the script Collection Release Gate in short mode")
	}
	need.POSIXShell(t)

	project := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "outside-the-confinement.txt")
	if err := os.WriteFile(marker, []byte("readable by this test, and by nothing the loader confines\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(marker); err != nil {
		t.Fatalf("the control: this test cannot read %s itself: %v", marker, err)
	}

	work := t.TempDir()
	script := filepath.Join(work, "probe.sh")
	body := "input=$(cat)\n" +
		"if cat '" + marker + "' >/dev/null 2>&1; then read=yes; else read=no; fi\n" +
		"if printf 'x' > \"$TMPDIR/scratch\"; then wrote=yes; else wrote=no; fi\n" +
		"printf '{\"changed\": true, \"stats\": {\"input\": %s, \"read_outside\": \"%s\", \"wrote_tmp\": \"%s\"}}\\n' \"$input\" \"$read\" \"$wrote\"\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "PLEIADES_COLLECTIONS_DIR": collections}

	// A relative interpreter is refused before anything is written.
	if out, err := runPleiadesWithEnv(t, work, env, "forge", "new-external", scriptGateMethod, "--script", script, "--interpreter", "sh", "--dir", "refused"); err == nil || !strings.Contains(out, "not an absolute") {
		t.Fatalf("a relative interpreter was accepted: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "refused")); err == nil {
		t.Error("the refused scaffold left a directory behind")
	}

	if out, err := runPleiadesWithEnv(t, work, env, "forge", "new-external", scriptGateMethod, "--script", script, "--interpreter", need.ShellPath, "--dir", "prog"); err != nil {
		t.Fatalf("forge new-external --script: %v\n%s", err, out)
	}
	bin := buildScaffoldedProgram(t, filepath.Join(work, "prog"), scriptGateProgram)
	installed := filepath.Join(collections, scriptGateProgram)
	program, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, program, 0o700); err != nil { // #nosec G306 -- a program the loader runs must be executable by its owner
		t.Fatal(err)
	}

	for _, args := range [][]string{{"init"}} {
		if out, err := runPleiadesWithEnv(t, project, env, args...); err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, out)
		}
	}

	if out, err := runPleiadesWithEnv(t, project, env, "doc", scriptGateMethod); err == nil || !strings.Contains(out, "is not approved to run") {
		t.Fatalf("an unapproved script program was loaded: %v\n%s", err, out)
	}
	approved, err := runPleiadesWithEnv(t, project, env, "collection", "approve", scriptGateProgram, "--yes")
	if err != nil && strings.Contains(approved, "Landlock") {
		t.Skipf("needs Landlock to load a program: %s", approved)
	}
	if err != nil || !strings.Contains(approved, scriptGateMethod) || !strings.Contains(approved, "approved "+scriptGateProgram+" (sha256:") {
		t.Fatalf("collection approve: %v\n%s", err, approved)
	}

	runbook := "# What this does: runs the gate's script once, beside The Pleiades.\n" +
		"# Blast radius: none; the method acts on no device.\n" +
		"# Changes: a file in the script's own temporary directory, removed after it.\n" +
		"# Undo: nothing to undo.\n" +
		"id: script-gate\n" +
		"tasks:\n" +
		"  - name: run-the-script\n" +
		"    " + scriptGateMethod + ":\n" +
		"    register: probe\n"
	if err := os.WriteFile(filepath.Join(project, "runbooks", "script.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, project, env, "validate", "runbooks/script.yaml"); err != nil {
		t.Fatalf("validate refused a runbook calling the script: %v\n%s", err, out)
	}

	rep, raw, code := runScriptGateJSON(t, project, env, "run", "runbooks/script.yaml")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("the run exited %d:\n%s", code, raw)
	}
	task := rep.Tasks[0]
	if task.Method != scriptGateMethod || task.Status != "changed" || task.Device != "" {
		t.Errorf("task = %s on %q, %s; want %s changed, on no device:\n%s", task.Method, task.Device, task.Status, scriptGateMethod, raw)
	}
	if task.Stats["wrote_tmp"] != "yes" {
		t.Errorf("the script could not write its own temporary directory:\n%s", raw)
	}
	if task.Stats["read_outside"] != "no" {
		t.Errorf("the script read %s, outside its confinement:\n%s", marker, raw)
	}
	if input, ok := task.Stats["input"].(map[string]any); !ok || len(input) != 0 {
		t.Errorf("the script's stdin was %v, want the task's params, none here", task.Stats["input"])
	}

	// The control: granted the directory, the same script reads the file,
	// so the "no" above is the confinement's answer and not the script's.
	granted := map[string]string{"PLEIADES_COLLECTIONS_READ_PATHS": outside}
	for k, v := range env {
		granted[k] = v
	}
	rep, raw, code = runScriptGateJSON(t, project, granted, "run", "runbooks/script.yaml")
	if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Stats["read_outside"] != "yes" {
		t.Errorf("granted %s, the script still could not read the file in it (exit %d):\n%s", outside, code, raw)
	}

	// A check never runs the script, and says why.
	rep, raw, code = runScriptGateJSON(t, project, env, "run", "runbooks/script.yaml", "--mode", "check")
	if code == 0 || rep.Outcome.Unchecked != 1 || !strings.Contains(raw, "cannot tell a script that only reads from one that writes") {
		t.Errorf("a check exited %d with %d unchecked, want the script unchecked, with its reason:\n%s", code, rep.Outcome.Unchecked, raw)
	}
}

// runScriptGateJSON runs the binary with --json and env over this process's
// environment, and decodes the one report on its standard output.
func runScriptGateJSON(t *testing.T, dir string, env map[string]string, args ...string) (jsonReport, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("pleiades did not run: %v", err)
		}
		code = exitErr.ExitCode()
	}
	var rep jsonReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("%v: standard output is not a report: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return rep, stdout.String() + stderr.String(), code
}

// pinnedToolchain matches the GOTOOLCHAIN a scaffolded README's build line
// pins.
var pinnedToolchain = regexp.MustCompile(`GOTOOLCHAIN=\S+`)

// buildScaffoldedProgram builds the program forge wrote into dir by its
// README's offline build block, as an author with no network does, and
// returns the binary. The checkout path is this one, and the pinned
// toolchain is the local Go, so it runs offline on any machine; the
// externalscaffold package's own gates hold the README's lines to that.
func buildScaffoldedProgram(t *testing.T, dir, binary string) string {
	t.Helper()
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, block := range strings.Split(string(readme), "~~~sh\n")[1:] {
		body, _, _ := strings.Cut(block, "~~~")
		if candidate := strings.Split(strings.TrimSpace(body), "\n"); strings.HasPrefix(candidate[0], "go mod edit -replace ") {
			lines = candidate
			break
		}
	}
	if lines == nil {
		t.Fatalf("the README has no offline build block:\n%s", readme)
	}
	for _, line := range lines {
		line = strings.ReplaceAll(line, "/path/to/the-pleiades", root)
		line = pinnedToolchain.ReplaceAllString(line, "GOTOOLCHAIN=local")
		cmd := exec.Command("sh", "-c", line) // #nosec G204 -- the generated README's own command, the thing under test
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the README's step %q failed offline: %v\n%s", line, err, out)
		}
	}
	return filepath.Join(dir, binary)
}
