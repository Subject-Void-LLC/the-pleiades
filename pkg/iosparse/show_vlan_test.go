// Package iosparse_test: ShowVlan joins each wrapped status line onto the
// line before it, and a run of such lines must cost work linear in their
// number. The test counts that work in bytes allocated, not in time.
package iosparse_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// wrappedStatusRun is show vlan output: the table header and its rule,
// which no pattern handles, followed by n status-only lines. Each is the
// line a real overlong VLAN name leaves below it, copied from genieparser's
// golden_output_vlan_5 fixture (38 spaces, "act/unsup Gi0/0/0"). ShowVlan
// joins such a line onto the line before it. Joined onto the rule, the
// result matches no pattern either, so it becomes the line the next one
// joins onto, and the text grows by one line per line.
func wrappedStatusRun(n int) string {
	var b strings.Builder
	b.WriteString("VLAN Name                             Status    Ports\n")
	b.WriteString("---- -------------------------------- --------- -------------------------------\n")
	for i := 0; i < n; i++ {
		b.WriteString(strings.Repeat(" ", 38) + "act/unsup Gi0/0/0\n")
	}
	return b.String()
}

// allocBytes returns the fewest heap bytes f allocated over three runs.
// Two collections before each run empty the regexp package's pools, so
// every run starts from the same state. Without the race detector the
// count is then the same on every run, unless the parse allocates enough
// to start a collection of its own; with it, sync.Pool drops a random
// quarter of what it is given, and the regexp machines made again add
// noise that grows with the number of lines, never with their square.
func allocBytes(f func()) uint64 {
	best := ^uint64(0)
	for i := 0; i < 3; i++ {
		// A sync.Pool survives one collection in its victim cache and is
		// emptied by the second.
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n < best {
			best = n
		}
	}
	return best
}

// TestShowVlan_WrappedRunIsLinear holds the work ShowVlan does on a run of
// wrapped status lines to linear growth: parsing four times as many may
// allocate at most six times as much. Appending each line once makes that
// about four. Joining each onto a fresh copy of all the text before it
// allocates the sum of the growing lengths, which grows about sixteen
// times, and still about ten times with the race detector's noise added.
// The two sizes are large enough that the joins outweigh that noise.
func TestShowVlan_WrappedRunIsLinear(t *testing.T) {
	// No pattern handles any line of the run, which is what makes the
	// joined text grow; the parse finds nothing and says so.
	if _, parsed, err := iosparse.Parse("show vlan", wrappedStatusRun(8)); !errors.Is(err, iosparse.ErrEmpty) {
		t.Fatalf("Parse = %v, %v; want ErrEmpty", parsed, err)
	}
	parse := func(n int) func() {
		out := wrappedStatusRun(n)
		return func() {
			// Only the work matters here; the result is ErrEmpty.
			_, _, _ = iosparse.Parse("show vlan", out)
		}
	}
	const n = 1024
	small := allocBytes(parse(n))
	large := allocBytes(parse(4 * n))
	t.Logf("%d wrapped lines: %d bytes allocated; %d: %d (ratio %.2f)",
		n, small, 4*n, large, float64(large)/float64(small))
	if large > 6*small {
		t.Errorf("%d wrapped lines allocated %d bytes and %d allocated %d: more than linear growth",
			n, small, 4*n, large)
	}
}

// TestShowVlan_WrappedNamesInARow holds ShowVlan to two overlong VLAN
// names in a row, each with its status wrapped onto the next line. The
// second status line must join onto the second name, the last line no
// pattern handled, never onto the first joined line, which a pattern did
// handle. genieparser's golden_output_vlan_5 fixture has one such name;
// the case adds a second after it and wants the fixture's expected result
// plus that VLAN, as genieparser's prev_line + ' ' + line reads it.
func TestShowVlan_WrappedNamesInARow(t *testing.T) {
	dir := filepath.Join("testdata", "ShowVlan", "iosxe", "equal")
	out, _ := readFixture(t, dir, "golden_output_vlan_5")
	first := "1006 fddinet-fdnet-trbrf-default-Test-long\n" + strings.Repeat(" ", 38) + "act/unsup Gi0/0/0\n"
	second := "1007 a-second-vlan-name-long-enough-to-wrap\n" + strings.Repeat(" ", 38) + "active    Gi0/0/1\n"
	// The first name and its status must be there exactly once, or the
	// case tests nothing.
	if n := strings.Count(out, first); n != 1 {
		t.Fatalf("the fixture holds the wrapped name %d times, want once", n)
	}
	out = strings.Replace(out, first, first+second, 1)
	data, err := os.ReadFile(filepath.Join(dir, "golden_output_vlan_5_expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	vlans, ok := want["vlans"].(map[string]any)
	if !ok {
		t.Fatal("the fixture's expected result has no vlans")
	}
	vlans["1007"] = map[string]any{
		"vlan_id": "1007", "name": "a-second-vlan-name-long-enough-to-wrap", "shutdown": false,
		"state": "active", "interfaces": []any{"GigabitEthernet0/0/1"},
	}
	_, parsed, err := iosparse.Parse("show vlan", out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d := firstDiff("", normalize(t, parsed), want); d != "" {
		t.Fatal(d)
	}
}
