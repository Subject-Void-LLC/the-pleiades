// The Crawl tier's Release Gate for rollback (Phase 40): the real
// `pleiades` binary against a real sshd container. Every claim about the
// device is read back through the container itself (docker exec), never
// through pleiades, so the rollback cannot vouch for its own work.
//
//   - G1, the Adversarial item: a run changes three things, then fails on a
//     read-only step; a file lands out of band in the directory it made;
//     rollback restores what it can, refuses to delete the directory it no
//     longer owns alone, and a second rollback after the stray file is
//     gone resumes and finishes, leaving the device as it was before.
//   - G2: every refusal happens before any device is touched.
//   - G3: a rollback: list added to the runbook after the run undoes a
//     change no method records an undo for.
//   - G4: a recorded identifier is the only value a journal holds.
//   - G5: a run killed mid-level leaves no seal, and rollback says so.
//   - G6: --mode check changes nothing and journals nothing.
package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"golang.org/x/crypto/ssh/knownhosts"
)

// rollbackGate is one project against the shared sshd container.
type rollbackGate struct {
	container testcontainers.Container
	home, dir string
}

// newRollbackGate makes a project with container1 on the shared sshd.
func newRollbackGate(t *testing.T, container testcontainers.Container, host string, port int) *rollbackGate {
	t.Helper()
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &rollbackGate{container: container, home: home, dir: t.TempDir()}
	for _, args := range [][]string{
		{"init"},
		{"add-host", "container1", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "container1", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, code := g.pleiades(t, args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	return g
}

// pleiades runs the real binary in the project, returning its output and
// exit status.
func (g *rollbackGate) pleiades(t *testing.T, args ...string) (string, int) {
	t.Helper()
	out, err := runPleiadesWithHome(t, g.dir, g.home, args...)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, 0
	case errors.As(err, &exitErr):
		return out, exitErr.ExitCode()
	default:
		t.Fatalf("pleiades %v did not run: %v", args, err)
		return out, -1
	}
}

// run writes runbook into the project and runs it, returning its run id
// and exit status.
func (g *rollbackGate) run(t *testing.T, name, runbook string) (string, int) {
	t.Helper()
	path := filepath.Join(g.dir, "runbooks", name+".yaml")
	if err := os.WriteFile(path, []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := g.pleiades(t, "run", "runbooks/"+name+".yaml")
	m := regexp.MustCompile(`run ([0-9a-f-]{36}), journal `).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the run named no run id (exit %d):\n%s", code, out)
	}
	return m[1], code
}

// device runs script on the container as the test user and returns its
// output: the independent read of what is on the device.
func (g *rollbackGate) device(t *testing.T, script string) string {
	t.Helper()
	code, r, err := g.container.Exec(context.Background(), []string{"su", "-s", "/bin/sh", releaseGateSSHUser, "-c", script}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("exec %q: %v", script, err)
	}
	out, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("exec %q: exit %d\n%s", script, code, out)
	}
	return strings.TrimSpace(string(out))
}

// journals counts the project's journal files.
func (g *rollbackGate) journals(t *testing.T) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.dir, ".pleiades", "journal", "*.jsonl"))
	return len(files)
}

// noLoginSince fails when sshd has logged a login since it had logged
// before: a refused rollback must not have reached the device at all. The
// log is written asynchronously, so it is read after it has had time to
// catch up; a login a refusal made would be there by then.
func (g *rollbackGate) noLoginSince(t *testing.T, before int) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if n := sshdLogins(t, g.container); n != before {
		t.Errorf("sshd logged %d login(s) during a refused rollback", n-before)
	}
}

