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
// to expire.
//
// That answer, and only that answer (409, in-use, a message naming too many
// sessions), is read as the device asking the client to slow down: the same
// request is sent again after a wait that starts at sessionRetryFirst and
// doubles up to sessionRetryMax, until sessionWaitBound has passed for that
// one request, longer than the device's own session time-out, or the caller's
// context ends. The device opened no session for the refused request, so it
// applied nothing, and a write is sent again as a read is. Any other 409
// (data-exists, resource-denied, a lock held: in-use with another message) is
// returned at once, as is every other status; a refused credential is never
// sent again (do's rule), so a wait never follows one.
package restconf

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The pacing of a request the server refused for too many sessions. They are
// variables only so a test can prove the schedule quickly; nothing else sets
// them.
var (
	// sessionRetryFirst is the first wait.
	sessionRetryFirst = 2 * time.Second
	// sessionRetryMax is the longest one wait grows to, so a session that
	// expires is taken within this long of its expiry.
	sessionRetryMax = 30 * time.Second
	// sessionWaitBound is how long one request waits in all before its
	// refusal is the caller's: past IOS XE's 600 second session idle
	// time-out, so every session that held the cap has expired by then.
	sessionWaitBound = 15 * time.Minute
)

// tagInUse is RFC 8040 section 7's error-tag for a resource in use, which IOS
// XE names its session cap with.
const tagInUse = "in-use"

// SessionsExhaustedError is a request the server kept refusing for too many
// sessions until the wait for it ended: its bound passed, or the caller's
// context ended. The device applied nothing: it opened no session for it.
type SessionsExhaustedError struct {
	// Answer is the server's last refusal, masked.
	Answer *StatusError
	// Refusals is how many times this request was refused.
	Refusals int
	// Waited is how long this request waited in all.
	Waited time.Duration
	// Cause is the context's error when the caller's context ended the wait,
	// nil when the bound did.
	Cause error
}

// Error says what the device refused, how long the client waited and why it
// stopped.
func (e *SessionsExhaustedError) Error() string {
	why := fmt.Sprintf("which is this client's bound of %s for one request", sessionWaitBound)
	if e.Cause != nil {
		why = "when the task's own time ended (" + e.Cause.Error() + ")"
	}
	return fmt.Sprintf("%s: the device has too many RESTCONF sessions open (its cap counts each request as a session until it expires), "+
		"and refused this request %d times over %s, %s; nothing was applied, so run the task again once the device's sessions have expired "+
		"(NETCONF, where the device serves it, uses one session for a whole task)", e.Answer.Error(), e.Refusals, e.Waited.Round(time.Second), why)
}

// Unwrap returns the server's last refusal, a *StatusError.
func (e *SessionsExhaustedError) Unwrap() error { return e.Answer }

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

// paced sends one request through once, sending it again on the package's
// schedule while the server refuses it for too many sessions. Every wait is
// counted on the client, which SessionWarnings reports.
func (c *Client) paced(ctx context.Context, once func() (answer, error)) (answer, error) {
	delay := sessionRetryFirst
	var waited time.Duration
	refusals := 0
	for {
		a, err := once()
		if err != nil || !c.tooManySessions(a) {
			return a, err
		}
		refusals++
		c.sessionRefusals++
		if waited+delay > sessionWaitBound {
			return answer{}, &SessionsExhaustedError{Answer: c.statusError(a), Refusals: refusals, Waited: waited}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return answer{}, &SessionsExhaustedError{Answer: c.statusError(a), Refusals: refusals, Waited: waited, Cause: ctx.Err()}
		case <-timer.C:
		}
		waited += delay
		c.sessionWaited += delay
		delay = min(delay*2, sessionRetryMax)
	}
}

// SessionWarnings is what a task says when the server refused any of this
// client's requests for too many sessions: how many refusals, and how long
// the client waited for the device's sessions to expire. None when it never
// did.
func (c *Client) SessionWarnings(device string) []string {
	if c.sessionRefusals == 0 {
		return nil
	}
	return []string{fmt.Sprintf("device %q refused %d RESTCONF requests with 409 in-use, too many sessions (its cap counts each request as a session until it expires), "+
		"so the task waited %s in all for its sessions to expire and sent each again; NETCONF, where the device serves it, uses one session for a whole task", device, c.sessionRefusals, c.sessionWaited.Round(time.Second))}
}
