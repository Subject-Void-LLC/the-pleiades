// The Mac as a Pleiades device: the private project that holds it, and the
// methods that check it and run the tests on it, through this branch's own
// pleiades binary.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Ready builds pleiades, makes the Mac a device of the private project
// when the project does not hold this exact target yet, then asks
// net.ssh.ping whether Pleiades reaches it and exec.command whether it is
// a Mac with go and caffeinate.
func (r pleiadesRemote) Ready(mac string) error {
	t, err := resolve(mac)
	if err != nil {
		return err
	}
	if err := loginCheck(mac, t); err != nil {
		return err
	}
	if err := r.ensureDevice(mac, t); err != nil {
		return err
	}
	if _, err := r.adhoc("net.ssh.ping"); err != nil {
		return fmt.Errorf("pleiades could not reach %s: %v\n  If its host key is new to this machine, run `ssh %s true` once and accept it", mac, err, mac)
	}
	out, err := r.adhoc("exec.command", "argv:="+argvYAML("/bin/sh", "-c", readyScript))
	if err != nil {
		return fmt.Errorf("checking %s: %v", mac, err)
	}
	return readyAnswer(mac, out)
}

// readyScript reports the Mac's kernel and whether go and caffeinate are
// on its PATH, one line each.
const readyScript = `export PATH="$PATH:` + macPath + `"
uname -s
command -v go >/dev/null 2>&1 && echo go || echo no-go
command -v caffeinate >/dev/null 2>&1 && echo caffeinate || echo no-caffeinate`

// readyAnswer checks readyScript's output.
func readyAnswer(mac, out string) error {
	lines := strings.Fields(out)
	if len(lines) != 3 {
		return fmt.Errorf("%s answered the readiness check with %q", mac, out)
	}
	switch {
	case lines[0] != "Darwin":
		return fmt.Errorf("%s is not a Mac: its kernel is %s", mac, lines[0])
	case lines[1] != "go":
		return fmt.Errorf("%s has no go on its PATH (looked in its PATH and %s): install it with `brew install go`, or from https://go.dev/dl", mac, macPath)
	case lines[2] != "caffeinate":
		return fmt.Errorf("%s has no caffeinate, which keeps it awake through the run", mac)
	}
	return nil
}

// ensureDevice (re)creates the private project for t unless it already
// holds exactly t: built pleiades, init, add-host, add-credential with the
// first of t's identity files that exists, and onboard.
func (r pleiadesRemote) ensureDevice(mac string, t sshTarget) error {
	key := firstKey(t)
	if key == "" {
		return fmt.Errorf("ssh names no private key that exists for %s (it looked at %v); make one with ssh-keygen -t ed25519", mac, t.keys)
	}
	stamp := strings.Join([]string{mac, t.host, t.port, t.user, key}, "\n")
	if data, err := os.ReadFile(filepath.Join(r.project, "target")); err == nil && string(data) == stamp && fileExists(r.binary()) {
		if err := r.build(); err != nil {
			return err
		}
		// Onboarding again records what this branch's binary grants the
		// Mac, which a binary from another branch may not have.
		if out, err := r.pleiades("onboard", device, "--dir", r.project); err != nil {
			return fmt.Errorf("pleiades onboard: %v\n%s", err, strings.TrimSpace(out))
		}
		return nil
	}
	if err := os.RemoveAll(r.project); err != nil {
		return err
	}
	if err := os.MkdirAll(r.project, 0o700); err != nil {
		return err
	}
	if err := r.build(); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "--dir", r.project},
		{"add-host", device, "--type", "generic_ssh", "--set", "host=" + t.host, "--set", "port=" + t.port, "--dir", r.project},
		{"add-credential", device, "--username", t.user, "--key", key, "--dir", r.project},
		{"onboard", device, "--dir", r.project},
	} {
		if out, err := r.pleiades(args...); err != nil {
			return fmt.Errorf("pleiades %s: %v\n%s", args[0], err, strings.TrimSpace(out))
		}
	}
	return os.WriteFile(filepath.Join(r.project, "target"), []byte(stamp), 0o600)
}

// binary is where this run's pleiades is built.
func (r pleiadesRemote) binary() string { return filepath.Join(r.project, "pleiades") }

// build builds pleiades from this tree into the project, so the run
// drives the branch's own binary.
func (r pleiadesRemote) build() error {
	out, err := exec.Command("go", "build", "-o", r.binary(), "./cmd/pleiades").CombinedOutput() // #nosec G204 -- this repository's own command
	if err != nil {
		return fmt.Errorf("building pleiades: %v\n%s", err, out)
	}
	return nil
}

// pleiades runs the built binary with args, returning its combined output.
func (r pleiadesRemote) pleiades(args ...string) (string, error) {
	out, err := exec.Command(r.binary(), args...).CombinedOutput() // #nosec G204 -- the binary this run built, with arguments this program wrote
	return string(out), err
}

// adhocReport is the part of `pleiades adhoc --json` read here.
type adhocReport struct {
	Tasks []struct {
		Status string         `json:"status"`
		Error  string         `json:"error"`
		Stats  map[string]any `json:"stats"`
	} `json:"tasks"`
	Validation []struct {
		Message string `json:"message"`
	} `json:"validation"`
	Outcome struct {
		ExitCode int    `json:"exit_code"`
		Message  string `json:"message"`
	} `json:"outcome"`
}

