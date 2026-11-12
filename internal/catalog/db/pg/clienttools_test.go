// Which pg_dump and pg_restore the harness lets a test run, and which stop
// a machine that cannot stand in for the device gets.
//
// The harness's device runs its commands on this machine, so the machine's
// own tools are the device's (FAILURE_PATTERNS 476): db.pg.dump and
// db.pg.restore refuse a device whose timeout takes no -k, and a client
// the fixture's server cannot take fails a dump or a restore before it
// loads a row (FAILURE_PATTERNS 279). Each lack is decided here, not left
// to surface as a confusing failure inside the method under test:
//
//   - When testsupport.UsePostgresClientTools put the fixture's own major
//     version first on PATH, the programs PATH then finds must be that
//     version. Anything else means the harness did not get what it asked
//     for, and the test fails.
//   - When it did not (no such directory here, as on a Mac or a hosted
//     runner), the machine's default client is used when the server takes
//     it. One it does not take, or none, stops the test through
//     testsupport.Require(need.PostgresClient): a skip naming why, or a
//     failure in a run that requires the need.
//
// What is ruled out is the old shape, where the answer was ignored and a
// mismatched client failed deep inside a restore as if the product had.
package pg_test

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// transactionTimeoutMajor is the first release whose pg_dump and
// pg_restore send transaction_timeout, which an older server refuses.
const transactionTimeoutMajor = 17

// clientPrograms are the programs the device's tests run.
var clientPrograms = []string{"pg_dump", "pg_restore"}

// clientVersionPattern reads a client program's major version from its
// --version, as the product's toolMajor does.
var clientVersionPattern = regexp.MustCompile(`\(PostgreSQL\) (\d+)`)

// toolsVerdict is what the harness does about this machine's client.
// Both empty means the test goes on.
type toolsVerdict struct {
	// broken is a harness that did not get what it asked for: the test
	// fails with it.
	broken string
	// missing is why this machine lacks a usable client: the test stops
	// through Require(need.PostgresClient).
	missing string
}

// judgeTools decides toolsVerdict for a server of major version server.
// pinned is UsePostgresClientTools' answer, and majorOf reads a program's
// major version and path as PATH finds it.
func judgeTools(pinned bool, server int, majorOf func(program string) (int, string, error)) toolsVerdict {
	for _, program := range clientPrograms {
		major, path, err := majorOf(program)
		switch {
		case pinned && err != nil:
			return toolsVerdict{broken: fmt.Sprintf("UsePostgresClientTools put PostgreSQL %d's client programs first on PATH, but %s did not answer: %v", server, program, err)}
		case pinned && major != server:
			return toolsVerdict{broken: fmt.Sprintf("UsePostgresClientTools put PostgreSQL %d's client programs first on PATH, but the %s PATH finds (%s) is %d", server, program, path, major)}
		case err != nil:
			return toolsVerdict{missing: fmt.Sprintf("%s: %v; install PostgreSQL %d's client programs (postgresql-client-%d)", program, err, server, server)}
		case major < server:
			return toolsVerdict{missing: fmt.Sprintf("the %s on PATH (%s) is %d, older than the PostgreSQL %d fixture, which db.pg.dump refuses; install postgresql-client-%d", program, path, major, server, server)}
		case major >= transactionTimeoutMajor && server < transactionTimeoutMajor:
			return toolsVerdict{missing: fmt.Sprintf("the %s on PATH (%s) is %d, which sends transaction_timeout, a setting the PostgreSQL %d fixture does not have; install postgresql-client-%d", program, path, major, server, server)}
		}
	}
	return toolsVerdict{}
}

// stopUnlessUsable stops t as v says, or lets it go on.
func stopUnlessUsable(t testing.TB, v toolsVerdict) {
	t.Helper()
	switch {
	case v.broken != "":
		t.Fatal(v.broken)
	case v.missing != "":
		testsupport.Require(t, need.PostgresClient, false, v.missing)
	}
}

// majorOnPath is program's major version, by its own --version, and the
// path PATH finds it at: the one the device's shell runs.
func majorOnPath(program string) (int, string, error) {
	path, err := exec.LookPath(program)
	if err != nil {
		return 0, "", errors.New("not on PATH")
	}
	out, err := exec.Command(path, "--version").Output() // #nosec G204 -- a program PATH found, asked only its version
	if err != nil {
		return 0, path, fmt.Errorf("%s --version: %w", path, err)
	}
	m := clientVersionPattern.FindSubmatch(out)
	if m == nil {
		return 0, path, fmt.Errorf("%s --version answered %q, which names no PostgreSQL version", path, out)
	}
	major, err := strconv.Atoi(string(m[1]))
	return major, path, err
}

// hostTimeoutTakesKill reports whether this machine's timeout takes -k
// and a duration, asked exactly as db.pg's requireTimeout asks a device.
func hostTimeoutTakesKill() bool {
	return exec.Command("/bin/sh", "-c", "timeout -k 10s 5s true").Run() == nil
}
