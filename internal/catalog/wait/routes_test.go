// Tests of how wait.routes fails, against the real in-process SSH server
// (wait_test.go) with a vtysh on its shell's PATH that answers as a test
// needs: refusing, printing what is not JSON, or a table that keeps
// moving. routes_live_test.go runs it against real FRR.
package wait_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
)

// steadyTable is a routing table with one selected route to 10.1.0.0/24.
const steadyTable = `{"10.1.0.0/24":[{"protocol":"ospf","selected":true,"metric":20,"nexthops":[{"ip":"10.0.0.1","active":true}]}]}`

// connectedOnly is a table after a route was withdrawn: the router still
// has its connected route, as every router that routes anything does.
const connectedOnly = `{"10.0.0.0/30":[{"protocol":"connected","selected":true,"nexthops":[{"interfaceName":"eth1","active":true}]}]}`

// fakeVtysh puts a vtysh running script first on the PATH of every shell
// the server starts for the rest of t.
func fakeVtysh(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vtysh"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil { // #nosec G306 -- an executable in this test's own directory
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// routesParams is a task waiting for 10.1.0.0/24 over the harness.
func routesParams(extra map[string]any) map[string]any {
	p := map[string]any{"prefixes": []any{"10.1.0.0/24"}, "stable_for": 0, "timeout": 5, "insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// TestRoutes_RefusesBeforeReachingARouter: parameters no router could
// answer and a device with no SSH address fail before anything is sent.
func TestRoutes_RefusesBeforeReachingARouter(t *testing.T) {
	if _, err := wait.Routes(context.Background(), nil, nil, map[string]any{}); err == nil || !strings.Contains(err.Error(), "prefixes") {
		t.Errorf("no prefixes = %v, want a refusal naming prefixes", err)
	}
	server := startWaitServer(t)
	if _, err := wait.Routes(context.Background(), newWaitContext(server), newWaitUnreachable(), routesParams(nil)); err == nil {
		t.Error("a device with no SSH address was reached")
	}
	if got := server.srv.Commands(); len(got) != 0 {
		t.Errorf("commands sent = %q, want none", got)
	}
}

// TestRoutes_ReportsWhatTheRouterAnswered: a vtysh that fails is named
// with its own words, bounded and quoted; one that prints what is not
// JSON is named as such; a session the router refuses is a read failure.
func TestRoutes_ReportsWhatTheRouterAnswered(t *testing.T) {
	server := startWaitServer(t)
	for _, tc := range []struct {
		name, script string
		want         []string
	}{
		{"vtysh refuses", `echo '% Unknown command: show ip route json' >&2; exit 1`, []string{`vtysh exited 1: "% Unknown command: show ip route json"`}},
		{"vtysh says too much", `printf '\033[2J' >&2; printf 'x%.0s' $(seq 400) >&2; exit 2`, []string{`vtysh exited 2: "\x1b[2Jxxx`, `x..."`}},
		{"vtysh prints no JSON", `echo 'zebra is not running'`, []string{"the routing table is not JSON"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeVtysh(t, tc.script)
			_, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(nil))
			for _, w := range tc.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("Routes = %v, want it to contain %q", err, w)
				}
			}
			if err != nil && strings.Contains(err.Error(), "\033") {
				t.Errorf("Routes = %q, which carries a raw escape", err)
			}
		})
	}

	refusing := startWaitServerWithSessionBudget(t, 0)
	if _, err := wait.Routes(context.Background(), newWaitContext(refusing), newWaitTarget(refusing), routesParams(nil)); err == nil || !strings.Contains(err.Error(), "reading the routing table") {
		t.Errorf("a refused session = %v, want a read failure", err)
	}
}

