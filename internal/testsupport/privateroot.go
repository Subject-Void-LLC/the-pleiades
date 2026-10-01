// Package testsupport: re-running a test as root inside an unprivileged
// user and mount namespace.
package testsupport

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// privateRootEnv names the test a re-executed binary is running as root
// for, so the re-run knows it is the re-run.
const privateRootEnv = "PLEIADES_PRIVATE_ROOT_TEST"

// PrivateRootScratch is the environment variable a setup script reads for
// a directory of its own. The calling test's t.TempDir() provides it, so
// whatever the script writes there is removed with the test.
const PrivateRootScratch = "PLEIADES_PRIVATE_ROOT_SCRATCH"

// PrivateEtc is a setup script for InPrivateRoot giving the namespace its
// own copy of /etc, bind-mounted over the real one, and empty tmpfs mounts
// over /home, /var/log and /var/mail, so real useradd, groupadd and their
// kin can run against it while the host's account database, home
// directories and logs stay exactly as they were. The copy lacks what this
// account cannot read, so it starts with an empty shadow and gshadow.
const PrivateEtc = `etc="$` + PrivateRootScratch + `/etc"
mkdir -p "$etc"
cp -a /etc/. "$etc"/ 2>/dev/null || true
: > "$etc/shadow"
: > "$etc/gshadow"
chmod 600 "$etc/shadow" "$etc/gshadow"
mount --bind "$etc" /etc
for d in /home /var/log /var/mail; do
  if [ -d "$d" ]; then mount -t tmpfs tmpfs "$d"; fi
done`

// InPrivateRoot runs the calling test again, as root, inside a new user
// namespace mapping this account to root and a new mount namespace
// (unshare --user --map-root-user --mount), after setup, a /bin/sh script,
// has run there. It reports whether this process is that re-run: the test
// does its work when it returns true and returns at once when it returns
// false, by which time the re-run has passed or t has failed.
//
// It is how a test proves a method against what only root may do (a real
// mount, a real account database) on a host where the test itself is not
// root. The namespace needs no privilege and changes nothing outside
// itself: a mount made there is gone when the process exits, and a file
// written through a bind mount lands in the setup's own copy. Every child
// of the re-run, including the shells of the in-process SSH harness,
// shares the namespace.
//
// The re-run selects the test by name, so it must be called from a
// top-level test, never a subtest. It is skipped in short mode.
//
// A host that refuses unprivileged user namespaces (Ubuntu 24.04 by
// default, any container with Docker's default seccomp profile) skips the
// test with the reason and the fix, through Require, unless the run
// requires "userns" (PLEIADES_TEST_REQUIRE), when it fails. This used to
// fail everywhere, on the reasoning that a skip would read as a pass. That
// stopped being true when every tools/testgate run began listing its skips
// with their reasons, and CI requires userns, so these tests cannot
// quietly stop running there; failing on a stranger's machine blamed their
// kernel policy for nothing in their change (Phase 118's clean room).
func InPrivateRoot(t *testing.T, setup string) bool {
	t.Helper()
	if os.Getenv(privateRootEnv) == t.Name() {
		return true
	}
	if strings.Contains(t.Name(), "/") {
		t.Fatalf("InPrivateRoot re-runs a test by name, so it cannot be called from subtest %s", t.Name())
	}
	if testing.Short() {
		t.Skip("skipping a test that re-runs itself as root in a user namespace, in short mode")
	}
	ok, why := UserNamespaces()
	Require(t, "userns", ok, why)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("finding this test binary to re-run: %v", err)
	}
	script := "set -e\n" + setup + "\nexec \"$0\" \"$@\""
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", "/bin/sh", "-c", script, // #nosec G204 -- fixed arguments, a script the calling test wrote, and this test's own binary
		self, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), privateRootEnv+"="+t.Name(), PrivateRootScratch+"="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the re-run as root in a user namespace failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: "+t.Name()) {
		t.Fatalf("the re-run as root in a user namespace did not report %s passing:\n%s", t.Name(), out)
	}
	return false
}
