// Package wait implements the "wait.path" and "wait.search" namespaced
// Collection methods: the two ways a runbook stops and watches a file on
// a device until something is true of it.
//
// Both are ansible.builtin.wait_for's file half, and they carry that
// module's parameter names (path, search_regex, timeout, delay, sleep,
// state) so a converted playbook renames nothing. wait_for's other half,
// the one that watches a TCP port, is a separate method
// (pleiades.builtin.wait.port) rather than a mode of these two, because a
// port and a path share no parameter beyond the timing ones and a single
// method covering both would spend most of its body deciding which of two
// unrelated things it was doing.
//
// # These methods change nothing, and everything else follows from that
//
// A wait observes. It reports Changed false on every run, it declares
// itself not reversible, and it never emits an inverse, because there is
// no state it moved that an undo could move back. What it still does is
// record a diff through sdk.Unchanged, whose two halves are the same
// observation: a journal has to be able to tell "this task looked at the
// device and found this" from "this task was never recorded", and an
// absent diff cannot say the first.
//
// # A timeout is a failure, not a result
//
// The condition never arriving is the whole reason someone wrote the
// task, so a run that waited and gave up returns an error rather than a
// Result a later task has to remember to inspect. That is Ansible's
// behavior too. It also means a timed-out run records nothing at all, the
// same rule the file namespace follows: the engine discards a Result that
// arrives with an error, so recording would leave a stat describing an
// observation the task never got to make.
//
// # Why polling, and why the context is checked at every step
//
// There is no remote watch primitive available here. The transport is an
// SSH exec channel (pkg/remoteexec), so "has it appeared yet" is a
// command sent, answered and sent again. Every sleep between those
// commands selects on the context as well as on a timer, so a run being
// torn down stops immediately instead of holding its device lock for the
// rest of a five minute timeout.
package wait

import (
	"fmt"
	"math"
	"time"
)

// Parameter names, which are ansible.builtin.wait_for's own names.
//
// They live in this shared file rather than in either method's file
// because both methods read all six of them, and two copies of a name is
// two places for one of them to drift. The wait prefix is what keeps
// them from colliding with anything a sibling method in this package
// declares: Go has no file-level scope, so every identifier here is
// visible to every other file in the package.
const (
	waitParamPath        = "path"
	waitParamSearchRegex = "search_regex"
	waitParamTimeout     = "timeout"
	waitParamDelay       = "delay"
	waitParamSleep       = "sleep"
	waitParamState       = "state"
)

// The values wait_for's state parameter accepts that mean something for
// a path.
//
// present and absent are the pair these methods document. started and
// stopped are accepted as well because they are what wait_for's own
// default is spelled as, and a playbook landing here unchanged is the
// point of the whole namespace: for a path, wait_for treats started as
// present and stopped as absent, so refusing them would break a
// migration over a synonym. drained is deliberately NOT accepted, since
// it asks about a socket's send queue and has no meaning for a file.
const (
	waitStatePresent = "present"
	waitStateAbsent  = "absent"
	waitStateStarted = "started"
	waitStateStopped = "stopped"
)

// waitStatElapsed is the stat both methods report how long they waited
// under, and it is wait_for's own return name for the same number.
//
// It is whole seconds rather than a duration string, again because that
// is what wait_for returns: a converted playbook whose later task
// compares elapsed against a number keeps working.
const waitStatElapsed = "elapsed"

// Defaults, which are wait_for's defaults.
//
// The long timeout is the one worth explaining: five minutes is what
// Ansible chose, and a wait exists precisely because the author does not
// know how long the thing takes, so a short default would turn a slow
// service start into a failed run rather than a slow one.
const (
	waitDefaultTimeout = 300 * time.Second
	waitDefaultDelay   = 0
	waitDefaultSleep   = 1 * time.Second
)

// waitRequest is everything both methods read from a task's params
// besides the condition each one watches for.
type waitRequest struct {
	// Path is the file on the device being watched.
	Path string

	// Present is what the task waits FOR: true for state present, false
	// for state absent. It is a bool rather than the state string because
	// every decision made from it is binary, and keeping the string would
	// mean re-comparing it at each of them.
	Present bool

	// Timeout is the whole budget, measured from the moment the method
	// starts polling rather than from the first probe, so Delay counts
	// against it. That is wait_for's own arithmetic (it computes the end
	// time before sleeping the delay), and matching it matters because a
	// converted playbook's delay plus timeout was tuned against that
	// behavior.
	Timeout time.Duration

	// Delay is how long to wait before the first probe. It exists for the
	// case where the condition is briefly true for the wrong reason: a
	// log file that still holds the previous run's success line, say,
	// where probing immediately would match the old content.
	Delay time.Duration

	// Sleep is how long to wait between probes.
	Sleep time.Duration
}

