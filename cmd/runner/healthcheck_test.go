// Tests for the runner's argument routing and its healthcheck
// subcommand.
//
// The routing case is the one that matters most and is the reason
// routeFor is a function rather than two statements inside main(). Before
// it existed, this binary treated every unrecognised first argument as an
// ordinary start, so a chart that ran `/app/runner healthcheck` as a
// liveness probe would have launched a second Runner Agent into the
// consumer group every few seconds. That is a defect no test could have
// caught while the decision lived inside main().
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
)

func TestRouteFor(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want commandRoute
	}{
		{
			name: "no arguments is the agent",
			args: nil,
			want: routeAgent,
		},
		{
			name: "the collection child argument is the child",
			args: []string{native.InternalCollectionRunnerArg},
			want: routeCollectionChild,
		},
		{
			// THE CASE THE CHART DEPENDS ON. The runner Deployment's
			// liveness probe execs exactly this argument vector, so a
			// regression here would leave every runner pod either
			// permanently unhealthy or, worse, spawning a second agent
			// per probe interval.
			name: "healthcheck is the probe",
			args: []string{healthcheckCommand},
			want: routeHealthcheck,
		},
		{
			name: "healthcheck with its own flags is still the probe",
			args: []string{healthcheckCommand, "--max-age=30s"},
			want: routeHealthcheck,
		},
		{
			name: "version prints the build's version",
			args: []string{versionCommand},
			want: routeVersion,
		},
		{
			// Unchanged, deliberately: every deployment that passes this
			// binary something it does not know still gets an Agent, which
			// is the behavior they already have.
			name: "an unknown argument still starts the agent",
			args: []string{"--not-a-flag-this-binary-has"},
			want: routeAgent,
		},
		{
			name: "an argument after the collection child does not change the route",
			args: []string{native.InternalCollectionRunnerArg, healthcheckCommand},
			want: routeCollectionChild,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeFor(tc.args); got != tc.want {
				t.Fatalf("routeFor(%q) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestHeartbeatPath(t *testing.T) {
	t.Run("unset takes the built-in default", func(t *testing.T) {
		unsetForTest(t, heartbeatFileEnv)
		if got := heartbeatPath(); got != runner.DefaultHeartbeatPath {
			t.Fatalf("heartbeatPath() = %q, want the default %q", got, runner.DefaultHeartbeatPath)
		}
	})

	t.Run("a value moves the heartbeat", func(t *testing.T) {
		t.Setenv(heartbeatFileEnv, "/var/run/pleiades/beat")
		if got := heartbeatPath(); got != "/var/run/pleiades/beat" {
			t.Fatalf("heartbeatPath() = %q, want the configured path", got)
		}
	})

	t.Run("set but empty turns it off", func(t *testing.T) {
		// The escape hatch for a deployment with a read-only root and
		// nothing writable mounted. It has to be distinguishable from
		// unset, which is why this reads LookupEnv rather than the getenv
		// helper main.go uses everywhere else.
		t.Setenv(heartbeatFileEnv, "")
		if got := heartbeatPath(); got != "" {
			t.Fatalf("heartbeatPath() = %q, want the empty path that means disabled", got)
		}
	})
}

func TestHeartbeatInterval(t *testing.T) {
	t.Run("unset takes the built-in default", func(t *testing.T) {
		unsetForTest(t, heartbeatIntervalEnv)
		got, err := heartbeatInterval()
		if err != nil {
			t.Fatalf("heartbeatInterval: %v", err)
		}
		if got != runner.DefaultHeartbeatInterval {
			t.Fatalf("heartbeatInterval() = %s, want %s", got, runner.DefaultHeartbeatInterval)
		}
	})

	t.Run("a duration is honored", func(t *testing.T) {
		t.Setenv(heartbeatIntervalEnv, "45s")
		got, err := heartbeatInterval()
		if err != nil {
			t.Fatalf("heartbeatInterval: %v", err)
		}
		if got != 45*time.Second {
			t.Fatalf("heartbeatInterval() = %s, want 45s", got)
		}
	})

	t.Run("a malformed value is an error rather than a silent default", func(t *testing.T) {
		// A silent fallback here produces a runner beating on a cadence
		// the operator's probe was not configured for, which presents as
		// a liveness probe restarting healthy pods and points at nothing.
		t.Setenv(heartbeatIntervalEnv, "ten seconds")
		if _, err := heartbeatInterval(); err == nil {
			t.Fatal("accepted a value that is not a Go duration")
		}
	})

	t.Run("a non-positive value is an error", func(t *testing.T) {
		t.Setenv(heartbeatIntervalEnv, "0s")
		if _, err := heartbeatInterval(); err == nil {
			t.Fatal("accepted an interval that never beats")
		}
	})
}

func TestRunHealthcheckExitCodes(t *testing.T) {
	t.Run("a fresh heartbeat exits 0", func(t *testing.T) {
		path := writeBeat(t, time.Now())
		if code := runHealthcheck([]string{healthcheckCommand, "-path", path}); code != 0 {
			t.Fatalf("runHealthcheck = %d, want 0", code)
		}
	})

	t.Run("a stale heartbeat exits 1", func(t *testing.T) {
		path := writeBeat(t, time.Now().Add(-10*time.Minute))
		if code := runHealthcheck([]string{healthcheckCommand, "-path", path}); code != 1 {
			t.Fatalf("runHealthcheck = %d, want 1 (unhealthy)", code)
		}
	})

	t.Run("a max-age below the file's age exits 1", func(t *testing.T) {
		path := writeBeat(t, time.Now().Add(-31*time.Second))
		if code := runHealthcheck([]string{healthcheckCommand, "-path", path, "-max-age=30s"}); code != 1 {
			t.Fatalf("runHealthcheck = %d, want 1 (unhealthy)", code)
		}
	})

	t.Run("a missing heartbeat exits 1, not 2", func(t *testing.T) {
		// This is the cold-start window: the agent writes its first beat
		// only after its first successful round trip to the broker. A 2
		// here would tell the operator their configuration was wrong on
		// every single start.
		path := filepath.Join(t.TempDir(), "never-written")
		if code := runHealthcheck([]string{healthcheckCommand, "-path", path}); code != 1 {
			t.Fatalf("runHealthcheck = %d, want 1 (not started beating yet)", code)
		}
	})

	t.Run("a heartbeat turned off exits 2", func(t *testing.T) {
		t.Setenv(heartbeatFileEnv, "")
		if code := runHealthcheck([]string{healthcheckCommand}); code != 2 {
			t.Fatalf("runHealthcheck = %d, want 2 (caller error)", code)
		}
	})

	t.Run("an unknown flag exits 2", func(t *testing.T) {
		if code := runHealthcheck([]string{healthcheckCommand, "-not-a-flag"}); code != 2 {
			t.Fatalf("runHealthcheck = %d, want 2 (caller error)", code)
		}
	})

	t.Run("an unstattable path exits 2", func(t *testing.T) {
		// A path whose parent is a FILE cannot be stat'ed and never will
		// be, so no amount of waiting fixes it. That is a caller error
		// rather than an unhealthy runner.
		parent := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
			t.Fatalf("writing the blocking file: %v", err)
		}
		if code := runHealthcheck([]string{healthcheckCommand, "-path", filepath.Join(parent, "heartbeat")}); code != 2 {
			t.Fatalf("runHealthcheck = %d, want 2 (caller error)", code)
		}
	})
}

func TestHealthcheckExitFor(t *testing.T) {
	if got := healthcheckExitFor(runner.ErrHeartbeatMissing); got != 1 {
		t.Fatalf("a missing heartbeat maps to %d, want 1", got)
	}
	stale := &runner.StaleHeartbeatError{Path: "/tmp/x", Age: time.Minute, MaxAge: time.Second}
	if got := healthcheckExitFor(stale); got != 1 {
		t.Fatalf("a stale heartbeat maps to %d, want 1", got)
	}
	if got := healthcheckExitFor(errors.New("permission denied")); got != 2 {
		t.Fatalf("an unreadable path maps to %d, want 2", got)
	}
}

// unsetForTest removes an environment variable for the duration of one
// test and restores whatever the process had before.
//
// t.Setenv first, even though the value is thrown away immediately, so
// that the restore is registered: os.Unsetenv alone would leak the
// removal into every later test in this binary, which is the shape of
// contamination that makes a suite pass or fail depending on its order.
func unsetForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "about to be removed")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetting %s: %v", key, err)
	}
}

// writeBeat writes a heartbeat file stamped at moment and returns its
// path. The MODIFICATION TIME is what the check reads, which is why this
// helper sets it rather than relying on the contents.
func writeBeat(t *testing.T, moment time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "heartbeat")
	if err := os.WriteFile(path, []byte(moment.Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		t.Fatalf("writing the heartbeat: %v", err)
	}
	if err := os.Chtimes(path, moment, moment); err != nil {
		t.Fatalf("stamping the heartbeat: %v", err)
	}
	return path
}
