// Tests for wait.connection's schedule alone: when it tries, how the gap
// grows, when a streak counts and what it says when it gives up. The tries
// answer from a script, and the schedule tests record each sleep rather
// than sleeping it, so they assert the exact intervals with no wall clock
// to race (FAILURE_PATTERNS 479); connection_test.go proves the tries
// themselves against a real SSH server.
package wait

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

var errRefused = errors.New("connection refused")

// scripted is a try that answers from script, one entry per try, nil for
// an answer, and refuses once the script runs out.
type scripted struct {
	script []error
	tries  int
}

func (s *scripted) try(context.Context) error {
	s.tries++
	if s.tries <= len(s.script) {
		return s.script[s.tries-1]
	}
	return errRefused
}

// recorder is a pace that records every sleep and jitter asked of it,
// sleeping nothing.
type recorder struct {
	slept, asked []time.Duration
	jitter       func(time.Duration) time.Duration
}

// pace is the recorder's jitter, with a sleep that does nothing.
func (r *recorder) pace() connectionPace {
	return connectionPace{
		jitter: func(d time.Duration) time.Duration {
			r.asked = append(r.asked, d)
			if r.jitter != nil {
				return r.jitter(d)
			}
			return d
		},
		sleep: func(context.Context, time.Duration) error { return nil },
	}
}

// recording wraps the recorder's sleep to keep what it was asked.
func (r *recorder) recording() connectionPace {
	p := r.pace()
	p.sleep = func(_ context.Context, d time.Duration) error { r.slept = append(r.slept, d); return nil }
	return p
}

// seconds is a list of whole seconds as durations.
func seconds(ns ...int) []time.Duration {
	var out []time.Duration
	for _, n := range ns {
		out = append(out, time.Duration(n)*time.Second)
	}
	return out
}

// request is a schedule in seconds, with a timeout no test reaches.
func request(sleep, maxSleep, steadyInterval, steady int, exponential bool) connectionRequest {
	return connectionRequest{timeout: time.Hour, sleep: time.Duration(sleep) * time.Second, perTry: time.Second, exponential: exponential,
		maxSleep: time.Duration(maxSleep) * time.Second, steady: steady, steadyInterval: time.Duration(steadyInterval) * time.Second}
}

