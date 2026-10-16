// Tests for pgclient that need no server: what Open refuses before it
// dials anything.
package pgclient_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
)

// unreachable is a target whose dialer records that it was called and
// fails, for a test that must see Open refuse before dialing.
func unreachable(dialed *bool) pgclient.Target {
	return pgclient.Target{
		Host: "db", Port: 5432, User: "app", Password: "pw-for-tests", Database: "app",
		Dial: func(context.Context) (net.Conn, error) {
			*dialed = true
			return nil, errors.New("dialed")
		},
	}
}

// TestOpen_RefusesWhilePQGODebugIsSet: with PQGO_DEBUG set the driver
// writes each connection's password, and every query and row, to
// standard error, so Open refuses before it dials, for any value.
func TestOpen_RefusesWhilePQGODebugIsSet(t *testing.T) {
	for _, value := range []string{"1", "yes"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PQGO_DEBUG", value)
			dialed := false
			_, err := pgclient.Open(context.Background(), unreachable(&dialed))
			if err == nil || !strings.Contains(err.Error(), "PQGO_DEBUG") {
				t.Fatalf("Open = %v, want a refusal naming PQGO_DEBUG", err)
			}
			if dialed {
				t.Error("Open dialed with PQGO_DEBUG set")
			}
		})
	}
}

// pqDebugChild marks the re-executed test binary as the child of
// TestOpen_RefusesPQGODebugSetAtStart.
const pqDebugChild = "PGCLIENT_TEST_PQGO_DEBUG_CHILD"

// TestOpen_RefusesPQGODebugSetAtStart: the driver reads PQGO_DEBUG once,
// as the program starts, so unsetting it later leaves its output on. The
// test binary is run again with it set from the start; that child unsets
// it and must still be refused.
func TestOpen_RefusesPQGODebugSetAtStart(t *testing.T) {
	if os.Getenv(pqDebugChild) == "1" {
		if err := os.Unsetenv("PQGO_DEBUG"); err != nil {
			t.Fatal(err)
		}
		dialed := false
		if _, err := pgclient.Open(context.Background(), unreachable(&dialed)); err == nil || !strings.Contains(err.Error(), "PQGO_DEBUG") || dialed {
			t.Fatalf("with PQGO_DEBUG set at start and unset since: Open = %v, dialed %v", err, dialed)
		}
		return
	}
	// #nosec G204 -- the test binary itself, rerunning this one test.
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpen_RefusesPQGODebugSetAtStart$", "-test.count=1")
	cmd.Env = append(os.Environ(), "PQGO_DEBUG=1", pqDebugChild+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child: %v\n%s", err, out)
	}
}
