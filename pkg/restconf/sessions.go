// Package restconf: waiting out a server's cap on RESTCONF sessions.
//
// IOS XE counts every RESTCONF request as a session of its own, whatever
// connection carries it, and keeps each for a while after its answer. Measured
// on a Catalyst 8000V (IOS XE 17.15, 2026-11-17): one keep-alive connection
// carried a whole discovery and a twelve-source fetch, yet the 101st source of
// a fetch of every module was answered 409 Conflict, error-tag in-use, "Too
// many sessions.", the same source on two tries eleven minutes apart; a run
// started at once after was refused at its first read, from new connections.
// The device's "show ip http server status" names a session idle time-out of
// 600 seconds. So no client can stay under the cap by reusing its connection
// alone (this one already does: one http.Client and one keep-alive transport
// for the Client's life); it can only wait for the device's older sessions
// to expire. With this pacing the same fetch of 676 sources completed on that
// device in 486 seconds, 44 refusals waited out.
//
// That answer, and only that answer (409, in-use, a message naming too many
// sessions), is read as the device asking the client to slow down: the same
// request is sent again after a wait that starts at Pacing.First and doubles
// up to Pacing.Max, until Pacing.PerRequest has passed for that one request,
// Pacing.PerTask for every request of the Client together, or the caller's
// context ends. The worst case is therefore one task waiting PerTask in all
// (15 minutes by default) beside its requests' own time.
//
// IOS XE's answer suggests it opened no session for the refused request and
// so applied nothing; that was measured for reads, never for writes. A write
// is sent again as a read is, and its answer says how many times it was
// (StatusError.Resent, Written.Resent), so a caller can tell an answer a first
// try having applied the write would also explain (a post answered
// data-exists, a delete 404) from a plain one. Any other 409 (data-exists,
// resource-denied, a lock held: in-use with another message) is returned at
// once, as is every other status; a refused credential is never sent again
// (do's rule), so a wait never follows one.
package restconf

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Pacing is how a client waits out a server's session cap.
type Pacing struct {
	// First is the first wait.
	First time.Duration
	// Max is the longest one wait grows to, so a session that expires is
	// taken within this long of its expiry.
	Max time.Duration
	// PerRequest is the most one request waits in all.
	PerRequest time.Duration
	// PerTask is the most one Client (one task's) waits in all, across every
	// request it sends.
	PerTask time.Duration
}

// defaultPacing is the pacing a context carrying none gets. PerRequest is
// past IOS XE's 600 second session idle time-out, so a request has waited out
// every session that held the cap before it gives up; PerTask bounds a whole
// task. It is a variable only so this package's tests can shorten it.
var defaultPacing = Pacing{First: 2 * time.Second, Max: 30 * time.Second, PerRequest: 11 * time.Minute, PerTask: 15 * time.Minute}

// pacingKey is the context key WithPacing stores a Pacing under.
type pacingKey struct{}

// WithPacing returns ctx carrying p, which every request a client sends under
// it is paced by, each field left zero taking its default (First 2 seconds,
// Max 30 seconds, PerRequest 11 minutes, PerTask 15 minutes). It is for a
// caller that must wait differently, a test above all.
func WithPacing(ctx context.Context, p Pacing) context.Context {
	return context.WithValue(ctx, pacingKey{}, p)
}

// pacingOf is the pacing ctx carries, each zero field its default.
func pacingOf(ctx context.Context) Pacing {
	p, _ := ctx.Value(pacingKey{}).(Pacing)
	if p.First <= 0 {
		p.First = defaultPacing.First
	}
	if p.Max <= 0 {
		p.Max = defaultPacing.Max
	}
	if p.PerRequest <= 0 {
		p.PerRequest = defaultPacing.PerRequest
	}
	if p.PerTask <= 0 {
		p.PerTask = defaultPacing.PerTask
	}
	return p
}

// tagInUse is RFC 8040 section 7's error-tag for a resource in use, which IOS
// XE names its session cap with.
const tagInUse = "in-use"

// The limits a wait can reach (SessionsExhaustedError.Limit).
const (
	// LimitPerRequest is Pacing.PerRequest.
	LimitPerRequest = "per request"
	// LimitPerTask is Pacing.PerTask.
	LimitPerTask = "per task"
)

// SessionsExhaustedError is a request the server kept refusing for too many
// sessions until the wait for it ended: a limit of the pacing was reached, or
// the caller's context ended. IOS XE's answer suggests the request was not
// applied (it had no session), which was measured for reads alone.
type SessionsExhaustedError struct {
	// Answer is the server's last refusal, masked.
	Answer *StatusError
	// Refusals is how many times this request was refused.
	Refusals int
	// Waited is how long this request waited in all.
	Waited time.Duration
	// Limit is the limit reached (LimitPerRequest or LimitPerTask), and
	// Bound its length; Limit is empty when the context ended the wait.
	Limit string
	Bound time.Duration
	// Cause is the context's error when the caller's context ended the wait,
	// and Within names whose time that was (Client.WaitsWithin).
	Cause  error
	Within string
}