// waitReadRequest reads and checks the timing and target parameters both
// methods share.
//
// It runs before either method connects, deliberately. Every refusal
// below is a mistake in the runbook rather than a condition on the
// device, and one that costs a TCP connect, a key exchange and an
// authentication round before reporting itself is a slower answer to the
// same question.
func waitReadRequest(params map[string]any) (waitRequest, error) {
	var req waitRequest

	path, err := waitTextParam(params, waitParamPath)
	if err != nil {
		return req, err
	}
	if path == "" {
		return req, fmt.Errorf("%s is required", waitParamPath)
	}
	req.Path = path

	if req.Present, err = waitWantPresent(params); err != nil {
		return req, err
	}
	if req.Timeout, err = waitSeconds(params, waitParamTimeout, waitDefaultTimeout); err != nil {
		return req, err
	}
	if req.Delay, err = waitSeconds(params, waitParamDelay, waitDefaultDelay); err != nil {
		return req, err
	}
	if req.Sleep, err = waitSeconds(params, waitParamSleep, waitDefaultSleep); err != nil {
		return req, err
	}

	// A timeout of zero is refused rather than read as "check once". A
	// wait with no time to wait in is almost always a variable that
	// resolved to nothing, and honoring it would turn that mistake into a
	// task that fails against a device which was merely a second slow.
	if req.Timeout <= 0 {
		return req, fmt.Errorf("%s must be more than 0 seconds: a wait with no time to wait in cannot do what the task asks", waitParamTimeout)
	}
	// A sleep of zero would send probes as fast as the network answers
	// them, which is a denial of service against the device this task is
	// supposed to be waiting politely for.
	if req.Sleep <= 0 {
		return req, fmt.Errorf("%s must be more than 0 seconds: a sleep of zero would probe the device as fast as it can answer", waitParamSleep)
	}
	// The delay is spent out of the same budget, so a delay that reaches
	// the deadline leaves room for exactly one probe and then a failure.
	// wait_for allows it and produces that useless run; refusing says
	// which of the two numbers is wrong while the author is still looking
	// at them.
	if req.Delay >= req.Timeout {
		return req, fmt.Errorf("%s (%s) must be shorter than %s (%s): the delay is spent out of the timeout, so this leaves no time to poll in",
			waitParamDelay, req.Delay, waitParamTimeout, req.Timeout)
	}

	return req, nil
}

// waitTextParam reads one string parameter, refusing a value that
// arrived as something other than text.
//
// sdk.StringParam is the usual reader and it treats a non-string as
// absent, which is the wrong answer here: a path written as a bare
// number in YAML would be reported as "path is required", sending the
// author hunting for a parameter they did write.
func waitTextParam(params map[string]any, key string) (string, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s is %T, not text: quote it in the runbook", key, raw)
	}
	return text, nil
}

// waitWantPresent turns the state parameter into the one question the
// rest of the method asks: is this task waiting for the condition to
// hold, or for it to stop holding?
func waitWantPresent(params map[string]any) (bool, error) {
	state, err := waitTextParam(params, waitParamState)
	if err != nil {
		return false, err
	}

	switch state {
	case "", waitStatePresent, waitStateStarted:
		// Absent means present, which is wait_for's default too (spelled
		// started there, since its default has to make sense for a port).
		return true, nil
	case waitStateAbsent, waitStateStopped:
		return false, nil
	default:
		return false, fmt.Errorf("%s %q is not one of %s, %s, %s or %s: drained asks about a socket's send queue and means nothing for a file",
			waitParamState, state, waitStatePresent, waitStateAbsent, waitStateStarted, waitStateStopped)
	}
}

// waitSeconds reads a whole number of seconds from a task parameter,
// returning fallback when the key is absent.
//
// It accepts three Go types for one YAML number, and that is not
// defensive coding. On the Walk tier a runbook's `timeout: 30` decodes
// to an int, and on the Crawl tier the same task crosses the Runner's
// per-task subprocess boundary as JSON, where every number decodes to a
// float64. A reader that took only int would silently fall back to the
// default on one tier and honor the task on the other, which is the
// worst failure shape available: the same runbook behaving differently
// depending on which tier ran it, with nothing reported either way.
func waitSeconds(params map[string]any, key string, fallback time.Duration) (time.Duration, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return fallback, nil
	}

	var seconds float64
	switch v := raw.(type) {
	case int:
		seconds = float64(v)
	case int64:
		// A YAML number too large for an int on a 32-bit build arrives
		// this way rather than as an int.
		seconds = float64(v)
	case float64:
		seconds = v
	default:
		return 0, fmt.Errorf("%s is %T, not a number of seconds: write it unquoted, as %s: 30", key, raw, key)
	}

	// A fraction is refused rather than truncated or rounded. wait_for's
	// timing parameters are whole seconds, and silently turning 0.5 into
	// 0 would produce a sleep of zero, which is the one value the check
	// in waitReadRequest exists to prevent.
	if seconds != math.Trunc(seconds) {
		return 0, fmt.Errorf("%s %v is not a whole number of seconds", key, seconds)
	}
	if seconds < 0 {
		return 0, fmt.Errorf("%s %v is negative: seconds cannot run backwards", key, seconds)
	}
	return time.Duration(seconds) * time.Second, nil
}
