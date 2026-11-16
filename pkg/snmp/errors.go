// Package snmp: what a failed request means, typed where a caller decides
// on it (a refusal, a timeout, an agent's error status) and masked of the
// client's credential either way.
package snmp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// requestError is a failed request's error, typed where the caller decides
// on it and masked of the credential either way. A panic gosnmp recovered
// from carries every goroutine's stack in its text, so it is replaced with
// what it means.
func (c *Client) requestError(what string, err error) error {
	for sentinel, reason := range refusals {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%s: %w", what, &RefusedError{Reason: reason})
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", what, err)
	}
	if errors.Is(err, gosnmp.ErrMaxRetriesExceeded) {
		return fmt.Errorf("%s: %w", what, &TimeoutError{Tries: c.settings.Retries + 1, Each: c.settings.Timeout})
	}
	text := err.Error()
	for _, unauthentic := range unauthenticAnswers {
		if strings.Contains(text, unauthentic) {
			return fmt.Errorf("%s: %w", what, &RefusedError{Reason: unauthenticReason})
		}
	}
	if strings.HasPrefix(text, "recover:") {
		text = "the agent's answer could not be decoded"
	}
	return fmt.Errorf("%s: %s", what, c.mask(text))
}

// TimeoutError is a request no answer came back for, after every retry. An
// SNMPv1 or SNMPv2c agent that does not know the community answers nothing,
// so it reads as this too.
type TimeoutError struct {
	Tries int
	Each  time.Duration
}

// Error says how many times the request was sent and how long each waited.
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("no answer to %d tries of %s each (an agent drops a request whose community it does not know, so a wrong community reads this way too)", e.Tries, e.Each)
}

// refusals are gosnmp's USM report errors that asking again cannot change,
// and what each means.
var refusals = map[error]string{
	gosnmp.ErrUnknownUsername:      "it knows no such user (usmStatsUnknownUserNames)",
	gosnmp.ErrWrongDigest:          "the authentication passphrase or protocol is wrong (usmStatsWrongDigests)",
	gosnmp.ErrDecryption:           "it could not decrypt the request, so the privacy passphrase or protocol is wrong (usmStatsDecryptionErrors)",
	gosnmp.ErrUnknownSecurityLevel: "the user is not configured for this security level (usmStatsUnsupportedSecLevels)",
}

// unauthenticAnswers are gosnmp v1.47.0's errors for an answer this user's
// key does not authenticate. gosnmp gives them no sentinel, so they are read
// by text, against the version go.mod pins.
//
// It is how a real agent refuses: measured against net-snmp 5.9.4, a wrong
// authentication passphrase and a user the agent does not know are each
// answered with a usmStats report sent without authentication (RFC 3414
// section 3.2 has the agent report at noAuthNoPriv, since it could not
// authenticate the request), which gosnmp, holding the agent's engine ID by
// then, refuses to read rather than trust. So the counter the report names
// is not known, only that the agent answered and did not authenticate the
// answer, and gosnmp sends the request once per retry before giving up.
var unauthenticAnswers = []string{"incoming packet is not authentic", "is not authenticated but user"}

// unauthenticReason is what an unauthenticated answer means, in words.
const unauthenticReason = "it answered with a report this user's key does not authenticate, which is how an agent answers a wrong authentication passphrase or protocol, " +
	"or a user it does not know (net-snmp's usmStatsWrongDigests and usmStatsUnknownUserNames reports are sent unauthenticated)"

// mask is text with each of the client's secret values replaced, a short one
// only where it stands as its own word.
func (c *Client) mask(text string) string {
	return MaskValues(text, c.secrets)
}

// statusName is an error status's RFC 3416 name.
func statusName(e gosnmp.SNMPError) string {
	name := e.String()
	if name == "" || strings.HasPrefix(name, "SNMPError(") {
		return "error status " + strconv.Itoa(int(e))
	}
	// gosnmp names them in Go style (NotWritable); RFC 3416 writes notWritable.
	return strings.ToLower(name[:1]) + name[1:]
}
