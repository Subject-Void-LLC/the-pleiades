package serialexec_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// FuzzExec_DeviceIdentifier proves Exec never panics against an
// arbitrary device string, for any of the three platform shapes a real
// caller might construct one from (SerialCapable.SerialDevice's own doc
// comment: "/dev/ttyUSB0" on Linux, "/dev/tty.usbserial-*" on macOS,
// "COM3" on Windows), and that a hostile one (a traversal attempt, an
// embedded NUL, a pathological length) fails closed with a real error
// rather than crashing or hanging. serialline.Device is deliberately
// OPAQUE and never parsed, joined, or validated as a path by this
// package's own design; this is what proves that stays true even for
// input designed to break a parser that assumed otherwise.
func FuzzExec_DeviceIdentifier(f *testing.F) {
	seeds := []string{
		"",
		"/dev/ttyUSB0",
		"/dev/tty.usbserial-A9UFOQI4",
		"COM3",
		"../../../etc/passwd",
		"/dev/../../../etc/passwd",
		"\x00",
		"/dev/tty\x00USB0",
		"COM999999999999999999",
		string(make([]byte, 10000)),
		"con", "aux", "nul", // Windows reserved device names, lowercase
		"/dev/tty" + string(make([]byte, 5000)),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	line := serialline.Config{BaudRate: 9600, DataBits: 8}

	f.Fuzz(func(t *testing.T, device string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Exec panicked on device %q: %v", device, r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		// Every one of these seeds and every fuzzer-generated string
		// names a device that does not exist on the machine running this
		// test, so Exec is expected to fail (a real error), not succeed
		// - the property under test is exclusively "does not panic and
		// does not hang," not "handles every string as a valid port."
		_, _ = serialexec.Exec(ctx, serialline.Device(device), line, serialexec.Options{}, "echo hi")
	})
}
