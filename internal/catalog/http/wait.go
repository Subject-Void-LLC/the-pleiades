// http.wait: wait, from the machine pleiades runs on, until a URL answers
// as http.check would accept, repeating http.check's own request.
package http

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnWait = "http.wait"

// The schedule parameters, the wait methods' own names, and their bounds.
const (
	waitParamDelay = "delay"
	waitParamSleep = "sleep"

	waitDefaultTimeout = 300 * time.Second
	waitDefaultSleep   = time.Second
	waitMostSeconds    = 86400
	// waitAttemptBound bounds each request, so a server that accepts and
	// never answers is asked again rather than holding the whole wait.
	waitAttemptBound = 10 * time.Second
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnWait,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It sends a GET or a HEAD, which HTTP defines as safe, and changes nothing a later task could undo.",
			},
			NoCheckReason: "what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: waitDoc(),
		},
		Invoke: Wait,
	})
}

// waitDoc is http.wait's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func waitDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Waits, from the machine pleiades runs on, until a URL answers with the expected status, and optionally body.",
		Description: "Repeats http.check's request and its tests until they pass: the status is one of status_code, and the body contains body_contains or matches body_matches. A refused connection, a timeout or an answer that does not pass all mean not yet. It runs once with no target device, from the machine pleiades runs on, so it waits for a service published there (a container's port on 127.0.0.1) that wait.port, which looks from a device over SSH, cannot see. Each attempt is bounded by 10 seconds or by what is left of the timeout, and running out of time fails the task naming the last answer, or the last error. What it returns is http.check's, never the body: the last answer's status and final URL, how many attempts were made and how long the wait took. A URL carrying a user name or password is refused, a redirect from https to http is not followed, and certificates are verified unless validate_certs is false.",
		Params: []collection.Param{
			{Name: requestParamURL, Type: "string", Required: true, Description: "The http or https URL to request."},
			{Name: requestParamStatusCode, Type: "int or list of int", Default: "200", Description: "The status, or statuses, that pass."},
			{Name: checkParamBodyContains, Type: "string", Description: "Text the body must contain."},
			{Name: checkParamBodyMatches, Type: "string", Description: "A regular expression (RE2, at most 1024 bytes) the body must match. One of body_contains and body_matches."},
			{Name: requestParamMethod, Type: "string", Default: "GET", Choices: []string{"GET", "HEAD"}, Description: "GET, or HEAD for the status alone."},
			{Name: checkParamMaxRedirects, Type: "int", Default: "5", Description: "How many redirects to follow, 0 to 10."},
			{Name: requestParamValidateCerts, Type: "bool", Default: "true", Description: "Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it."},
			{Name: requestParamTimeout, Type: "int", Default: "300", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0, and at most 86400."},
			{Name: waitParamDelay, Type: "int", Default: "0", Description: "How many seconds to wait before the first request. Must be shorter than the timeout, which it is spent out of."},
			{Name: waitParamSleep, Type: "int", Default: "1", Description: "How many seconds to wait between requests. Must be more than 0."},
		},
		Returns: []collection.ReturnField{
			{Name: "status", Type: "int", Returned: "when the server answered", Description: "The last answer's status."},
			{Name: "final_url", Type: "string", Returned: "when the server answered", Description: "The URL that answered last, without query or fragment."},
			{Name: "attempts", Type: "int", Returned: "always", Description: "How many requests were made."},
			{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
		},
		Examples: []collection.Example{
			{Name: "Wait for a service this machine runs to answer", RunbookYAML: "- name: Wait for Batfish's API\n  http.wait:\n    url: http://127.0.0.1:9996/v2/version\n    timeout: 180\n    sleep: 3\n"},
		},
		SeeAlso: []string{"http.check", "pleiades.builtin.wait.port"},
	}
}

// waitSchedule is a wait's timeout, first delay and sleep between attempts.
type waitSchedule struct {
	timeout, delay, sleep time.Duration
}

// waitSeconds reads one schedule parameter in whole seconds: fallback when
// absent, refused outside 0 to waitMostSeconds, and refused at zero unless
// zeroOK.
func waitSeconds(params map[string]any, key string, fallback time.Duration, zeroOK bool) (time.Duration, error) {
	n, set, err := sdk.IntParam(params, key)
	switch {
	case err != nil:
		return 0, err
	case !set:
		return fallback, nil
	case n < 0 || n > waitMostSeconds || (n == 0 && !zeroOK):
		low := 1
		if zeroOK {
			low = 0
		}
		return 0, fmt.Errorf("%s is %d; it must be from %d to %d seconds", key, n, low, waitMostSeconds)
	}
	return time.Duration(n) * time.Second, nil
}

// waitParse reads http.wait's call: http.check's request and tests, and the
// wait's own schedule, whose timeout is the whole wait rather than one
// request's.
func waitParse(params map[string]any) (checkSpec, waitSchedule, error) {
	var sched waitSchedule
	var err error
	if sched.timeout, err = waitSeconds(params, requestParamTimeout, waitDefaultTimeout, false); err != nil {
		return checkSpec{}, sched, err
	}
	if sched.delay, err = waitSeconds(params, waitParamDelay, 0, true); err != nil {
		return checkSpec{}, sched, err
	}
	if sched.sleep, err = waitSeconds(params, waitParamSleep, waitDefaultSleep, false); err != nil {
		return checkSpec{}, sched, err
	}
	if sched.delay >= sched.timeout {
		return checkSpec{}, sched, fmt.Errorf("%s (%s) must be shorter than %s (%s), which it is spent out of", waitParamDelay, sched.delay, requestParamTimeout, sched.timeout)
	}
	request := maps.Clone(params)
	delete(request, requestParamTimeout)
	delete(request, waitParamDelay)
	delete(request, waitParamSleep)
	spec, err := checkParse(request)
	return spec, sched, err
}

// Wait implements http.wait.
func Wait(ctx context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnWait, err)
	}
	spec, sched, err := waitParse(params)
	if err != nil {
		return fail(err)
	}
	start := time.Now()
	deadline := start.Add(sched.timeout)
	attempts := 0
	var last error
	var stats map[string]any
	pause := sched.delay
	for {
		if pause > 0 {
			if err := sleepFor(ctx, pause); err != nil {
				return fail(fmt.Errorf("waiting for %s: %w", shown(spec.url), err))
			}
		}
		attempt := spec
		attempt.timeout = min(waitAttemptBound, max(time.Until(deadline), time.Second))
		attempts++
		var answer map[string]any
		answer, last = checkOnce(ctx, attempt)
		if answer != nil {
			stats = answer
		}
		if last == nil || ctx.Err() != nil {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		pause = min(sched.sleep, remaining)
	}
	for k, v := range map[string]any{"attempts": attempts, "elapsed": int(time.Since(start).Seconds())} {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	for _, k := range []string{"status", "final_url"} {
		if v, ok := stats[k]; ok {
			if err := rc.SetStat(k, v); err != nil {
				return fail(err)
			}
		}
	}
	switch {
	case last == nil:
		return collection.Result{}, nil
	case ctx.Err() != nil:
		return fail(fmt.Errorf("waiting for %s: %w", shown(spec.url), ctx.Err()))
	}
	return fail(fmt.Errorf("timed out after %s and %d attempts waiting for %s: %w", sched.timeout, attempts, shown(spec.url), last))
}

// sleepFor waits for d, or until ctx ends.
func sleepFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
