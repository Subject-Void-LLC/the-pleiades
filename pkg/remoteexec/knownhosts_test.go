package remoteexec

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// generateTestHostKey returns a real ed25519 ssh.Signer, suitable for
// formatting into a known_hosts line, the same shape a real sshd's host
// key takes.
func generateTestHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to build ssh.Signer: %v", err)
	}
	return signer
}

// writeKnownHosts writes a single known_hosts line for hostPort and key
// into a fresh temp file, returning the file's path.
func writeKnownHosts(t *testing.T, hostPort string, key ssh.PublicKey) string {
	t.Helper()
	line := knownhosts.Line([]string{hostPort}, key)
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts file: %v", err)
	}
	return path
}

// fakeRemoteAddr is a minimal net.Addr good enough for exercising a
// ssh.HostKeyCallback in isolation, without a real connection.
type fakeRemoteAddr struct {
	s string
}

func (a fakeRemoteAddr) Network() string { return "tcp" }
func (a fakeRemoteAddr) String() string  { return a.s }

// TestHostKeyCallback_AcceptsMatchingKey proves that a callback built
// from a known_hosts file accepts the SAME key recorded for that host.
func TestHostKeyCallback_AcceptsMatchingKey(t *testing.T) {
	hostPort := "example.test:2222"
	signer := generateTestHostKey(t)
	path := writeKnownHosts(t, hostPort, signer.PublicKey())

	cb, err := hostKeyCallback(Options{KnownHostsPath: path})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}

	remote := fakeRemoteAddr{s: "203.0.113.10:2222"}
	if err := cb(hostPort, remote, signer.PublicKey()); err != nil {
		t.Fatalf("expected the matching host key to be accepted, got: %v", err)
	}
}

// TestHostKeyCallback_RejectsForgedKey proves that a callback rejects a
// DIFFERENT key presented for a host recorded under another key,
// simulating a MITM presenting a forged host key.
func TestHostKeyCallback_RejectsForgedKey(t *testing.T) {
	hostPort := "example.test:2222"
	realKey := generateTestHostKey(t)
	forgedKey := generateTestHostKey(t)
	path := writeKnownHosts(t, hostPort, realKey.PublicKey())

	cb, err := hostKeyCallback(Options{KnownHostsPath: path})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}

	remote := fakeRemoteAddr{s: "203.0.113.10:2222"}
	if err := cb(hostPort, remote, forgedKey.PublicKey()); err == nil {
		t.Fatal("expected a forged host key to be rejected, got nil error")
	}
}

// TestHostKeyCallback_MissingFileFailsClosed proves that a nonexistent
// known_hosts file produces a clear, explicit error from
// hostKeyCallback itself, rather than silently accepting any key later.
func TestHostKeyCallback_MissingFileFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := hostKeyCallback(Options{KnownHostsPath: path})
	if err == nil {
		t.Fatal("expected an error for a missing known_hosts file, got nil")
	}
}

// TestHostKeyCallback_UnknownHostRejected proves that a host with NO
// entry at all in a known_hosts file that does exist is rejected, not
// silently trusted (trust-on-first-use is explicitly not this
// package's behavior).
func TestHostKeyCallback_UnknownHostRejected(t *testing.T) {
	knownHostPort := "known.test:22"
	unknownHostPort := "unknown.test:22"
	key := generateTestHostKey(t)
	path := writeKnownHosts(t, knownHostPort, key.PublicKey())

	cb, err := hostKeyCallback(Options{KnownHostsPath: path})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}

	remote := fakeRemoteAddr{s: "203.0.113.11:22"}
	if err := cb(unknownHostPort, remote, key.PublicKey()); err == nil {
		t.Fatal("expected a host with no known_hosts entry to be rejected, got nil error")
	}
}

// TestHostKeyCallback_InsecureSkipBypassesVerification proves that
// InsecureSkipHostKeyVerify is the only way to bypass all of the above,
// accepting any key regardless of known_hosts content (or absence).
func TestHostKeyCallback_InsecureSkipBypassesVerification(t *testing.T) {
	cb, err := hostKeyCallback(Options{InsecureSkipHostKeyVerify: true, KnownHostsPath: "/does/not/exist"})
	if err != nil {
		t.Fatalf("expected no error when InsecureSkipHostKeyVerify is true, got: %v", err)
	}

	forgedKey := generateTestHostKey(t)
	remote := fakeRemoteAddr{s: "203.0.113.12:22"}
	if err := cb("anything:22", remote, forgedKey.PublicKey()); err != nil {
		t.Fatalf("expected InsecureSkipHostKeyVerify to accept any key, got: %v", err)
	}
}

