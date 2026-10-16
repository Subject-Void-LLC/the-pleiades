// Package testsupport: a test that needs something the machine may not
// offer, skipped where it is missing and failed where the run requires it.
package testsupport

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// RequireEnv lists, comma separated, what a run insists on (need.Env).
const RequireEnv = need.Env

// Require stops t when the machine lacks what: a skip naming it, or a
// failure when this run requires it (need.Require, whose package imports
// only the standard library so a light test can use it directly).
func Require(t testing.TB, what string, available bool, why string) {
	t.Helper()
	need.Require(t, what, available, why)
}

// LocalStackEnv names the variable holding the auth token the AWS
// tests start their LocalStack container with.
const LocalStackEnv = "LOCALSTACK_AUTH_TOKEN"

// LocalStackToken returns the LocalStack auth token, stopping tb through
// Require("localstack") when there is none. Going through Require is what
// lets a gate tell a package measured without LocalStack from one whose
// coverage fell: its skip reason names the need.
func LocalStackToken(tb testing.TB) string {
	tb.Helper()
	token := os.Getenv(LocalStackEnv)
	Require(tb, "localstack", token != "",
		"set "+LocalStackEnv+" to a LocalStack auth token; these tests run against a real LocalStack container")
	return token
}

// userNamespaces caches one probe of whether this machine lets an
// unprivileged process create a user namespace; the answer does not change
// while a test binary runs.
var userNamespaces struct {
	once sync.Once
	ok   bool
	why  string
}

// UserNamespaces reports whether an unprivileged process may create a user
// namespace here, and if not, why and how to allow it. Ubuntu 24.04 refuses
// by default through AppArmor, and Docker's default seccomp profile refuses
// inside a container. Off Linux the answer is that the kernel has no such
// thing, said as that: a macOS run used to be told to look for unshare and
// then to change an Ubuntu sysctl.
func UserNamespaces() (bool, string) {
	userNamespaces.once.Do(func() {
		if runtime.GOOS != "linux" {
			userNamespaces.why = fmt.Sprintf("user namespaces are a Linux kernel feature, and this is %s", runtime.GOOS)
			return
		}
		out, err := exec.Command("unshare", "--user", "--map-root-user", "true").CombinedOutput()
		if err == nil {
			userNamespaces.ok = true
			return
		}
		userNamespaces.why = fmt.Sprintf("unprivileged user namespaces are refused here (unshare: %v %s); "+
			"on Ubuntu 24.04 allow them with `sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0`, "+
			"and inside a container with a seccomp profile that permits unshare",
			err, strings.TrimSpace(string(out)))
	})
	return userNamespaces.ok, userNamespaces.why
}
