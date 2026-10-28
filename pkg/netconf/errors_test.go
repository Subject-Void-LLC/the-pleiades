// Tests for RPCError's rendering of a device's own error-message: the
// device's words reach the error, but bounded and quoted through
// pkg/farside, so a hostile or broken device cannot fill an error or
// drive a terminal with it.
package netconf

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// TestRPCError_BoundsAndQuotesTheDevicesMessage feeds RPCError a message
// far longer than farside.MaxBytes and laced with control characters (an
// escape sequence that would recolor a terminal, a carriage return that
// would overwrite the line, a bell and a NUL), and requires the rendered
// error to keep the device's leading words, show every control character
// as an escape rather than the byte itself, stay one line, and stay
// within the bound.
func TestRPCError_BoundsAndQuotesTheDevicesMessage(t *testing.T) {
	// The device's words come first, then the hostile bytes, then enough
	// filler to pass the bound many times over.
	msg := "Unsupported capability :candidate \x1b[31mred\x1b[0m\rover\x07\x00" + strings.Repeat("x", 5000)
	e := RPCError{Type: "protocol", Tag: "invalid-value", Message: msg, BadElement: "candidate"}

	got := e.Error()

	// The device's own words must survive: they are the reason an
	// operator can act on.
	if !strings.Contains(got, "Unsupported capability :candidate") {
		t.Errorf("Error() = %q, want the device's own words", got)
	}
	// No raw control byte may reach the error.
	for _, c := range got {
		if c < 0x20 || c == 0x7f {
			t.Fatalf("Error() = %q holds raw control character %U, want it quoted", got, c)
		}
	}
	// The escape must be shown, so a reader can tell the device sent it.
	if !strings.Contains(got, `\x1b`) {
		t.Errorf("Error() = %q, want the escape shown as \\x1b", got)
	}
	// The message is cut and marked as cut, not carried whole.
	if !strings.Contains(got, `..."`) {
		t.Errorf("Error() = %q, want the message cut and marked with ...", got)
	}
	// The whole message part fits farside's bound plus its cut marker and
	// quotes, and the rest of the error is the fixed prefix and details.
	quoted := farside.Text(msg)
	if len(quoted) > farside.MaxBytes+5 {
		t.Errorf("farside.Text(msg) is %d bytes, want at most %d", len(quoted), farside.MaxBytes+5)
	}
	if !strings.Contains(got, ": "+quoted) {
		t.Errorf("Error() = %q, want it to carry the message as farside.Text renders it, %q", got, quoted)
	}
	if len(got) > len(quoted)+200 {
		t.Errorf("Error() is %d bytes, want the message bounded (the message part is %d)", len(got), len(quoted))
	}
	// The other fields still follow the message.
	if !strings.Contains(got, "bad-element=candidate") || !strings.Contains(got, "type=protocol") {
		t.Errorf("Error() = %q, want the type and bad-element details kept", got)
	}
}

// TestRPCError_ControlWithoutAMessage is the control for the test above:
// an error with no message renders no quoted part at all, so the quoting
// seen there comes from the message and not from the rest of Error.
func TestRPCError_ControlWithoutAMessage(t *testing.T) {
	got := RPCError{Tag: "unknown-element", Path: "/rpc"}.Error()
	want := "netconf: rpc-error: unknown-element (path=/rpc)"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
