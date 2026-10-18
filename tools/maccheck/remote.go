// The Mac end of maccheck: what it is (pleiadesRemote) and reaching it
// (ssh -G and the liveness probe). device.go drives it as a Pleiades
// device, sending the tree and running the tests through pleiades itself.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// macPath is added to the Mac's PATH, since a non-interactive SSH session
// does not read the profile that puts Homebrew's or go.dev's go on it.
const macPath = "/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin"

// checkoutDir is where trees are unpacked on the Mac, under its home, one
// directory per commit; the newest oscheck.Kept stay.
const checkoutDir = ".pleiades-maccheck"

// device is the Mac's name in the private project.
const device = "mac"

// sshOptions are the plain login check's: never prompt, give up on a
// connection in ten seconds, and end one the Mac stops answering (asleep,
// or off the network) after a minute rather than wait on it forever.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}

// probeTimeout bounds the liveness probe of the Mac's SSH port; a variable
// so a test can probe an address that never answers without waiting.
var probeTimeout = 5 * time.Second

// pleiadesRemote is the real Mac end.
type pleiadesRemote struct {
	// mac is the target as ssh takes it.
	mac string
	// project is the private Pleiades project's directory, under this
	// worktree's git directory so it is never committed and never shared.
	project string
	// posix sends the tree and runs the tests, through the binary built in
	// the project.
	posix oscheck.POSIX
}

// newRemote is the Mac mac, driven from the private project.
func newRemote(project, mac string) pleiadesRemote {
	p := oscheck.Pleiades{Binary: filepath.Join(project, "pleiades"), Project: project, Device: device}
	return pleiadesRemote{mac: mac, project: project, posix: oscheck.POSIX{
		Pleiades: p,
		Tool:     "maccheck",
		Machine:  "the Mac",
		Dir:      checkoutDir,
		Path:     macPath,
		Wrap:     "caffeinate -dims ",
		Files:    "files",
	}}
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

// Reachable resolves the Mac as ssh would and opens a TCP connection to its
// SSH port, so a Mac that is asleep, off the network or not listening is
// named in seconds, before anything else runs.
func (r pleiadesRemote) Reachable() error {
	t, err := resolve(r.mac)
	if err != nil {
		return err
	}
	return probe(r.mac, t.host, t.port)
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
