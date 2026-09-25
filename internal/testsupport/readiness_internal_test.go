// Tests for the readiness bounds, run without Docker.
//
// The deadline a wait strategy carries is unexported in testcontainers, so
// these read it through reflection. That is deliberate rather than a
// shortcut: the claim being tested is a claim about a value the library
// hides, and the alternative is waiting two minutes for a container that
// never becomes ready to find out when it gives up.
package testsupport

import (
	"fmt"
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

// unboundedSteps returns a description of each step in opt's wait strategy
// that does not carry bound as its own startup timeout. A step with none
// set gives up at the library's sixty seconds whatever the group allows.
func unboundedSteps(t *testing.T, opt testcontainers.CustomizeRequestOption, bound time.Duration) []string {
	t.Helper()
	var req testcontainers.GenericContainerRequest
	if err := opt(&req); err != nil {
		t.Fatalf("applying the option: %v", err)
	}
	ms, ok := req.WaitingFor.(*wait.MultiStrategy)
	if !ok {
		t.Fatalf("the option left a %T, want a *wait.MultiStrategy", req.WaitingFor)
	}
	if len(ms.Strategies) == 0 {
		t.Fatal("the strategy has no steps, so there is nothing to check")
	}
	var out []string
	for _, step := range ms.Strategies {
		st, ok := step.(wait.StrategyTimeout)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%T reports no timeout at all", step))
		case st.Timeout() == nil:
			out = append(out, fmt.Sprintf("%T sets none, so it stops at the library's sixty seconds", step))
		case *st.Timeout() != bound:
			out = append(out, fmt.Sprintf("%T sets %v", step, *st.Timeout()))
		}
	}
	return out
}

// TestReadinessCarriesTheAgreedBound reads back both bounds each helper
// sets: the group's deadline, and every step's own startup timeout, which
// is the one a step actually stops at (FAILURE_PATTERNS 350).
func TestReadinessCarriesTheAgreedBound(t *testing.T) {
	for name, opt := range map[string]testcontainers.CustomizeRequestOption{
		"PostgresReady":   PostgresReady(),
		"ToxiproxyReady":  ToxiproxyReady(),
		"LocalStackReady": LocalStackReady(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := deadlineOf(t, opt); got != ContainerStartupTimeout {
				t.Errorf("%s waits under %v, want ContainerStartupTimeout (%v)", name, got, ContainerStartupTimeout)
			}
			for _, step := range unboundedSteps(t, opt, ContainerStartupTimeout) {
				t.Errorf("%s: a step %s, want ContainerStartupTimeout (%v)", name, step, ContainerStartupTimeout)
			}
		})
	}
}

// TestUnboundedStepsDetects is the negative control: the shape the first
// version of PostgresReady had, a group deadline over steps that set
// nothing, is reported step by step.
func TestUnboundedStepsDetects(t *testing.T) {
	groupOnly := testcontainers.WithWaitStrategyAndDeadline(ContainerStartupTimeout,
		wait.ForLog("ready"), wait.ForListeningPort("5432/tcp").WithStartupTimeout(time.Minute))
	if got := unboundedSteps(t, groupOnly, ContainerStartupTimeout); len(got) != 2 {
		t.Errorf("found %q, want both steps: one with no timeout and one with the wrong one", got)
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
// toxiproxy or LocalStack started without its helper waits under the
// module's.
func TestNoContainerWaitsUnderTheLibraryDeadline(t *testing.T) {
	root := RepoRoot(t)
	var checked, localstacks int
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
		if n := strings.Count(text, "localstack.Run("); n > 0 {
			localstacks += n
			if got := strings.Count(text, "LocalStackReady()"); got < n {
				t.Errorf("%s starts LocalStack %d time(s) but passes testsupport.LocalStackReady() %d time(s); the rest wait under the module's sixty second group deadline", rel, n, got)
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
	if localstacks == 0 {
		t.Fatal("found no LocalStack start at all, so this guard is checking nothing")
	}
}
