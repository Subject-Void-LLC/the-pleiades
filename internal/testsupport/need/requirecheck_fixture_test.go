// The control for checkRequireCalls: a module written for the test, holding
// each shape of need argument once, the old db/pg harness's among them, so
// the check is seen to refuse each one it must and nothing else.
package need

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureNeed is the fixture's need package: one registered need, one
// constant that is not one, and Require.
const fixtureNeed = `package need

import "testing"

const (
	Docker       = "docker"
	Unregistered = "pg_dump"
)

func Require(t testing.TB, what string, available bool, why string) {}
`

// fixtureSupport is the fixture's testsupport: Require forwarding its
// caller's need, Download handing over a Pin's What, and Pins with a
// registered need, an unregistered one and none.
const fixtureSupport = `package testsupport

import (
	"testing"

	"example.com/m/internal/testsupport/need"
)

type Pin struct{ What, URL string }

var Good = Pin{What: need.Docker}

var Bad = Pin{What: "toybox"} // PIN-UNKNOWN

var Unnamed = Pin{URL: "x"} // PIN-NO-WHAT

func Require(t testing.TB, what string, available bool, why string) { need.Require(t, what, available, why) }

func Download(t testing.TB, pin Pin) { Require(t, pin.What, false, "") }
`

// fixtureUser is a package calling Require every way the check reads.
const fixtureUser = `package user_test

import (
	"os/exec"
	"testing"

	"example.com/m/internal/testsupport"
	"example.com/m/internal/testsupport/need"
)

// requireTools is the old db/pg harness's shape: a loop variable.
func requireTools(t *testing.T) {
	for _, name := range []string{"pg_dump", "pg_restore"} {
		_, err := exec.LookPath(name)
		testsupport.Require(t, name, err == nil, name+" is not on PATH") // OLD-SHAPE
	}
}

var tool = "pg_dump"

func unknownVar(t *testing.T) { testsupport.Require(t, tool, false, "") } // UNKNOWN-VAR

const pinned = need.Unregistered

func unknownConst(t *testing.T) { testsupport.Require(t, pinned, false, "") } // UNKNOWN-CONST

func unknownLiteral(t *testing.T) { need.Require(t, "pg_restore", false, "") } // UNKNOWN-LITERAL

var mutable = need.Docker

func reassigned(t *testing.T) {
	mutable = "anything"
	testsupport.Require(t, mutable, false, "") // REASSIGNED
}

func asValue(t *testing.T) {
	f := testsupport.Require // VALUE
	f(t, "docker", false, "")
}

func helper(t *testing.T, what string) { testsupport.Require(t, what, false, "") }

func forwarded(t *testing.T) {
	helper(t, need.Docker)
	helper(t, "psql") // FORWARDED
}

func Exported(t *testing.T, what string) { need.Require(t, what, false, "") } // EXPORTED

func closure(t *testing.T) {
	func(n string) { testsupport.Require(t, n, false, "") }("docker") // CLOSURE
}

var good = need.Docker

func fine(t *testing.T) {
	const local = need.Docker
	testsupport.Require(t, local, false, "")
	testsupport.Require(t, good, false, "")
	testsupport.Require(t, (need.Docker), false, "")
	need.Require(t, "docker", false, "")
}

// Require here is another function of the same name, which is not checked.
func Require(a, b, c, d string) {}

func decoy() {
	x := "anything"
	Require("", x, "", "")
}
`

// TestRequireCheck_RefusesWhatItCannotResolve: in the fixture, the old
// db/pg harness's loop variable, a closure's parameter and a variable
// assigned again cannot be resolved and are refused; a variable, a
// constant, a literal and a forwarder's argument naming an unregistered
// need are refused naming it; Require used as a value, an exported
// forwarder, a Pin with an unregistered What or none, and an allow entry
// nothing needs are each refused. Every need named through a constant, a
// local constant, an unexported variable set once, a literal or a
// forwarder's argument resolves, and an unrelated function named Require is
// left alone: exactly the expected problems are found, and no others.
func TestRequireCheck_RefusesWhatItCannotResolve(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                              "module example.com/m\n\ngo 1.22\n",
		"internal/testsupport/need/need.go":   fixtureNeed,
		"internal/testsupport/testsupport.go": fixtureSupport,
		"user/user_test.go":                   fixtureUser,
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	allow := map[string]string{
		"internal/testsupport.Download": "its Pins are checked",
		"user.Nothing":                  "listed, and needed by no call",
	}
	got, err := checkRequireCalls(root, []string{"docker"}, allow)
	if err != nil {
		t.Fatal(err)
	}
	at := func(file, marker string) string {
		for i, line := range strings.Split(files[file], "\n") {
			if strings.Contains(line, "// "+marker) {
				return fmt.Sprintf("%s:%d: ", file, i+1)
			}
		}
		t.Fatalf("no marker %s in %s", marker, file)
		return ""
	}
	want := []string{
		at("user/user_test.go", "OLD-SHAPE") + "the need passed to Require cannot be resolved",
		at("user/user_test.go", "UNKNOWN-VAR") + `Require is given need "pg_dump", which is not in need.Names`,
		at("user/user_test.go", "UNKNOWN-CONST") + `Require is given need "pg_dump", which is not in need.Names`,
		at("user/user_test.go", "UNKNOWN-LITERAL") + `Require is given need "pg_restore", which is not in need.Names`,
		at("user/user_test.go", "REASSIGNED") + "the need passed to Require cannot be resolved",
		at("user/user_test.go", "VALUE") + "Require is used as a value",
		at("user/user_test.go", "FORWARDED") + `helper is given need "psql", which is not in need.Names`,
		at("user/user_test.go", "EXPORTED") + "Exported is exported and passes its parameter to Require",
		at("user/user_test.go", "CLOSURE") + "the need passed to Require cannot be resolved",
		at("internal/testsupport/testsupport.go", "PIN-UNKNOWN") + `a Pin's What is given need "toybox", which is not in need.Names`,
		at("internal/testsupport/testsupport.go", "PIN-NO-WHAT") + "a Pin with no What: key",
		"allow: allow lists user.Nothing, which no call needed",
	}
	matched := make([]bool, len(got.problems))
	for _, w := range want {
		found := false
		for i, p := range got.problems {
			if !matched[i] && strings.HasPrefix(p, w) {
				matched[i], found = true, true
				break
			}
		}
		if !found {
			t.Errorf("no problem %q", w)
		}
	}
	for i, p := range got.problems {
		if !matched[i] {
			t.Errorf("a problem the fixture does not hold: %s", p)
		}
	}
	for _, name := range []string{"docker", "pg_dump", "pg_restore", "psql", "toybox"} {
		if !got.seen[name] {
			t.Errorf("%q was not resolved: seen %v", name, got.seen)
		}
	}
	if got.seen["anything"] {
		t.Error("the unrelated Require, or the reassigned variable, was read as a need")
	}
}
