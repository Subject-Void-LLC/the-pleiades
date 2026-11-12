// Tests that every function here refuses a service name the cmdlets would
// read as a wildcard pattern, or that begins with "-", before any script is
// sent (FAILURE_PATTERNS 1174). The session names a port nothing listens
// on, so a function that got past the name would fail on the dial instead,
// with a different error.
package winrmsvc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// TestFunctions_RefuseANameTheCmdletsWouldMisread walks Status and every
// verb over the names refused, and checks a real name still reaches the
// dial.
func TestFunctions_RefuseANameTheCmdletsWouldMisread(t *testing.T) {
	session := Session{
		Target:  winrmexec.Target{Host: "127.0.0.1", Port: 1},
		Options: winrmexec.Options{Timeout: time.Second},
	}
	calls := map[string]func(context.Context, Session, string) error{
		"status":  func(ctx context.Context, s Session, n string) error { _, err := Status(ctx, s, n); return err },
		"start":   Start,
		"stop":    Stop,
		"restart": Restart,
		"enable":  Enable,
		"disable": Disable,
	}
	for _, name := range []string{"*", "Win*", "Spool?r", "[a-z]*", "-Force"} {
		for label, call := range calls {
			t.Run(label+" "+name, func(t *testing.T) {
				err := call(context.Background(), session, name)
				if err == nil || !strings.Contains(err.Error(), "service name is refused") {
					t.Fatalf("err = %v, want the name refused before anything is sent", err)
				}
			})
		}
	}
	if err := Start(context.Background(), session, "Spooler"); err == nil || strings.Contains(err.Error(), "is refused") {
		t.Fatalf("a real name = %v, want it to reach the dial and fail there", err)
	}
}