// TestHostKeyCallback_EmptyPathResolvesTheHomeDirectory proves an
// unset KnownHostsPath means "$HOME/.ssh/known_hosts", resolved here on
// every connection rather than once when a Runner is built.
//
// Resolving it late is what lets Shared memoize a Runner without also
// freezing whichever home directory happened to be set first, and what
// lets a file written after construction still count.
func TestHostKeyCallback_EmptyPathResolvesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Cleared explicitly, not assumed absent. The home directory is the
	// LAST of three sources now, so a machine that happens to export this
	// variable would otherwise make this test pass against a file it never
	// meant to read.
	t.Setenv(KnownHostsEnv, "")

	// With no file there yet, the resolved path must fail closed and the
	// error must name where it looked, so an operator can act on it.
	_, err := hostKeyCallback(Options{})
	if err == nil {
		t.Fatal("expected an error when $HOME has no known_hosts file yet, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(home, ".ssh", "known_hosts")) {
		t.Errorf("error = %v, want it to name the path it resolved under $HOME", err)
	}

	// Writing the file afterward is enough: nothing cached the earlier
	// failure.
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", sshDir, err)
	}
	signer := generateTestHostKey(t)
	line := knownhosts.Line([]string{"late.test:22"}, signer.PublicKey())
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}

	cb, err := hostKeyCallback(Options{})
	if err != nil {
		t.Fatalf("hostKeyCallback after the file appeared: %v", err)
	}
	if err := cb("late.test:22", fakeRemoteAddr{s: "203.0.113.13:22"}, signer.PublicKey()); err != nil {
		t.Errorf("expected the newly written entry to be honored, got: %v", err)
	}
}

// TestHostKeyCallback_UnresolvableHomeFailsClosed proves that when there
// is no configured path AND no home directory to fall back on, the
// result is a clear refusal rather than a path that cannot exist being
// reported as a missing file, which would send a reader looking for a
// file they were never going to find.
func TestHostKeyCallback_UnresolvableHomeFailsClosed(t *testing.T) {
	// os.UserHomeDir reads $HOME on this platform and errors when it is
	// empty, which is the sandboxed-environment case Options documents.
	t.Setenv("HOME", "")
	t.Setenv(KnownHostsEnv, "")

	_, err := hostKeyCallback(Options{})
	if err == nil {
		t.Fatal("expected an error when no known_hosts path is configured and no home directory can be determined")
	}
	if !strings.Contains(err.Error(), "home directory") {
		t.Errorf("error = %v, want it to name the unresolvable home directory rather than a missing file", err)
	}
	// This is the exact error the published runner image produced, and the
	// operator reading it can do nothing about the home directory of a
	// distroless container. It has to name the thing they CAN set.
	if !strings.Contains(err.Error(), KnownHostsEnv) {
		t.Errorf("error = %v, want it to name %s, which is the actionable fix", err, KnownHostsEnv)
	}
}

// A compile-time sanity check that net.Addr is satisfied by
// fakeRemoteAddr, since ssh.HostKeyCallback's signature requires a real
// net.Addr.
var _ net.Addr = fakeRemoteAddr{}

// TestHostKeyCallback_UnstatableFileIsReported covers the branch between
// "the file is missing" and "the file parsed": a path that cannot be
// stat'd for some other reason must say so rather than be reported as a
// missing file, since the two need different fixes.
func TestHostKeyCallback_UnstatableFileIsReported(t *testing.T) {
	// A path whose parent is a regular file, so stat fails with
	// ENOTDIR rather than ENOENT.
	notADir := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", notADir, err)
	}

	_, err := hostKeyCallback(Options{KnownHostsPath: filepath.Join(notADir, "known_hosts")})
	if err == nil {
		t.Fatal("expected an unstatable known_hosts path to be refused")
	}
	if !strings.Contains(err.Error(), "stat known_hosts file") {
		t.Errorf("error = %v, want it to report the stat failure rather than a missing file", err)
	}
}

// setHomeKnownHosts points $HOME at a fresh temp directory holding a
// known_hosts file with one entry for hostPort, the shape a real user's
// home directory has, and returns the file's path.
func setHomeKnownHosts(t *testing.T, hostPort string, key ssh.PublicKey) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", sshDir, err)
	}
	path := filepath.Join(sshDir, "known_hosts")
	line := knownhosts.Line([]string{hostPort}, key)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// assertVerifies checks that cb accepts key for accepted and rejects it
