// The Mac as a Pleiades device: the private project that holds it, and the
// checks that it can run the tests, through this branch's own pleiades
// binary. Sending the tree and running the tests are oscheck.POSIX's.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// Ready builds pleiades, makes the Mac a device of the private project
// when the project does not hold this exact target yet, then asks
// net.ssh.ping whether Pleiades reaches it and exec.command whether it is
// a Mac with go and caffeinate.
func (r pleiadesRemote) Ready() error {
	t, err := resolve(r.mac)
	if err != nil {
		return err
	}
	if err := loginCheck(r.mac, t); err != nil {
		return err
	}
	if err := r.ensureDevice(t); err != nil {
		return err
	}
	if _, err := r.posix.Pleiades.Adhoc(context.Background(), "net.ssh.ping"); err != nil {
		return fmt.Errorf("pleiades could not reach %s: %v\n  If its host key is new to this machine, run `ssh %s true` once and accept it", r.mac, err, r.mac)
	}
	return r.posix.Ready(r.mac, "Darwin", "Mac",
		oscheck.Need{Command: "go", Why: "install it with `brew install go`, or from https://go.dev/dl"},
		oscheck.Need{Command: "git", Why: "the tests that make a throwaway repository need it: install the Command Line Tools with `xcode-select --install`"},
		oscheck.Need{Command: "caffeinate", Why: "it keeps the Mac awake through the run"})
}

// Ship unpacks commit sha's tree on the Mac (oscheck.POSIX.Ship).
func (r pleiadesRemote) Ship(sha string) error { return r.posix.Ship(sha) }

// Test runs the packages on sha's tree, under caffeinate
// (oscheck.POSIX.Test).
func (r pleiadesRemote) Test(ctx context.Context, sha string, parallel int, packages []string, stdout io.Writer) (bool, error) {
	return r.posix.Test(ctx, r.mac, sha, parallel, packages, stdout)
}

// ensureDevice (re)creates the private project for t unless it already
// holds exactly t: built pleiades, init, add-host, add-credential with the
// first of t's identity files that exists, and onboard.
func (r pleiadesRemote) ensureDevice(t sshTarget) error {
	key := firstKey(t)
	if key == "" {
		return fmt.Errorf("ssh names no private key that exists for %s (it looked at %v); make one with ssh-keygen -t ed25519", r.mac, t.keys)
	}
	p := r.posix.Pleiades
	stamp := strings.Join([]string{r.mac, t.host, t.port, t.user, key}, "\n")
	if data, err := os.ReadFile(filepath.Join(r.project, "target")); err == nil && string(data) == stamp && oscheck.FileExists(p.Binary) {
		if err := p.Build(); err != nil {
			return err
		}
		// Onboarding again records what this branch's binary grants the
		// Mac, which a binary from another branch may not have.
		if out, err := p.Run("onboard", device, "--dir", r.project); err != nil {
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
	if err := p.Build(); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "--dir", r.project},
		{"add-host", device, "--type", "generic_ssh", "--set", "host=" + t.host, "--set", "port=" + t.port, "--dir", r.project},
		{"add-credential", device, "--username", t.user, "--key", key, "--dir", r.project},
		{"onboard", device, "--dir", r.project},
	} {
		if out, err := p.Run(args...); err != nil {
			return fmt.Errorf("pleiades %s: %v\n%s", args[0], err, strings.TrimSpace(out))
		}
	}
	return os.WriteFile(filepath.Join(r.project, "target"), []byte(stamp), 0o600)
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
		if expanded := oscheck.ExpandHome(k); oscheck.FileExists(expanded) {
			return expanded
		}
	}
	return ""
}