// TestRoutes_ATableThatNeverHoldsStillTimesOut: every route present, but
// a metric that moves on every read, fails at the timeout saying the
// table never held still.
func TestRoutes_ATableThatNeverHoldsStillTimesOut(t *testing.T) {
	server := startWaitServer(t)
	counter := filepath.Join(t.TempDir(), "reads")
	fakeVtysh(t, `n=$(cat '`+counter+`' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '`+counter+`'
printf '{"10.1.0.0/24":[{"protocol":"ospf","selected":true,"metric":%d,"nexthops":[{"ip":"10.0.0.1","active":true}]}]}' $n`)
	_, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{"stable_for": 2, "timeout": 3}))
	if err == nil || !strings.Contains(err.Error(), "had not held still for 2s") {
		t.Errorf("Routes = %v, want a timeout saying the table never held still", err)
	}
}

// TestRoutes_ACancelledRunStopsWaiting: a run cancelled while a route is
// still missing returns the cancellation, not a timeout.
func TestRoutes_ACancelledRunStopsWaiting(t *testing.T) {
	server := startWaitServer(t)
	fakeVtysh(t, `echo '`+steadyTable+`'`)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := wait.Routes(ctx, newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{"prefixes": []any{"10.9.0.0/24"}, "timeout": 30}))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Routes = %v, want the run's own cancellation", err)
	}
}

// TestRoutes_RecordFailuresAreReported: each stat the wait records is a
// failure of the task when it cannot be recorded.
func TestRoutes_RecordFailuresAreReported(t *testing.T) {
	server := startWaitServer(t)
	fakeVtysh(t, `echo '`+steadyTable+`'`)
	for _, key := range []string{"waited", "routes", "diff"} {
		rc := newWaitContext(server)
		rc.failOn = key
		if _, err := wait.Routes(context.Background(), rc, newWaitTarget(server), routesParams(nil)); !errors.Is(err, errWaitStat) {
			t.Errorf("failing %s: Routes = %v, want the recording failure", key, err)
		}
	}
}

// TestRoutes_StateAbsentWaitsForTheRouteToGo: with state absent (its case
// ignored) the wait ends once the route is withdrawn, reporting the prefix
// with no next hops; a route that stays fails at the timeout naming it; a
// route moved off the next hop named counts as gone; and a table read
// empty proves nothing gone, so the wait goes on and fails at its timeout
// saying so. The method before state existed refused the parameter, then
// waited for the route present.
func TestRoutes_StateAbsentWaitsForTheRouteToGo(t *testing.T) {
	server := startWaitServer(t)
	counter := filepath.Join(t.TempDir(), "reads")
	fakeVtysh(t, `n=$(cat '`+counter+`' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '`+counter+`'
if [ $n -le 2 ]; then echo '`+steadyTable+`'; else echo '`+connectedOnly+`'; fi`)
	rc := newWaitContext(server)
	if _, err := wait.Routes(context.Background(), rc, newWaitTarget(server), routesParams(map[string]any{"state": "ABSENT"})); err != nil {
		t.Fatalf("Routes = %v, want the wait to end once the route is withdrawn", err)
	}
	if got, ok := rc.stats["routes"].(map[string][]string); !ok || len(got["10.1.0.0/24"]) != 0 || got["10.1.0.0/24"] == nil {
		t.Errorf("routes = %#v, want 10.1.0.0/24 with an empty list", rc.stats["routes"])
	}

	fakeVtysh(t, `echo '{}'`)
	_, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{"state": "absent", "timeout": 2}))
	if err == nil || !strings.Contains(err.Error(), "holds no selected route at all") {
		t.Errorf("Routes over an empty table = %v, want a timeout saying the table read held no route", err)
	}

	fakeVtysh(t, `echo '`+steadyTable+`'`)
	_, err = wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{"state": "absent", "timeout": 2}))
	if err == nil || !strings.Contains(err.Error(), "still has a selected route to 10.1.0.0/24 (through 10.0.0.1)") {
		t.Errorf("Routes = %v, want a timeout naming the route still there", err)
	}
	if _, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{
		"state": "absent", "next_hops": map[string]any{"10.1.0.0/24": []any{"10.0.0.9"}},
	})); err != nil {
		t.Errorf("Routes = %v, want a route through another next hop to count as gone", err)
	}
	if _, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), routesParams(map[string]any{"state": "gone"})); err == nil || !strings.Contains(err.Error(), "neither present nor absent") {
		t.Errorf("state gone = %v, want a refusal naming both", err)
	}
}
