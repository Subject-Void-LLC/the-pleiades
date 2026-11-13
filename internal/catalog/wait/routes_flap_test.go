// Stress of wait.routes on a flapping link: a route that comes and goes, or
// moves between next hops, read after read, against the real in-process SSH
// server (wait_test.go) with a vtysh on its PATH that flaps on a counter,
// so each run of the test sees the same sequence of tables.
package wait_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
)

// hopTable is a table whose one route to 10.1.0.0/24 goes through hop.
func hopTable(hop string) string {
	return `{"10.1.0.0/24":[{"protocol":"ospf","selected":true,"metric":20,"nexthops":[{"ip":"` + hop + `","active":true}]}]}`
}

// flappingVtysh puts a vtysh on the PATH that answers flap on its odd reads
// and steady on its even ones for its first flaps reads (none, when flaps
// is negative: it flaps forever), and steady after. It returns the file
// counting the reads.
func flappingVtysh(t *testing.T, flaps int, flap, steady string) string {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "reads")
	limit := strconv.Itoa(flaps)
	if flaps < 0 {
		limit = "1000000"
	}
	fakeVtysh(t, `n=$(cat '`+counter+`' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '`+counter+`'
if [ $n -le `+limit+` ] && [ $((n % 2)) -eq 1 ]; then echo '`+flap+`'; else echo '`+steady+`'; fi`)
	return counter
}

// reads is how many times the vtysh counting into counter was run.
func reads(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter) // #nosec G304 -- this test's own counter file
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRoutes_AFlappingLinkIsNeverTakenAsSettled: while the route flaps, no
// wait ends, whichever state it waits for: present reads half the time, and
// absent the other half, but the table never holds still for stable_for.
// Once the flap stops, a present wait ends only after the table has held
// still for stable_for from the last change, never on the first read that
// happened to hold the route; and a flap of next hops is held the same way
// against next_hops.
func TestRoutes_AFlappingLinkIsNeverTakenAsSettled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the flapping-link stress, which waits out several timeouts, in short mode")
	}
	server := startWaitServer(t)
	params := func(extra map[string]any) map[string]any { return routesParams(extra) }

	for _, state := range []string{"present", "absent"} {
		t.Run("flapping forever, "+state, func(t *testing.T) {
			counter := flappingVtysh(t, -1, connectedOnly, steadyTable)
			_, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), params(map[string]any{"state": state, "stable_for": 1, "timeout": 5}))
			if err == nil {
				t.Fatalf("a wait for %s ended while the route flapped", state)
			}
			if n := reads(t, counter); n < 4 {
				t.Errorf("the table was read %d times in a 5 second wait, want one read a second", n)
			}
		})
	}

	t.Run("a flap that stops", func(t *testing.T) {
		const flaps = 6
		counter := flappingVtysh(t, flaps, connectedOnly, steadyTable)
		rc := newWaitContext(server)
		if _, err := wait.Routes(context.Background(), rc, newWaitTarget(server), params(map[string]any{"stable_for": 2, "timeout": 30})); err != nil {
			t.Fatalf("Routes = %v, want the wait to end once the flap stopped", err)
		}
		// The last change is read flaps (steady after the last flap on read
		// flaps-1); held still for two seconds of one-second reads is at
		// least two reads past it.
		if n := reads(t, counter); n < flaps+2 {
			t.Errorf("the wait ended after %d reads, before the table had held still for stable_for after read %d", n, flaps)
		}
		if waited, _ := rc.stats["waited"].(float64); waited < 2 {
			t.Errorf("waited = %v, want at least stable_for", rc.stats["waited"])
		}
	})

	t.Run("next hops that flap", func(t *testing.T) {
		const flaps = 5
		counter := flappingVtysh(t, flaps, hopTable("10.0.0.2"), hopTable("10.0.0.1"))
		if _, err := wait.Routes(context.Background(), newWaitContext(server), newWaitTarget(server), params(map[string]any{
			"next_hops": map[string]any{"10.1.0.0/24": []any{"10.0.0.1"}}, "stable_for": 2, "timeout": 30,
		})); err != nil {
			t.Fatalf("Routes = %v, want the wait to end once the next hop held", err)
		}
		if n := reads(t, counter); n < flaps+2 {
			t.Errorf("the wait ended after %d reads, before the next hop had held for stable_for", n)
		}
	})
}