// Error says what the device refused, how long the client waited and which
// limit or time stopped it.
func (e *SessionsExhaustedError) Error() string {
	why := fmt.Sprintf("reaching this client's limit of %s of waiting %s", e.Bound, e.Limit)
	if e.Limit == "" {
		why = "when " + e.Within + " ended (" + e.Cause.Error() + ")"
	}
	return fmt.Sprintf("%s: the device has too many RESTCONF sessions open (its cap counts each request as a session until it expires), "+
		"and refused this request %d times over %s, %s; its answer suggests the request was not applied, so run the task again once the device's sessions have expired "+
		"(NETCONF, where the device serves it, uses one session for a whole task)", e.Answer.Error(), e.Refusals, e.Waited.Round(time.Millisecond), why)
}

// Unwrap returns the server's last refusal, a *StatusError.
func (e *SessionsExhaustedError) Unwrap() error { return e.Answer }

// WaitsWithin names whose time a wait for sessions runs within, which the
// error says when that time ends the wait: "the task's time" unless the
// caller says otherwise (a try of wait.restconf, onboarding).
func (c *Client) WaitsWithin(name string) {
	c.within = name
}

// tooManySessions reports whether a is the server refusing to open one more
// session (the package's rule): 409, error-tag in-use, and a message naming
// too many sessions. The message is read masked, as every error is.
func (c *Client) tooManySessions(a answer) bool {
	if a.status != http.StatusConflict {
		return false
	}
	for _, x := range c.statusError(a).Errors {
		if x.Tag == tagInUse && strings.Contains(strings.ToLower(x.Message), "too many sessions") {
			return true
		}
	}
	return false
}

// afterSessionRefusal, when set, runs after each refusal is counted and
// before its wait. Only this package's tests set it, so a test ends the
// caller's context at a known point inside the wait instead of racing a
// deadline against a request in flight; it is nil in every other build.
var afterSessionRefusal func(refusals int)

// paced sends one request through once, sending it again on ctx's pacing
// while the server refuses it for too many sessions, and returns the answer
// that ended it, counting on it how many times it was sent again. Every wait
// is counted on the client, which SessionWarnings reports, and the client's
// waits together never pass the pacing's PerTask.
func (c *Client) paced(ctx context.Context, once func() (answer, error)) (answer, error) {
	p := pacingOf(ctx)
	c.taskBound = p.PerTask
	delay := p.First
	var waited time.Duration
	refusals := 0
	for {
		a, err := once()
		if err != nil || !c.tooManySessions(a) {
			a.resent = refusals
			return a, err
		}
		refusals++
		c.sessionRefusals++
		if afterSessionRefusal != nil {
			afterSessionRefusal(refusals)
		}
		exhausted := func(limit string, bound time.Duration, cause error) error {
			c.sessionGaveUp++
			e := &SessionsExhaustedError{Answer: c.statusError(a), Refusals: refusals, Waited: waited, Limit: limit, Bound: bound, Cause: cause, Within: c.within}
			if e.Within == "" {
				e.Within = "the task's time"
			}
			return e
		}
		// The wait is cut to what the limits leave, and a request with
		// nothing left is the caller's.
		wait, limit, bound := delay, LimitPerRequest, p.PerRequest
		left := p.PerRequest - waited
		if task := p.PerTask - c.sessionWaited; task < left {
			left, limit, bound = task, LimitPerTask, p.PerTask
		}
		if left <= 0 {
			return answer{}, exhausted(limit, bound, nil)
		}
		wait = min(wait, left)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return answer{}, exhausted("", 0, ctx.Err())
		case <-timer.C:
		}
		waited += wait
		c.sessionWaited += wait
		delay = min(delay*2, p.Max)
	}
}

// SessionWarnings is what a task says when the server refused any of this
// client's requests for too many sessions: how many refusals, how long the
// client waited in all for the device's sessions to expire (and the most a
// task waits), and whether it gave up on any request. None when it never
// waited.
func (c *Client) SessionWarnings(device string) []string {
	if c.sessionRefusals == 0 {
		return nil
	}
	sent := ", sending each refused request again until it was answered"
	if c.sessionGaveUp > 0 {
		sent = fmt.Sprintf(", sending each refused request again, and gave up on %d of them", c.sessionGaveUp)
	}
	return []string{fmt.Sprintf("device %q answered RESTCONF requests %d times with 409 in-use, too many sessions (its cap counts each request as a session until it expires), "+
		"so the task waited %s in all (at most %s per task) for its sessions to expire%s; NETCONF, where the device serves it, uses one session for a whole task",
		device, c.sessionRefusals, c.sessionWaited.Round(time.Millisecond), c.taskBound, sent)}
}
