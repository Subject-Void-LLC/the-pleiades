// The runner's healthcheck subcommand: this binary reading the liveness
// heartbeat its own Agent writes and turning that file's freshness into a
// process exit code. It also owns where the heartbeat lives and how often
// it beats, because the writer in main() and the reader here have to
// resolve the same path from the same place or the probe checks a file
// nobody writes.
//
// # Why the binary has to answer this itself
//
// A container probe runs INSIDE the container it checks, so it can only
// execute a program that image already contains. The runtime image is
// gcr.io/distroless/base-debian12:nonroot, whose /bin, /sbin, /usr/bin
// and /usr/sbin are empty: no shell, no curl, no wget, no busybox. The
// only executable in the whole image is the runner binary. So either this
// binary can answer for itself or the container has no probe at all.
//
// # Why not the controller's answer
//
// cmd/controller/healthcheck.go probes /readyz on the controller's own
// HTTP listener. This process has no listener and should not grow one: it
// dials out to the broker and pulls from a durable consumer group, so a
// port here would be a thing to configure and defend for the sake of one
// question. The heartbeat file answers the same question with no socket
// (internal/runner/heartbeat.go says what a written beat proves, and what
// it misses).
//
// # Why running the plain binary would have been worse than no probe
//
// Before this file existed, cmd/runner treated any first argument it did
// not recognise as an ordinary start, so `exec: ["/app/runner",
// "healthcheck"]` would have launched a SECOND runner agent into the
// consumer group every few seconds, forever. routeFor below is what makes
// that argument mean something instead, and TestRouteFor pins it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
)

// healthcheckCommand is the single argument that selects the probe.
const healthcheckCommand = "healthcheck"

// heartbeatFileEnv names the environment variable that moves the
// heartbeat, read by the writer in main() and by the probe here.
//
// SET BUT EMPTY turns the heartbeat off entirely, which is the escape
// hatch for a deployment whose root filesystem is read-only with nothing
// writable mounted anywhere. It is spelled that way because an unset
// variable has to keep meaning "use the default": the heartbeat is on by
// default, since a probe nobody enabled is a probe nobody has.
const heartbeatFileEnv = "RUNNER_HEARTBEAT_FILE"

// heartbeatIntervalEnv names the environment variable that changes how
// often a beat is attempted, as a Go duration ("10s", "1m").
//
// It exists because the interval and the probe's staleness limit are one
// setting in two places: an operator who lengthens one has to be able to
// lengthen the other, and a chart that could only change the probe would
// force every deployment onto this binary's cadence.
const heartbeatIntervalEnv = "RUNNER_HEARTBEAT_INTERVAL"

// commandRoute names which of the three things this binary is being asked
// to be, resolved from its arguments and nothing else.
type commandRoute int

const (
	// routeAgent is the argument-free case: run the execution plane. This
	// is the only route that returns to main() rather than exiting.
	routeAgent commandRoute = iota

	// routeCollectionChild is the per-task subprocess the native adapter
	// re-executes this binary as (PLAN.md Section 17.5).
	routeCollectionChild

	// routeHealthcheck is the container probe (this file).
	routeHealthcheck

	// routeVersion prints this build's version (internal/buildinfo) and
	// exits, the same string `pleiades version` prints.
	routeVersion
)

// versionCommand is the argument that selects routeVersion.
const versionCommand = "version"

// isCollectionChildCommand reports whether args select the per-task
// collection subprocess.
func isCollectionChildCommand(args []string) bool {
	return len(args) > 0 && args[0] == native.InternalCollectionRunnerArg
}

// isHealthcheckCommand reports whether args select the probe.
func isHealthcheckCommand(args []string) bool {
	return len(args) > 0 && args[0] == healthcheckCommand
}

// routeFor resolves an argument vector to exactly one route.
//
// It exists so the ORDER of the guards is a value a test can assert on
// rather than a property of statements inside main(), which is the same
// reason cmd/controller/healthcheck.go has a function of this name. The
// controller's own comment records what that bought there: reversing two
// guards left the whole package green while the shipped binary answered
// `controller healthcheck` with "unknown command".
//
// The collection child comes first because it is the hot path (one
// re-exec per task, and that child must pay for nothing else) and because
// it must never be shadowed. The default comes last and is deliberately
// permissive: an argument this binary does not know still starts the
// Agent, which is the behavior every existing deployment already has.
func routeFor(args []string) commandRoute {
	if isCollectionChildCommand(args) {
		return routeCollectionChild
	}
	if isHealthcheckCommand(args) {
		return routeHealthcheck
	}
	if len(args) > 0 && args[0] == versionCommand {
		return routeVersion
	}
	return routeAgent
}

