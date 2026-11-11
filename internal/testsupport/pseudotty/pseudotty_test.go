// Tests that Open hands out a working pseudo terminal, that it survives the
// code under test closing the device, that cleanup wakes a blocked reader,
// and that a machine without one is a named skip.
package pseudotty

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// readWithin reads once from far in a goroutine and returns what it read,
// failing t when nothing arrives within wait. A master side has no portable
// read deadline, so the bound is a select rather than SetReadDeadline.
func readWithin(t *testing.T, far *os.File, wait time.Duration) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := far.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(wait):
		t.Fatalf("nothing reached the master side within %s", wait)
		return ""
	}
}

// writeThroughDevice opens device by its path, as code under test would,
// writes text and closes it again.
func writeThroughDevice(t *testing.T, device, text string) {
	t.Helper()
	f, err := os.OpenFile(device, os.O_RDWR, 0) // #nosec G304 -- the test's own pseudo terminal
	if err != nil {
		t.Fatalf("opening %s: %v", device, err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("writing %s: %v", device, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", device, err)
	}
}

// TestOpen_TheDeviceReachesTheMaster proves the device path is the slave of
// far: bytes written through a fresh open of the path arrive on far, twice
// in a row with the device closed between, which holds only because Open
// keeps its own slave descriptor.
func TestOpen_TheDeviceReachesTheMaster(t *testing.T) {
	device, far := Open(t)
	if device == "" || far == nil {
		t.Fatalf("Open returned device %q and far %v", device, far)
	}
	for _, text := range []string{"first", "second"} {
		writeThroughDevice(t, device, text)
		if got := readWithin(t, far, 2*time.Second); got != text {
			t.Errorf("far read %q, want %q", got, text)
		}
	}
}

// TestOpen_CleanupWakesABlockedReader proves the cleanup order: a read of
// the master that nothing will ever answer returns once the test that opened
// the terminal ends, rather than leaking a goroutine blocked forever.
func TestOpen_CleanupWakesABlockedReader(t *testing.T) {
	done := make(chan struct{})
	t.Run("holder", func(t *testing.T) {
		_, far := Open(t)
		go func() {
			defer close(done)
			_, _ = far.Read(make([]byte, 16))
		}()
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a read of the master was still blocked after the terminal's cleanup ran")
	}
}

// TestOpen_NoPseudoTerminalIsANamedSkip proves a machine that cannot give a
// pseudo terminal skips the test under Need, with the reason, rather than
// failing it or carrying on with an empty device.
func TestOpen_NoPseudoTerminalIsANamedSkip(t *testing.T) {
	// Not required here, so the failure is a skip whatever this run requires.
	t.Setenv(need.Env, "")
	saved := openPTY
	t.Cleanup(func() { openPTY = saved })
	openPTY = func() (*os.File, *os.File, error) { return nil, nil, errors.New("no ptmx here") }

	var device string
	ran := t.Run("without", func(t *testing.T) {
		device, _ = Open(t)
		t.Error("Open returned on a machine without a pseudo terminal")
	})
	if !ran {
		t.Fatal("the subtest failed rather than skipped")
	}
	if device != "" {
		t.Errorf("device = %q, want empty", device)
	}
}