// gateReport is the part of the --json report the gates read.
type gateReport struct {
	RunID string `json:"run_id"`
	Tasks []struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Status string `json:"status"`
	} `json:"tasks"`
	Rollback *struct {
		Levels [][]struct {
			Node   string         `json:"node"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		} `json:"levels"`
		Problems []struct {
			Node   string `json:"node"`
			Reason string `json:"reason"`
			Accept string `json:"accept"`
		} `json:"problems"`
		Left          []struct{ Node string } `json:"left"`
		AlreadyUndone []struct{ Node string } `json:"already_undone"`
	} `json:"rollback"`
	Outcome struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
	} `json:"outcome"`
}

// report runs the binary with --json and decodes the one document it
// prints on standard output, whatever its exit status.
func (g *rollbackGate) report(t *testing.T, args ...string) gateReport {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...) // #nosec G204 -- the test's own binary
	cmd.Dir = g.dir
	cmd.Env = append(os.Environ(), "HOME="+g.home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	var rep gateReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("pleiades %v --json printed no report: %v\nstdout: %s\nstderr: %s", args, err, out, stderr.String())
	}
	return rep
}

// methods lists a rollback report's steps as "method node".
func (r gateReport) methods() []string {
	var out []string
	if r.Rollback == nil {
		return nil
	}
	for _, level := range r.Rollback.Levels {
		for _, s := range level {
			out = append(out, s.Method+" "+s.Node)
		}
	}
	return out
}

func TestCLI_RollbackReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the rollback Release Gate container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)

	t.Run("G1 a partial run is undone, stopped by a stray file, and resumed", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "rm -rf /tmp/rb-g1 && printf 'keep\n' > /tmp/rb-g1-keep && chmod 0644 /tmp/rb-g1-keep")
		const footprint = "stat -c '%a %U' /tmp/rb-g1-keep; cat /tmp/rb-g1-keep; ls -A /tmp/rb-g1 2>/dev/null || echo no-directory"
		start := g.device(t, footprint)
		runID, code := g.run(t, "g1", `id: g1
hosts: container1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/rb-g1
  - name: copy a file into it
    file.copy:
      dest: /tmp/rb-g1/new
      content: "new\n"
  - name: tighten the kept file
    file.permissions:
      path: /tmp/rb-g1-keep
      mode: "0600"
  - name: wait for something that never comes
    wait.path:
      path: /tmp/rb-g1-never
      timeout: 1
`)
		if code != 1 {
			t.Fatalf("the forward run exited %d, want it to fail on its last task", code)
		}
		if got := g.device(t, "stat -c %a /tmp/rb-g1-keep; ls -A /tmp/rb-g1"); got != "600\nnew" {
			t.Fatalf("after the run the device holds %q", got)
		}

		// Something the run did not make lands in its directory.
		g.device(t, "touch /tmp/rb-g1/extra")
		rep := g.report(t, "rollback", runID)
		want := []string{"file.permissions tasks[2]", "file.remove tasks[1]", "file.remove tasks[0]"}
		if got := rep.methods(); !slices.Equal(got, want) {
			t.Errorf("the rollback planned %q, want %q: newest change first, the read-only failure not at all", got, want)
		}
		if rep.Outcome.ExitCode != 1 {
			t.Fatalf("rollback exited %d, want 1: removing a directory holding a file the run did not make must fail", rep.Outcome.ExitCode)
		}
		if got := g.device(t, footprint); got != "644 "+releaseGateSSHUser+"\nkeep\nextra" {
			t.Errorf("after the stopped rollback the device holds %q: want the mode back, the copy gone, the stray file kept", got)
		}

		// The stray file goes; a second rollback resumes where it stopped.
		g.device(t, "rm /tmp/rb-g1/extra")
		rep = g.report(t, "rollback", runID)
		if got := rep.methods(); !slices.Equal(got, []string{"file.remove tasks[0]"}) || rep.Outcome.ExitCode != 0 {
			t.Fatalf("the resumed rollback ran %q and exited %d, want only the directory's undo, exit 0", got, rep.Outcome.ExitCode)
		}
		if n := len(rep.Rollback.AlreadyUndone); n != 2 {
			t.Errorf("the resumed rollback names %d changes already undone, want 2", n)
		}
		if got := g.device(t, footprint); got != start {
			t.Errorf("the device ends as %q, want it as it began: %q", got, start)
		}

		// Undone in full, a third rollback has nothing to do, and says so.
		if out, code := g.pleiades(t, "rollback", runID); code != 1 || !strings.Contains(out, "already been undone") {
			t.Errorf("a third rollback: exit %d\n%s", code, out)
		}
		list, _ := g.pleiades(t, "journal", "list")
		if !strings.Contains(list, "rolled back by") || strings.Count(list, "rollback of "+runID) != 2 {
			t.Errorf("journal list does not show the run and its two rollbacks:\n%s", list)
		}
		// Undoing a rollback is not a rollback.
		for _, run := range g.reportList(t) {
			if run.RollbackOf == runID {
				if out, code := g.pleiades(t, "rollback", run.RunID); code != 1 || !strings.Contains(out, "is a rollback of") {
					t.Errorf("a rollback of a rollback: exit %d\n%s", code, out)
				}
				break
			}
		}
	})

	t.Run("G2 a refusal touches nothing", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "printf 'setting = old\n' > /tmp/rb-g2.conf && rm -rf /tmp/rb-g2")
		runID, _ := g.run(t, "g2", `id: g2
hosts: container1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/rb-g2
  - name: edit a file in place
    file.line.set:
      path: /tmp/rb-g2.conf
      regexp: "^setting"
      line: "setting = new"
`)
		journals := g.journals(t)
		logins := sshdLogins(t, container)
		out, code := g.pleiades(t, "rollback", runID)
		if code != 1 || !strings.Contains(out, "refused, before any device was contacted") || !strings.Contains(out, "--leave tasks[1]") {
			t.Fatalf("a rollback needing a withheld undo: exit %d\n%s", code, out)
		}
		g.noLoginSince(t, logins)
		if got := g.device(t, "cat /tmp/rb-g2.conf; test -d /tmp/rb-g2 && echo dir"); got != "setting = new\ndir" {
			t.Errorf("a refused rollback changed the device: %q", got)
		}
		if g.journals(t) != journals {
			t.Error("a refused rollback wrote a journal")
		}

		// A later run changed the same device: undoing beneath it is refused
		// until named.
		later, _ := g.run(t, "g2l", "id: g2l\nhosts: container1\ntasks:\n  - name: another\n    file.directory:\n      path: /tmp/rb-g2-later\n")
		logins = sshdLogins(t, container)
		out, code = g.pleiades(t, "rollback", runID, "--leave", "tasks[1]")
		if code != 1 || !strings.Contains(out, later) || !strings.Contains(out, "--despite-run "+later) {
			t.Fatalf("a rollback beneath a later run: exit %d\n%s", code, out)
		}
		g.noLoginSince(t, logins)
		if got := g.device(t, "test -d /tmp/rb-g2 && echo dir"); got != "dir" {
			t.Error("a rollback refused for a later run changed the device")
		}

		// Both named: only the edit stays.
		rep := g.report(t, "rollback", runID, "--leave", "tasks[1]", "--despite-run", later)
		if rep.Outcome.ExitCode != 0 || len(rep.Rollback.Left) != 1 || rep.Rollback.Left[0].Node != "tasks[1]" {
			t.Fatalf("with --leave and --despite-run: %+v", rep)
		}
		if got := g.device(t, "cat /tmp/rb-g2.conf; test -d /tmp/rb-g2 && echo dir || echo gone"); got != "setting = new\ngone" {
			t.Errorf("after --leave the device holds %q", got)
		}

		// A command that failed may have done anything: its rollback is
		// incomplete until the operator accepts that.
		failed, _ := g.run(t, "g2b", "id: g2b\nhosts: container1\ntasks:\n  - name: fail\n    exec.command:\n      cmd: /bin/false\n")
		if out, code := g.pleiades(t, "rollback", failed); code != 3 || !strings.Contains(out, "--allow-unknown") {
			t.Errorf("a failed command's rollback: exit %d, want 3\n%s", code, out)
		}
		if out, code := g.pleiades(t, "rollback", failed, "--allow-unknown", "tasks[0]"); code != 0 {
			t.Errorf("with --allow-unknown, exit %d\n%s", code, out)
		}
	})

	t.Run("G2 a journal edited by hand is not trusted", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "rm -rf /tmp/rb-g2t")
		runID, _ := g.run(t, "g2t", "id: g2t\nhosts: container1\ntasks:\n  - name: make\n    file.directory:\n      path: /tmp/rb-g2t\n")
		path := filepath.Join(g.dir, ".pleiades", "journal", runID+".jsonl")
		raw, err := os.ReadFile(path) // #nosec G304 -- the test's own project
		if err != nil {
			t.Fatal(err)
		}
		// The escape is built here, not written into this file: source
		// holds no control characters.
		esc := `\u00` + "1b"
		logins := sshdLogins(t, container)
		for name, edit := range map[string]string{
			"another undo method": strings.Replace(string(raw), `"inverse_fqcn":"file.remove"`, `"inverse_fqcn":"exec.shell"`, 1),
			"an added target":     strings.Replace(string(raw), `"inverse_params":[`, `"inverse_params":[{"key":"target","text":"elsewhere"},`, 1),
			"an escape sequence":  strings.Replace(string(raw), `"text":"/tmp/rb-g2t"`, `"text":"/tmp/rb-g2t`+esc+`[2J"`, 1),
			"an unknown key":      strings.Replace(string(raw), `"inverse_complete":`, `"inverse_extra":1,"inverse_complete":`, 1),
		} {
			if edit == string(raw) {
				t.Fatalf("%s: the edit matched nothing in %s", name, raw)
			}
			if err := os.WriteFile(path, []byte(edit), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, code := g.pleiades(t, "rollback", runID); code != 1 {
				t.Errorf("%s: exit %d, want 1\n%s", name, code, out)
			}
			if got := g.device(t, "test -d /tmp/rb-g2t && echo dir"); got != "dir" {
				t.Fatalf("%s: the device was changed", name)
			}
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		// Others may write it: refused.
		if err := os.Chmod(path, 0o666); err != nil { // #nosec G302 -- the point of the test
			t.Fatal(err)
		}
		if out, code := g.pleiades(t, "rollback", runID); code != 1 {
			t.Errorf("a journal others may write was trusted: exit %d\n%s", code, out)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		real := path + ".real"
		if err := os.Rename(path, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, path); err != nil {
			t.Fatal(err)
		}
		if out, code := g.pleiades(t, "rollback", runID); code != 1 {
			t.Errorf("a journal reached through a symbolic link was trusted: exit %d\n%s", code, out)
		}
		if got := g.device(t, "test -d /tmp/rb-g2t && echo dir"); got != "dir" {
			t.Fatal("the device was changed")
		}
		g.noLoginSince(t, logins)
		// Put back, the same journal is undone: the refusals were the edits.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(real, path); err != nil {
			t.Fatal(err)
		}
		if out, code := g.pleiades(t, "rollback", runID); code != 0 {
			t.Fatalf("the untouched journal: exit %d\n%s", code, out)
		}
		if got := g.device(t, "test -d /tmp/rb-g2t && echo dir || echo gone"); got != "gone" {
			t.Error("the untouched journal's rollback did not undo the directory")
		}
	})

	t.Run("G3 a rollback list written after the run", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "rm -f /tmp/rb-g3")
		task := "  - name: touch a marker\n    exec.command:\n      cmd: /bin/touch /tmp/rb-g3\n"
		runID, code := g.run(t, "g3", "id: g3\nhosts: container1\ntasks:\n"+task)
		if code != 0 {
			t.Fatalf("the forward run exited %d", code)
		}
		if out, code := g.pleiades(t, "rollback", runID); code != 1 || !strings.Contains(out, "rollback: list") {
			t.Fatalf("a command with no undo: exit %d\n%s", code, out)
		}
		// Written now, after the run. The runbook's version does not hash
		// rollback:, so it is still the runbook that ran.
		authored := "id: g3\nhosts: container1\ntasks:\n" + task + "    rollback:\n      - name: remove it\n        exec.command:\n          cmd: /bin/rm -f /tmp/rb-g3\n"
		if err := os.WriteFile(filepath.Join(g.dir, "runbooks", "g3.yaml"), []byte(authored), 0o600); err != nil {
			t.Fatal(err)
		}
		rep := g.report(t, "rollback", runID)
		if got := rep.methods(); rep.Outcome.ExitCode != 0 || !slices.Equal(got, []string{"exec.command tasks[0]"}) {
			t.Fatalf("with the list: exit %d, steps %q", rep.Outcome.ExitCode, got)
		}
		if got := g.device(t, "test -e /tmp/rb-g3 && echo present || echo gone"); got != "gone" {
			t.Errorf("the marker is %s after the authored rollback", got)
		}

		// A runbook changed in what it runs is not the one that ran.
		g.device(t, "touch /tmp/rb-g3")
		again, _ := g.run(t, "g3", authored)
		changed := strings.Replace(authored, "touch a marker", "touch the marker", 1)
		if err := os.WriteFile(filepath.Join(g.dir, "runbooks", "g3.yaml"), []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, code := g.pleiades(t, "rollback", again); code != 1 || !strings.Contains(out, "--runbook") {
			t.Errorf("a rollback whose runbook changed since: exit %d\n%s", code, out)
		}
		if got := g.device(t, "test -e /tmp/rb-g3 && echo present"); got != "present" {
			t.Error("a refused rollback removed the marker")
		}
	})

	t.Run("G4 a recorded identifier is the only value the journal holds", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		const secret = "SENTINEL-OLD-CONTENT-OF-THE-FILE"
		g.device(t, "rm -rf /tmp/rb-g4-SENTINELPATH && printf 'setting = "+secret+"\n' > /tmp/rb-g4.conf")
		runID, _ := g.run(t, "g4", `id: g4
hosts: container1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/rb-g4-SENTINELPATH
  - name: edit a file holding a value
    file.line.set:
      path: /tmp/rb-g4.conf
      regexp: "^setting"
      line: "setting = new"
`)
		if _, code := g.pleiades(t, "rollback", runID, "--leave", "tasks[1]"); code != 0 {
			t.Fatalf("rollback exit %d", code)
		}
		text, _ := g.pleiades(t, "journal", "show", runID)
		if !strings.Contains(text, "undo: file.remove path=/tmp/rb-g4-SENTINELPATH") || !strings.Contains(text, "undo: file.copy dest=/tmp/rb-g4.conf") || !strings.Contains(text, "not recorded in full") {
			t.Errorf("journal show does not say what each change's undo is:\n%s", text)
		}
		show, _ := g.pleiades(t, "journal", "show", runID, "--json")
		all := text + show
		files, _ := filepath.Glob(filepath.Join(g.dir, ".pleiades", "journal", "*"))
		if len(files) != 4 {
			t.Fatalf("want the run's and the rollback's journals and seals, found %q", files)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f) // #nosec G304 -- the test's own project
			if err != nil {
				t.Fatal(err)
			}
			all += string(raw)
			if !strings.HasSuffix(f, ".jsonl") {
				continue
			}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var e map[string]any
				if err := json.Unmarshal([]byte(line), &e); err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				delete(e, "inverse_params")
				rest, _ := json.Marshal(e)
				if strings.Contains(string(rest), "SENTINELPATH") {
					t.Errorf("the recorded path appears outside inverse_params: %s", rest)
				}
			}
		}
		if strings.Contains(all, secret) {
			t.Errorf("the file's prior content reached a journal or journal show")
		}
		if !strings.Contains(all, "SENTINELPATH") {
			t.Error("the recorded path appears nowhere, so the check above proved nothing")
		}
	})

	t.Run("G5 a run killed mid-level leaves no seal", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "rm -rf /tmp/rb-g5")
		if err := os.WriteFile(filepath.Join(g.dir, "runbooks", "g5.yaml"), []byte("id: g5\nhosts: container1\ntasks:\n  - name: make\n    file.directory:\n      path: /tmp/rb-g5\n  - name: hang\n    exec.command:\n      cmd: /bin/sleep 60\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binPath, "run", "runbooks/g5.yaml") // #nosec G204 -- the test's own binary
		cmd.Dir = g.dir
		cmd.Env = append(os.Environ(), "HOME="+g.home)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var runID string
		for deadline := time.Now().Add(60 * time.Second); runID == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			files, _ := filepath.Glob(filepath.Join(g.dir, ".pleiades", "journal", "*.jsonl"))
			for _, f := range files {
				if raw, _ := os.ReadFile(f); strings.Contains(string(raw), "file.directory") { // #nosec G304 -- the test's own project
					runID = strings.TrimSuffix(filepath.Base(f), ".jsonl")
				}
			}
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if runID == "" {
			t.Fatal("the first level was never journaled")
		}
		out, code := g.pleiades(t, "rollback", runID)
		if code != 3 || !strings.Contains(out, "no seal") || !strings.Contains(out, "--allow-unknown") {
			t.Errorf("rollback of a killed run: exit %d, want 3 naming the missing seal\n%s", code, out)
		}
		if got := g.device(t, "test -e /tmp/rb-g5 && echo present || echo gone"); got != "gone" {
			t.Errorf("the directory the killed run made is %s: what the journal holds is still undone", got)
		}
	})

	t.Run("G6 a check changes nothing and journals nothing", func(t *testing.T) {
		g := newRollbackGate(t, container, host, port)
		g.device(t, "rm -rf /tmp/rb-g6")
		runID, _ := g.run(t, "g6", "id: g6\nhosts: container1\ntasks:\n  - name: make\n    file.directory:\n      path: /tmp/rb-g6\n")
		journals := g.journals(t)
		rep := g.report(t, "rollback", runID, "--mode", "check")
		if rep.Outcome.ExitCode != 0 || !slices.Equal(rep.methods(), []string{"file.remove tasks[0]"}) || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "would_change" {
			t.Fatalf("a check of the rollback: %+v", rep)
		}
		if g.journals(t) != journals {
			t.Error("a checked rollback wrote a journal")
		}
		if got := g.device(t, "test -d /tmp/rb-g6 && echo dir"); got != "dir" {
			t.Error("a checked rollback changed the device")
		}
	})
}

// runListEntry is one run in `journal list --json`.
type runListEntry struct {
	RunID      string `json:"run_id"`
	RollbackOf string `json:"rollback_of"`
}

// reportList decodes `pleiades journal list --json`.
func (g *rollbackGate) reportList(t *testing.T) []runListEntry {
	t.Helper()
	cmd := exec.Command(binPath, "journal", "list", "--json") // #nosec G204 -- the test's own binary
	cmd.Dir = g.dir
	cmd.Env = append(os.Environ(), "HOME="+g.home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("journal list --json: %v", err)
	}
	var runs []runListEntry
	if err := json.Unmarshal(out, &runs); err != nil {
		t.Fatalf("journal list --json: %v\n%s", err, out)
	}
	return runs
}
