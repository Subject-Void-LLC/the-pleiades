// Package need: a test that needs something the machine may not offer,
// skipped where it is missing and failed where the run requires it.
//
// This is testsupport.Require's whole implementation, kept in a package
// that imports nothing but the standard library. internal/testsupport
// imports testcontainers-go, and an external Collection author's `go mod
// tidy` loads the tests of every package pkg/external reaches, so a test
// there that imported testsupport put testcontainers-go's whole module
// graph into every such author's go.sum. A test of such a package uses
// this package instead (internal/archtest holds that).
package need

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Env lists, comma separated, what a run insists on: a test that needs
// one of them fails when it is missing instead of skipping. "all" requires
// everything. CI sets it for what its runners provide, so a capability a
// runner loses turns the job red rather than quietly shrinking what it
// tests; a developer's machine leaves it unset.
const Env = "PLEIADES_TEST_REQUIRE"

// Require stops t when the machine lacks what: available is the answer and
// why says what is missing and how to provide it. When this run requires
// what (Env), that is a failure; otherwise it is a skip, which every
// tools/testgate run lists with its reason ("needs <what>: <why>"), so it
// is never read as a pass.
func Require(t testing.TB, what string, available bool, why string) {
	t.Helper()
	if available {
		return
	}
	if required(what) {
		t.Fatalf("this run requires %s (%s=%s), and it is missing: %s", what, Env, os.Getenv(Env), why)
		return // unreachable with a real testing.T, whose Fatalf never returns
	}
	t.Skipf("needs %s: %s", what, why)
}

// required reports whether Env names what, or says all.
func required(what string) bool {
	list := strings.Split(os.Getenv(Env), ",")
	for i := range list {
		list[i] = strings.TrimSpace(list[i])
	}
	return slices.Contains(list, what) || slices.Contains(list, "all")
}