// TestWaitForConnection_ExponentialBackoff: the gap doubles after each
// failed try from sleep and stops at max_sleep, and what is slept is what
// the jitter makes of each gap.
func TestWaitForConnection_ExponentialBackoff(t *testing.T) {
	s := &scripted{script: []error{errRefused, errRefused, errRefused, errRefused, errRefused, nil}}
	r := &recorder{}
	w, err := waitForConnection(context.Background(), request(2, 10, 0, 1, true), s.try, r.recording())
	if err != nil {
		t.Fatal(err)
	}
	if w.tries != 6 || w.failures != 5 {
		t.Errorf("tries %d failures %d", w.tries, w.failures)
	}
	if want := seconds(2, 4, 8, 10, 10); !slices.Equal(r.slept, want) || !slices.Equal(r.asked, want) {
		t.Errorf("slept %v, asked %v, want %v", r.slept, r.asked, want)
	}

	halved := &recorder{jitter: func(d time.Duration) time.Duration { return d / 2 }}
	if _, err := waitForConnection(context.Background(), request(4, 60, 0, 1, true), (&scripted{script: []error{errRefused, errRefused, nil}}).try, halved.recording()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(halved.asked, seconds(4, 8)) || !slices.Equal(halved.slept, seconds(2, 4)) {
		t.Errorf("asked %v, slept %v", halved.asked, halved.slept)
	}
}

// TestWaitForConnection_FixedBackoff keeps every gap at sleep and never
// asks the jitter.
func TestWaitForConnection_FixedBackoff(t *testing.T) {
	r := &recorder{}
	if _, err := waitForConnection(context.Background(), request(3, 0, 0, 1, false), (&scripted{script: []error{errRefused, errRefused, errRefused, nil}}).try, r.recording()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.slept, seconds(3, 3, 3)) || len(r.asked) != 0 {
		t.Errorf("slept %v, asked jitter %v", r.slept, r.asked)
	}
}

// TestWaitForConnection_Steady: ready only after the streak asked for, its
// answers steady_interval apart; a refusal mid-streak starts the streak
// and the backoff again.
func TestWaitForConnection_Steady(t *testing.T) {
	s := &scripted{script: []error{errRefused, errRefused, nil, nil, errRefused, nil, nil, nil}}
	r := &recorder{}
	w, err := waitForConnection(context.Background(), request(2, 60, 5, 3, true), s.try, r.recording())
	if err != nil {
		t.Fatal(err)
	}
	if w.tries != 8 || w.failures != 3 {
		t.Errorf("tries %d failures %d", w.tries, w.failures)
	}
	// refused, 2, refused, 4, answer, 5, answer, 5, refused, 2 (the backoff
	// started again), answer, 5, answer, 5, answer.
	if want := seconds(2, 4, 5, 5, 2, 5, 5); !slices.Equal(r.slept, want) {
		t.Errorf("slept %v, want %v", r.slept, want)
	}
	once := &recorder{}
	if w, err := waitForConnection(context.Background(), request(2, 0, 0, 1, false), (&scripted{script: []error{nil}}).try, once.recording()); err != nil || w.tries != 1 || len(once.slept) != 0 {
		t.Errorf("a steady of 1 that answered at once: %+v %v slept %v", w, err, once.slept)
	}
}

// TestWaitForConnection_TheDelayComesFirst: the delay is the first sleep,
// before any try.
func TestWaitForConnection_TheDelayComesFirst(t *testing.T) {
	req := request(2, 0, 0, 1, false)
	req.delay = 7 * time.Second
	r := &recorder{}
	if _, err := waitForConnection(context.Background(), req, (&scripted{script: []error{nil}}).try, r.recording()); err != nil || !slices.Equal(r.slept, seconds(7)) {
		t.Errorf("slept %v: %v", r.slept, err)
	}
}

// realPace sleeps for real, for the tests that run out a timeout.
var realPace = connectionPace{jitter: func(d time.Duration) time.Duration { return d }, sleep: waitSleep}

// TestWaitForConnection_GivingUp names the last refusal, and the longest
// streak when the machine answered at all. Only the messages are asserted;
// how long giving up took is not.
func TestWaitForConnection_GivingUp(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	req := connectionRequest{timeout: ms(60), sleep: ms(20), perTry: time.Second, steady: 1}
	_, err := waitForConnection(context.Background(), req, (&scripted{}).try, realPace)
	if err == nil || !strings.Contains(err.Error(), "the last try said: connection refused") {
		t.Errorf("never answered: %v", err)
	}

	req = connectionRequest{timeout: ms(200), sleep: ms(20), perTry: time.Second, steady: 3, steadyInterval: ms(20)}
	flapping := &scripted{script: []error{nil, nil, errRefused, nil}}
	_, err = waitForConnection(context.Background(), req, flapping.try, realPace)
	if err == nil || !strings.Contains(err.Error(), "answered 2 time(s) in a row at most, of the 3 asked for") ||
		!strings.Contains(err.Error(), "the last refusal said: connection refused") {
		t.Errorf("answered, never steadily: %v", err)
	}

	req = connectionRequest{timeout: ms(60), sleep: ms(20), perTry: time.Second, steady: 3, steadyInterval: ms(10)}
	slow := func(context.Context) error { time.Sleep(ms(40)); return nil }
	_, err = waitForConnection(context.Background(), req, slow, realPace)
	if err == nil || !strings.Contains(err.Error(), "no refusal") {
		t.Errorf("a streak the tries' own time pushed past the timeout: %v", err)
	}
}

// TestWaitForConnection_StopsWhenTheRunIsTornDown, between tries.
func TestWaitForConnection_StopsWhenTheRunIsTornDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForConnection(ctx, request(60, 0, 0, 1, false), (&scripted{}).try, realPace)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a torn-down run: %v", err)
	}
}

// TestConnectionJitter stays in the gap's upper half, and of nothing is
// nothing.
func TestConnectionJitter(t *testing.T) {
	for range 200 {
		if got := connectionJitter(time.Second); got < 500*time.Millisecond || got > time.Second {
			t.Fatalf("jitter of 1s: %s", got)
		}
	}
	if got := connectionJitter(0); got != 0 {
		t.Errorf("jitter of nothing: %s", got)
	}
}

// TestReadConnection_Backoff reads the new parameters and refuses one that
// would do nothing, or could not work, as the others are set.
func TestReadConnection_Backoff(t *testing.T) {
	r, err := readConnection(map[string]any{"backoff": "exponential", "sleep": 2, "max_sleep": 30, "steady": 3, "steady_interval": 5})
	if err != nil || !r.exponential || r.maxSleep != 30*time.Second || r.steady != 3 || r.steadyInterval != 5*time.Second {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := readConnection(map[string]any{"backoff": "fixed"}); err != nil || r.exponential || r.steady != 1 || r.maxSleep != 60*time.Second {
		t.Errorf("defaults: %+v %v", r, err)
	}
	for why, params := range map[string]map[string]any{
		"an unknown backoff":            {"backoff": "linear"},
		"a backoff not text":            {"backoff": 2},
		"max_sleep with fixed":          {"max_sleep": 30},
		"max_sleep below sleep":         {"backoff": "exponential", "sleep": 10, "max_sleep": 5},
		"no max_sleep":                  {"backoff": "exponential", "max_sleep": 0},
		"a steady of none":              {"steady": 0},
		"a steady not a number":         {"steady": "three"},
		"steady_interval with steady 1": {"steady_interval": 5},
		"no steady_interval":            {"steady": 2, "steady_interval": 0},
		"a streak past the timeout":     {"steady": 5, "steady_interval": 10, "timeout": 40},
		"a streak past the delay":       {"steady": 2, "steady_interval": 10, "timeout": 30, "delay": 20},
	} {
		if _, err := readConnection(params); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
}
