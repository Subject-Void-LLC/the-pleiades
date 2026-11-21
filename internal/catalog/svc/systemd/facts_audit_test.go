// TestMain: every fact this package's methods emit in its tests is held to
// their declarations (pkg/collection/facttest).
package systemd_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact a method emitted
// that it does not declare and for a fact-declaring method no test drove. The
// tests that drive them run commands through an in-process SSH server's
// /bin/sh, so where there is none the coverage is reported, not enforced.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{Unavailable: need.POSIXShellMissing()}))
}
