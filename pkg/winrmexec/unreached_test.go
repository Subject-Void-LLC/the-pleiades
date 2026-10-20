// Tests for NotStartedError.Unreached: which failures before the command
// was sent are the device never answering, and which are it answering
// with a refusal.
package winrmexec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

// timeoutError is a net.Error that timed out, as http.Transport's
// "timeout awaiting response headers" is.
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout awaiting response headers" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestNotStartedError_Unreached holds each failure to the rule.
func TestNotStartedError_Unreached(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"a refused connection": {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		"a handshake that timed out": {fmt.Errorf("winrm: NTLM handshake: %w; nothing of the message was sent",
			&url.Error{Op: "Post", URL: "http://h:5985/wsman", Err: timeoutError{}}), true},
		"the bound passing before a shell": {fmt.Errorf("%w: no shell opened on h within 5s", context.DeadlineExceeded), true},
		"a TLS alert the host sent":        {&url.Error{Op: "Post", URL: "https://h:5986/wsman", Err: &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}}, false},
		"a refused login":                  {fmt.Errorf("winrm: %w; nothing of the message was sent", errLoginRefused), false},
		"a handshake answered with 503":    {errors.New("winrm: the NTLM handshake with h ended with HTTP 503 and no sealed session"), false},
		"the caller canceling":             {fmt.Errorf("%w: no shell opened", context.Canceled), false},
	} {
		if got := (&NotStartedError{Err: tc.err}).Unreached(); got != tc.want {
			t.Errorf("%s: Unreached() = %v, want %v", name, got, tc.want)
		}
	}
}
