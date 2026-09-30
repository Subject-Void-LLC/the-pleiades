// Package testsupport: a test that needs something the machine may not
// offer, skipped where it is missing and failed where the run requires it.
package testsupport

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

// RequireEnv lists, comma separated, what a run insists on: a test that
// needs one of them fails when it is missing instead of skipping. "all"
// requires everything. CI sets it for what its runners provide, so a
// capability a runner loses turns the job red rather than quietly
// shrinking what it tests; a developer's machine leaves it unset.
const RequireEnv = "PLEIADES_TEST_REQUIRE"

// Require stops t when the machine lacks need: available is the answer
// and why says what is missing and how to provide it. When this run
// requires need (RequireEnv), that is a failure; otherwise it is a skip,
// which every tools/testgate run lists with its reason, so it is never
// read as a pass.
func Require(t testing.TB, need string, available bool, why string) {
	t.Helper()
	if available {
		return
	}
	if required(need) {
		t.Fatalf("this run requires %s (%s=%s), and it is missing: %s", need, RequireEnv, os.Getenv(RequireEnv), why)
		return // unreachable with a real testing.T, whose Fatalf never returns
	}
	t.Skipf("needs %s: %s", need, why)
}

// required reports whether RequireEnv names need, or says all.
func required(need string) bool {
	list := strings.Split(os.Getenv(RequireEnv), ",")
	for i := range list {
		list[i] = strings.TrimSpace(list[i])
	}
	return slices.Contains(list, need) || slices.Contains(list, "all")
}

// LocalStackTokenEnv names the variable holding the auth token the AWS
// tests start their LocalStack container with.
const LocalStackTokenEnv = "LOCALSTACK_AUTH_TOKEN"

// LocalStackToken returns the LocalStack auth token, stopping tb through
// Require("localstack") when there is none. Going through Require is what
// lets a gate tell a package measured without LocalStack from one whose
// coverage fell: its skip reason names the need.
func LocalStackToken(tb testing.TB) string {
	tb.Helper()
	token := os.Getenv(LocalStackTokenEnv)
	Require(tb, "localstack", token != "",
		"set "+LocalStackTokenEnv+" to a LocalStack auth token; these tests run against a real LocalStack container")
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
// inside a container.
func UserNamespaces() (bool, string) {
	userNamespaces.once.Do(func() {
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