// adhoc runs one method against the Mac and returns its stdout return, or
// the task's error.
func (r pleiadesRemote) adhoc(method string, params ...string) (string, error) {
	args := append([]string{"adhoc", device, method}, params...)
	args = append(args, "--json", "--dir", r.project)
	out, _ := exec.Command(r.binary(), args...).Output() // #nosec G204 -- the binary this run built, with arguments this program wrote
	var report adhocReport
	if err := json.Unmarshal(out, &report); err != nil {
		return "", fmt.Errorf("pleiades adhoc %s printed no report: %v", method, err)
	}
	if len(report.Tasks) != 1 {
		why := report.Outcome.Message
		for _, v := range report.Validation {
			why += "; " + v.Message
		}
		return "", fmt.Errorf("pleiades adhoc %s ran %d tasks: %s", method, len(report.Tasks), why)
	}
	task := report.Tasks[0]
	if task.Error != "" || report.Outcome.ExitCode != 0 {
		return "", fmt.Errorf("%s: %s", method, firstOf(task.Error, report.Outcome.Message))
	}
	stdout, _ := task.Stats["stdout"].(string)
	return stdout, nil
}

// Ship sends commit sha's tree to the Mac through pleiades: exec.command
// removes all but the newest trees and answers the Mac's home directory,
// git archive writes the tree into the private project, file.copy sends
// it, and archive.extract unpacks it into a directory of its own and
// deletes the archive.
func (r pleiadesRemote) Ship(_, sha string) error {
	out, err := r.adhoc("exec.command", "argv:="+argvYAML("/bin/sh", "-c", fmt.Sprintf(`set -e
base="$HOME/%[1]s"
mkdir -p "$base"
ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "${base:?}/$old"; done
rm -rf "${base:?}/%[3]s" "${base:?}/%[3]s.tar.gz"
printf '%%s\n' "$HOME"`, checkoutDir, keptCheckouts, sha)))
	if err != nil {
		return fmt.Errorf("preparing the Mac: %v", err)
	}
	home := strings.TrimSpace(out)
	if !strings.HasPrefix(home, "/") {
		return fmt.Errorf("the Mac named %q as its home directory", home)
	}
	files := filepath.Join(r.project, "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return err
	}
	local := filepath.Join(files, sha+".tar.gz")
	defer func() { _ = os.Remove(local) }()
	if out, err := exec.Command("git", "archive", "--format=tar.gz", "-o", local, sha).CombinedOutput(); err != nil { // #nosec G204 -- a commit rev-parse resolved
		return fmt.Errorf("git archive: %v: %s", err, strings.TrimSpace(string(out)))
	}
	archive := path.Join(home, checkoutDir, sha+".tar.gz")
	if _, err := r.adhoc("file.copy", "src=files/"+sha+".tar.gz", "dest="+archive, "mode=0600"); err != nil {
		return err
	}
	_, err = r.adhoc("archive.extract", "src="+archive, "dest="+path.Join(home, checkoutDir, sha), "remove:=true")
	return err
}

// Test runs script on the Mac through exec.command and reads its exit
// status from the marker the script ends its output with.
func (r pleiadesRemote) Test(mac, sha, script string, stdout, stderr io.Writer) (bool, error) {
	fmt.Fprintln(stdout, "maccheck: running on the Mac; Pleiades returns the output when the run ends, since it does not stream it yet")
	out, err := r.adhoc("exec.command", "argv:="+argvYAML("/bin/sh", "-c", script))
	if err != nil {
		return false, err
	}
	output, code, ok := splitMarker(out)
	fmt.Fprintln(stdout, output)
	if !ok {
		return false, fmt.Errorf("the run on %s ended without its exit status", mac)
	}
	return code == 0, nil
}

// splitMarker splits the test run's output from the exit status its last
// line carries.
func splitMarker(out string) (string, int, bool) {
	i := strings.LastIndex(out, exitMarker)
	if i < 0 {
		return out, 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(out[i+len(exitMarker):]))
	if err != nil {
		return out, 0, false
	}
	return strings.TrimRight(out[:i], "\n"), code, true
}

// argvYAML is argv as a YAML flow list, for an adhoc key:=yaml parameter.
func argvYAML(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		b, _ := json.Marshal(a) // a JSON string is a YAML double-quoted scalar
		quoted[i] = string(b)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// loginCheck logs in once with plain ssh, never prompting, so a key the
// Mac does not hold or a host key this machine does not know is named
// with the command that fixes it, rather than as the raw error of the
// Pleiades onboarding that would meet it next.
func loginCheck(mac string, t sshTarget) error {
	var stderr bytes.Buffer
	cmd := exec.Command("ssh", slices.Concat(sshOptions, []string{"--", mac, "true"})...) // #nosec G204 -- the developer's own configured Mac
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return loginFailure(mac, firstKey(t), stderr.String(), err)
	}
	return nil
}

// loginFailure explains a failed ssh login by what ssh said.
func loginFailure(mac, key, said string, err error) error {
	pub := "~/.ssh/id_ed25519.pub"
	if key != "" {
		pub = key + ".pub"
	}
	switch {
	case strings.Contains(said, "Permission denied"):
		return fmt.Errorf("%s refused this machine's key: %s\n  Add it to the Mac once, from this machine (it asks for the Mac's password):\n    ssh-copy-id -i %s %s", mac, strings.TrimSpace(said), pub, mac)
	case strings.Contains(said, "Host key verification failed"), strings.Contains(said, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return fmt.Errorf("this machine does not trust %s's host key: %s\n  Check it is your Mac, then accept its key once: ssh %s true", mac, strings.TrimSpace(said), mac)
	}
	return fmt.Errorf("ssh could not log in to %s: %v: %s", mac, err, strings.TrimSpace(said))
}

// firstKey is the first of t's identity files that exists, or "".
func firstKey(t sshTarget) string {
	for _, k := range t.keys {
		if expanded := expandHome(k); fileExists(expanded) {
			return expanded
		}
	}
	return ""
}
