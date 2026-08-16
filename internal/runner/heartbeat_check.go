// Package runner: the read side of the liveness heartbeat, which is what
// `runner healthcheck` runs and therefore what every orchestrator probe
// really asks.
//
// It lives beside the writer (heartbeat.go) rather than in cmd/runner on
// purpose. The two halves have to agree on one thing, which file property
// carries the signal, and a rule split across a package boundary is a
// rule that drifts. Keeping them together also means a unit test can
// write a beat and judge it through the same pair of functions the
// shipped binary uses.
package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// DefaultHeartbeatMaxAge is how old a heartbeat may be before
// CheckHeartbeat calls it stale, when nothing states otherwise.
//
// Six intervals at DefaultHeartbeatInterval. The ratio is the point
// rather than the number: a threshold near the interval would flap on any
// single slow round trip, and one much larger would leave a genuinely
// dead subscription reporting healthy for minutes. Six missed beats in a
// row is not a blip.
//
// This is the LIVENESS number, so it is deliberately the more forgiving
// of the two the chart uses. Crossing it gets a pod restarted, which
// abandons whatever that Runner was executing, so the bar for it is a
// broker that has been unreachable for a minute rather than one that
// hiccupped. helm/the-pleiades passes a shorter one for readiness, where
// the only consequence is that a rollout waits.
const DefaultHeartbeatMaxAge = 60 * time.Second

// ErrHeartbeatMissing reports that no heartbeat file exists at all.
//
// It is a distinct error because it means something different from
// staleness and needs the same answer for a different reason. A Runner
// writes its first beat only after its first successful round trip to the
// broker, and NewHeartbeat removes any file a previous container left
// behind, so absence means "this process has not reached its consumer
// yet". That is the ordinary cold start, and it is correctly reported as
// not-healthy rather than as a configuration fault.
var ErrHeartbeatMissing = errors.New("no heartbeat has been written yet")

// StaleHeartbeatError reports a heartbeat that exists but has stopped
// advancing, which is FAILURE_PATTERNS.md #119 seen from the outside.
//
// It carries the numbers rather than only a message so a caller can act
// on them and a human reading a probe failure can tell "one second over"
// from "the broker has been gone for an hour".
type StaleHeartbeatError struct {
	// Path is the file that was checked.
	Path HeartbeatPath

	// Age is how long ago it was last written.
	Age time.Duration

	// MaxAge is the threshold it exceeded.
	MaxAge time.Duration
}

// Error describes the staleness in the terms an operator has to act on.
func (e *StaleHeartbeatError) Error() string {
	return fmt.Sprintf(
		"the heartbeat at %s was last written %s ago, which is past the %s limit: this runner is still running but is no longer attached to its durable NATS consumer",
		e.Path, e.Age.Round(time.Millisecond), e.MaxAge)
}

// CheckHeartbeat reports whether the heartbeat at path is fresh enough,
// returning nil when it is.
//
// now is a parameter rather than a call to time.Now inside, so a test can
// state the moment it is asking about instead of sleeping to reach one.
//
// The MODIFICATION TIME is the signal, not the file's contents. Two
// reasons, and the second is the load-bearing one. The kernel records
// mtime as a consequence of the write itself, so it cannot disagree with
// whether the write happened, while a timestamp inside the file is a
// claim the writer makes and could get wrong. And this runs every few
// seconds for the life of the container against a path that comes from
// configuration; os.Stat answers the only question a probe asks without
// ever opening that path.
func CheckHeartbeat(path HeartbeatPath, maxAge time.Duration, now time.Time) error {
	if path == "" {
		return errors.New("no heartbeat path was given")
	}
	if maxAge <= 0 {
		return fmt.Errorf("the staleness limit is %s, which no heartbeat can ever satisfy", maxAge)
	}

	info, err := os.Stat(path.String())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w at %s", ErrHeartbeatMissing, path)
		}
		// Anything else (an unreadable directory, a path that is not a
		// file) is a fault in how this probe was pointed, not an answer
		// about the Runner. The caller maps it to a different exit code.
		return fmt.Errorf("reading the heartbeat at %s: %w", path, err)
	}

	// Negative ages are possible (a clock stepping backwards, a file
	// stamped in the future) and are deliberately NOT staleness. A
	// heartbeat from the future is a clock problem to investigate, and
	// restarting the Runner would not fix it.
	age := now.Sub(info.ModTime())
	if age > maxAge {
		return &StaleHeartbeatError{Path: path, Age: age, MaxAge: maxAge}
	}
	return nil
}

// HeartbeatAge reports how long ago the heartbeat at path was written.
//
// Split out so a caller that wants to REPORT the age (the healthcheck
// subcommand's success line, a test asserting the file stopped advancing)
// does not have to re-derive it from a nil error, and so both callers read
// the same file property.
func HeartbeatAge(path HeartbeatPath, now time.Time) (time.Duration, error) {
	info, err := os.Stat(path.String())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("%w at %s", ErrHeartbeatMissing, path)
		}
		return 0, fmt.Errorf("reading the heartbeat at %s: %w", path, err)
	}
	return now.Sub(info.ModTime()), nil
}
