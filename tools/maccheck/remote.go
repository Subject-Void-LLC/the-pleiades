// The Mac end of maccheck: what it is (remoteRunner), reaching it (ssh -G
// and the liveness probe), and sending it the tree. device.go drives it as
// a Pleiades device.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// macPath is added to the Mac's PATH, since a non-interactive SSH session
// does not read the profile that puts Homebrew's or go.dev's go on it.
const macPath = "/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin"

// checkoutDir is where trees are unpacked on the Mac, under its home, one
// directory per commit; the newest keptCheckouts stay.
const (
	checkoutDir   = ".pleiades-maccheck"
	keptCheckouts = 3
)

// device is the Mac's name in the private project.
const device = "mac"

// exitMarker ends the test run's output with its exit status. The run
// itself always exits 0: exec.command fails a task on a non-zero exit and
// keeps only an excerpt of its output then, and a failing run's output is
// the part worth reading.
const exitMarker = "maccheck-exit="

// sshOptions are every ssh call's: never prompt, give up on a connection
// in ten seconds, and end one the Mac stops answering (asleep, or off the
// network) after a minute rather than wait on it forever.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}

// probeTimeout bounds the liveness probe of the Mac's SSH port; a variable
// so a test can probe an address that never answers without waiting.
var probeTimeout = 5 * time.Second

// remoteRunner is the Mac end, apart from run so tests can stand in for it.
type remoteRunner interface {
	// Reachable probes the Mac's SSH port.
	Reachable(mac string) error
	// Ready makes the Mac a device of the private project, if it is not
	// one already, and checks it can run the tests.
	Ready(mac string) error
	// Ship unpacks commit sha's tree on the Mac.
	Ship(mac, sha string) error
	// Test runs script there and reports whether it passed, writing its
	// output to stdout.
	Test(mac, sha, script string, stdout, stderr io.Writer) (bool, error)
}

// pleiadesRemote is the real Mac end.
type pleiadesRemote struct {
	// project is the private Pleiades project's directory, under this
	// worktree's git directory so it is never committed and never shared.
	project string
}

// sshTarget is what ssh -G resolves a target to: an alias's HostName,
// Port, User and IdentityFile included.
type sshTarget struct {
	host, port, user string
	keys             []string
}

// resolve runs ssh -G on mac.
func resolve(mac string) (sshTarget, error) {
	out, err := exec.Command("ssh", "-G", "--", mac).Output() // #nosec G204 -- the developer's own configured Mac
	if err != nil {
		return sshTarget{}, fmt.Errorf("ssh cannot resolve %q: %v", mac, err)
	}
	return parseSSHG(out), nil
}

// parseSSHG reads the hostname, port, user and identityfile lines of
// `ssh -G` output.
func parseSSHG(out []byte) sshTarget {
	var t sshTarget
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), " ")
		if !ok {
			continue
		}
		switch key {
		case "hostname":
			t.host = value
		case "port":
			t.port = value
		case "user":
			t.user = value
		case "identityfile":
			t.keys = append(t.keys, value)
		}
	}
	return t
}

// Reachable resolves mac as ssh would and opens a TCP connection to its
// SSH port, so a Mac that is asleep, off the network or not listening is
// named in seconds, before anything else runs.
func (pleiadesRemote) Reachable(mac string) error {
	t, err := resolve(mac)
	if err != nil {
		return err
	}
	return probe(mac, t.host, t.port)
}

// probe opens and closes one TCP connection to host:port.
func probe(mac, host, port string) error {
	if host == "" || port == "" {
		return fmt.Errorf("ssh -G named no host and port for %q", mac)
	}
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return fmt.Errorf("%s (%s) did not answer on its SSH port within %s: %v\n  Is the Mac awake and on this network, with Remote Login on (System Settings > General > Sharing > Remote Login)?", mac, addr, probeTimeout, err)
	}
	return conn.Close()
}

// Ship pipes git archive's tar of sha into a fresh directory on the Mac,
// over ssh, after removing all but the newest unpacked trees. It is the
// one step that is not a Pleiades method: no method moves a local file to
// a device yet (file.copy refuses src, since a Runner cannot see the
// controller's files), and a git push would meet this clone's pre-push
// gate.
func (pleiadesRemote) Ship(mac, sha string) error {
	unpack := fmt.Sprintf(`set -e
base="$HOME/%[1]s"
mkdir -p "$base"
ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "$base/$old"; done
rm -rf "$base/%[3]s"
mkdir -p "$base/%[3]s"
tar -x -C "$base/%[3]s"`, checkoutDir, keptCheckouts, sha)
	archive := exec.Command("git", "archive", "--format=tar", sha) // #nosec G204 -- a commit rev-parse resolved
	tar, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	send := exec.Command("ssh", slices.Concat(sshOptions, []string{"--", mac, "/bin/sh", "-c", shellQuote(unpack)})...) // #nosec G204 -- the developer's own configured Mac
	send.Stdin = tar
	send.Stderr = &stderr
	if err := archive.Start(); err != nil {
		return err
	}
	sendErr := send.Run()
	archiveErr := archive.Wait()
	if sendErr != nil {
		return fmt.Errorf("%v: %s", sendErr, strings.TrimSpace(stderr.String()))
	}
	return archiveErr
}

// shellQuote quotes s as one word for the Mac's login shell, which runs
// the command ssh sends it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
