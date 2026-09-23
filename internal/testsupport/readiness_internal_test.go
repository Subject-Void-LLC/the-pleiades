// Tests for the readiness bounds, run without Docker.
//
// The deadline a wait strategy carries is unexported in testcontainers, so
// these read it through reflection. That is deliberate rather than a
// shortcut: the claim being tested is a claim about a value the library
// hides, and the alternative is waiting two minutes for a container that
// never becomes ready to find out when it gives up.
package testsupport

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// deadlineOf applies opt to an empty request and returns the deadline its
// resulting wait strategy carries.
func deadlineOf(t *testing.T, opt testcontainers.CustomizeRequestOption) time.Duration {
	t.Helper()
	var req testcontainers.GenericContainerRequest
	if err := opt(&req); err != nil {
		t.Fatalf("applying the option: %v", err)
	}
	ms, ok := req.WaitingFor.(*wait.MultiStrategy)
	if !ok {
		t.Fatalf("the option left a %T, want a *wait.MultiStrategy", req.WaitingFor)
	}
	field := reflect.ValueOf(ms).Elem().FieldByName("deadline")
	if !field.IsValid() {
		t.Fatal("wait.MultiStrategy no longer has a deadline field; testcontainers changed shape, so re-derive this test before trusting it")
	}
	if field.IsNil() {
		t.Fatal("the strategy carries no deadline at all")
	}
	return time.Duration(field.Elem().Int())
}

func TestReadinessCarriesTheAgreedBound(t *testing.T) {
	for name, opt := range map[string]testcontainers.CustomizeRequestOption{
		"PostgresReady":  PostgresReady(),
		"ToxiproxyReady": ToxiproxyReady(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := deadlineOf(t, opt); got != ContainerStartupTimeout {
				t.Errorf("%s waits under %v, want ContainerStartupTimeout (%v)", name, got, ContainerStartupTimeout)
			}
		})
	}
}

// TestTheLibraryDefaultIsTheDefect pins the upstream fact this file
// exists because of, so a dependency bump that changes it is noticed.
//
// If testcontainers ever stops hardcoding sixty seconds here, this fails,
// and the helpers may no longer be needed at all. That is a better outcome
// than keeping a workaround for a problem that went away.
func TestTheLibraryDefaultIsTheDefect(t *testing.T) {
	if got := deadlineOf(t, testpg.BasicWaitStrategies()); got != 60*time.Second {
		t.Errorf("testpg.BasicWaitStrategies() now waits under %v, not sixty seconds; re-check whether PostgresReady is still needed", got)
	}
}

// TestNoContainerWaitsUnderTheLibraryDeadline is the guard on every call
// site, because a helper that one site forgets does nothing for it.
//
// BasicWaitStrategies must not appear at all, since passing it alongside
// PostgresReady re-wraps the strategy under its own sixty seconds. And a
// toxiproxy started without ToxiproxyReady waits under the module's.
func TestNoContainerWaitsUnderTheLibraryDeadline(t *testing.T) {
	root := RepoRoot(t)
	var checked int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "readiness_internal_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- walking this repository's own test files.
		if err != nil {
			return err
		}
		text := string(src)
		rel, _ := filepath.Rel(root, path)
		if strings.Contains(text, "BasicWaitStrategies()") {
			t.Errorf("%s calls testpg.BasicWaitStrategies(), which waits under a hardcoded sixty seconds; use testsupport.PostgresReady() instead", rel)
		}
		if n := strings.Count(text, "tctoxiproxy.Run("); n > 0 {
			checked += n
			if got := strings.Count(text, "ToxiproxyReady()"); got < n {
				t.Errorf("%s starts toxiproxy %d time(s) but passes testsupport.ToxiproxyReady() %d time(s); the rest wait under the module's sixty seconds", rel, n, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if checked == 0 {
		t.Fatal("found no toxiproxy start at all, so this guard is checking nothing")
	}
}