// heartbeatPath resolves where the heartbeat lives, for both the writer
// and the probe.
//
// os.LookupEnv rather than the getenv helper in main.go, because the two
// answer different questions. getenv folds "unset" and "set to empty"
// together, and here they mean opposite things: unset takes the default
// and empty turns the heartbeat off.
func heartbeatPath() runner.HeartbeatPath {
	if value, ok := os.LookupEnv(heartbeatFileEnv); ok {
		return runner.HeartbeatPath(value)
	}
	return runner.DefaultHeartbeatPath
}

// heartbeatInterval resolves how often a beat is attempted.
//
// A malformed value is FATAL here, unlike envInt's silent fallback for
// RUNNER_POOL_SIZE, and the difference is deliberate. A pool size that
// falls back to the default runs the same work a bit differently. An
// interval that falls back to the default while an operator believes they
// set a longer one produces a Runner beating on a cadence their probe was
// not configured for, which shows up as a liveness probe restarting
// healthy pods and points at nothing.
func heartbeatInterval() (time.Duration, error) {
	value := os.Getenv(heartbeatIntervalEnv)
	if value == "" {
		return runner.DefaultHeartbeatInterval, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a Go duration (for example 10s): %w", heartbeatIntervalEnv, value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s=%q is not positive, and a heartbeat that never beats reports every runner unhealthy", heartbeatIntervalEnv, value)
	}
	return parsed, nil
}

// runHealthcheck reads the heartbeat and returns a process exit code: 0
// only when it is fresh, non-zero otherwise.
//
// args includes the subcommand name at index 0, matching
// cmd/controller's runHealthcheck, so the two read side by side.
//
// The exit codes follow that binary exactly: 2 for a caller error (a flag
// this command does not have, a path it cannot stat, a heartbeat that was
// turned off) and 1 for a runner that is not healthy. Docker and the
// kubelet both treat every non-zero code as a failure, so the split is
// for the human reading the logs.
func runHealthcheck(args []string) int {
	flags := flag.NewFlagSet(healthcheckCommand, flag.ContinueOnError)
	path := flags.String("path", heartbeatPath().String(),
		"the heartbeat file to read; defaults to "+heartbeatFileEnv+", then to the built-in path")
	maxAge := flags.Duration("max-age", runner.DefaultHeartbeatMaxAge,
		"how old the heartbeat may be before this runner is reported unhealthy")
	if err := flags.Parse(args[1:]); err != nil {
		// flag has already written the reason and the usage to stderr.
		return 2
	}

	if *path == "" {
		fmt.Fprintf(os.Stderr,
			"runner %s: the heartbeat is turned off (%s is set and empty), so there is nothing here to read. Either unset %s so this runner writes one, or remove this probe.\n",
			healthcheckCommand, heartbeatFileEnv, heartbeatFileEnv)
		return 2
	}

	target := runner.HeartbeatPath(*path)
	if err := runner.CheckHeartbeat(target, *maxAge, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "runner %s: %v\n", healthcheckCommand, err)
		return healthcheckExitFor(err)
	}

	// Printed rather than silent, because the one other way to run this
	// is a human typing `kubectl exec ... -- /app/runner healthcheck`,
	// and an empty success tells them nothing about how close to the
	// limit the runner was. A probe ignores stdout.
	age, err := runner.HeartbeatAge(target, time.Now())
	if err != nil {
		// The file was there a moment ago and is not now, which means the
		// process is shutting down or something else is writing this path.
		// Not healthy, and not a caller error either.
		fmt.Fprintf(os.Stderr, "runner %s: %v\n", healthcheckCommand, err)
		return 1
	}
	fmt.Printf("healthy: the heartbeat at %s is %s old (limit %s)\n",
		target, age.Round(time.Millisecond), *maxAge)
	return 0
}

// healthcheckExitFor maps a CheckHeartbeat failure to an exit code.
//
// A missing heartbeat and a stale one are both "this runner is not
// healthy" (1). A missing file is the cold-start window specifically: the
// Agent writes its first beat only after its first successful round trip
// to the broker, so reporting 2 there would tell an operator their
// configuration was wrong during every single start. Everything else,
// meaning a path this process cannot even stat, is a caller error (2)
// because no amount of waiting fixes it.
func healthcheckExitFor(err error) int {
	var stale *runner.StaleHeartbeatError
	if errors.Is(err, runner.ErrHeartbeatMissing) || errors.As(err, &stale) {
		return 1
	}
	return 2
}