// for rejected, which together prove WHICH known_hosts file was read
// rather than merely that some file was.
//
// Asserting the rejection matters as much as the acceptance. A callback
// built from the wrong file still returns a nil error from
// hostKeyCallback, so a test that only checked for "no error" would pass
// no matter which of the three sources won.
func assertVerifies(t *testing.T, cb ssh.HostKeyCallback, key ssh.PublicKey, accepted, rejected string) {
	t.Helper()
	if err := cb(accepted, fakeRemoteAddr{s: "203.0.113.20:22"}, key); err != nil {
		t.Errorf("expected %q to be accepted, got: %v", accepted, err)
	}
	if err := cb(rejected, fakeRemoteAddr{s: "203.0.113.21:22"}, key); err == nil {
		t.Errorf("expected %q to be rejected, since its entry lives in a file that should not have been read", rejected)
	}
}

// TestHostKeyCallback_EnvironmentPathBeatsTheHomeDirectory proves the
// deployment's own known_hosts is used when the caller configured none,
// in preference to whatever the account's home directory holds.
//
// This is the case the runner container is in, and the case that did not
// exist before: with no way to name a file, the home directory was the
// only source, and a distroless container has neither a home directory
// nor a file in it.
func TestHostKeyCallback_EnvironmentPathBeatsTheHomeDirectory(t *testing.T) {
	key := generateTestHostKey(t)
	setHomeKnownHosts(t, "home.test:22", key.PublicKey())
	t.Setenv(KnownHostsEnv, writeKnownHosts(t, "deployment.test:22", key.PublicKey()))

	cb, err := hostKeyCallback(Options{})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}
	assertVerifies(t, cb, key.PublicKey(), "deployment.test:22", "home.test:22")
}

// TestHostKeyCallback_ExplicitPathBeatsTheEnvironment proves the
// hierarchy runs the right way round: a caller that named a file for one
// connection is more specific than a variable set for the whole process,
// so the caller wins.
func TestHostKeyCallback_ExplicitPathBeatsTheEnvironment(t *testing.T) {
	key := generateTestHostKey(t)
	explicit := writeKnownHosts(t, "explicit.test:22", key.PublicKey())
	t.Setenv(KnownHostsEnv, writeKnownHosts(t, "deployment.test:22", key.PublicKey()))

	cb, err := hostKeyCallback(Options{KnownHostsPath: explicit})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}
	assertVerifies(t, cb, key.PublicKey(), "explicit.test:22", "deployment.test:22")
}

// TestHostKeyCallback_EmptyEnvironmentValueCountsAsUnset proves that
// exporting the name with no value configures nothing and falls through
// to the home directory, rather than resolving to a file named "" and
// reporting it missing.
func TestHostKeyCallback_EmptyEnvironmentValueCountsAsUnset(t *testing.T) {
	key := generateTestHostKey(t)
	setHomeKnownHosts(t, "home.test:22", key.PublicKey())
	t.Setenv(KnownHostsEnv, "")

	cb, err := hostKeyCallback(Options{})
	if err != nil {
		t.Fatalf("hostKeyCallback returned an error: %v", err)
	}
	if err := cb("home.test:22", fakeRemoteAddr{s: "203.0.113.22:22"}, key.PublicKey()); err != nil {
		t.Errorf("expected the home directory entry to be honored when %s is exported empty, got: %v", KnownHostsEnv, err)
	}
}

// TestHostKeyCallback_EnvironmentPathStillFailsClosed proves the new
// source is not a way around the fail-closed rule: a variable naming a
// file that is not there is refused, and the error names the path so an
// operator can see the mount landed somewhere else.
func TestHostKeyCallback_EnvironmentPathStillFailsClosed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted", "known_hosts")
	// A home directory WITH a usable file, to prove the environment path is
	// not quietly abandoned in favor of a fallback that happens to work.
	setHomeKnownHosts(t, "home.test:22", generateTestHostKey(t).PublicKey())
	t.Setenv(KnownHostsEnv, missing)

	_, err := hostKeyCallback(Options{})
	if err == nil {
		t.Fatal("expected a missing known_hosts file named by the environment to be refused, not fall back to $HOME")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %v, want it to name %q", err, missing)
	}
}
