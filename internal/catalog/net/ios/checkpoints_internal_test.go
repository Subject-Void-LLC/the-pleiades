// Package ios: net.ios.checkpoints against the checkpoint flow's device
// double, with dir answered as IOL XE 17.15.1 answers it (measured through
// pleiades on the CML sandbox, 2026-10-25).
package ios

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// measuredDir is dir unix: as the sandbox's router printed it after one
// net.ios.config change, other files kept to show they are passed over.
const measuredDir = "Directory of unix:/\r\n\r\n" +
	"145478  -rw-             2068  Oct 26 2026 01:16:16 +00:00  pleiades-e1beac8a0f57-after.cfg\r\n" +
	"145407  -rw-             1999  Oct 26 2026 01:16:14 +00:00  pleiades-e1beac8a0f57.cfg\r\n" +
	"145474  drwx             4096  Oct 26 2026 01:04:33 +00:00  pnp-tech\r\n" +
	"145456  -rw-           262144  Oct 26 2026 01:01:58 +00:00  nvram_00019\r\n" +
	"145457  -r-x              604  Oct 26 2026 01:01:32 +00:00  config\r\n\r\n" +
	"2147479552 bytes total (2147479552 bytes free)\r\n"

// TestReadCheckpoints_TheMeasuredListing: only the minted files are read, a
// change's two files are one set, with the before file's time and both sizes.
func TestReadCheckpoints_TheMeasuredListing(t *testing.T) {
	sets := readCheckpoints(measuredDir, "unix:")
	if len(sets) != 1 {
		t.Fatalf("sets = %+v", sets)
	}
	s := sets[0]
	want := time.Date(2026, 10, 26, 1, 16, 14, 0, time.UTC)
	if s.id != "e1beac8a0f57" || s.before != "unix:pleiades-e1beac8a0f57.cfg" || s.after != "unix:pleiades-e1beac8a0f57-after.cfg" || !s.modified.Equal(want) || s.bytes != 4067 {
		t.Fatalf("set = %+v", s)
	}
}

// dirDevice is the checkpoint double answering dir from its files, each
// with the time its name's table gives.
type dirDevice struct {
	*configDevice
	times map[string]time.Time
	// clock is what show clock answers.
	clock time.Time
	// failDelete names a file whose delete fails.
	failDelete string
}

// Command answers dir unix: and passes everything else to the double.
func (d *dirDevice) Command(ctx context.Context, line string) (string, error) {
	switch {
	case line == "show clock":
		d.commands = append(d.commands, line)
		return "*" + d.clock.Format("15:04:05.000 MST Mon Jan 2 2006") + "\r\n", nil
	case d.failDelete != "" && line == "delete /force "+d.failDelete:
		d.commands = append(d.commands, line)
		return "%Error deleting " + d.failDelete + " (Device or resource busy)\r\n", nil
	case line != "dir unix:":
		return d.configDevice.Command(ctx, line)
	}
	d.commands = append(d.commands, line)
	names := make([]string, 0, len(d.files))
	for path := range d.files {
		names = append(names, strings.TrimPrefix(path, "unix:"))
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("Directory of unix:/\r\n\r\n")
	for i, n := range names {
		fmt.Fprintf(&b, "%d  -rw-  %d  %s  %s\r\n", 1000+i, len(d.files["unix:"+n]), d.times["unix:"+n].Format(dirTime), n)
	}
	b.WriteString("\r\n2147479552 bytes total (2147479552 bytes free)\r\n")
	return b.String(), nil
}

// threeChanges is a double holding three changes' checkpoints, a day apart,
// the newest an hour old and with no after file (a change that failed),
// besides a startup configuration that must never be touched.
func threeChanges(t *testing.T) *dirDevice {
	t.Helper()
	return threeChangesAt(t, time.Now().UTC().Truncate(time.Second))
}

// threeChangesAt is threeChanges with the device's clock reading now.
func threeChangesAt(t *testing.T, now time.Time) *dirDevice {
	t.Helper()
	d := &dirDevice{configDevice: newConfigDevice(), times: map[string]time.Time{}, clock: now}
	for i, id := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"} {
		at := now.Add(-time.Hour - time.Duration(2-i)*24*time.Hour)
		d.files["unix:pleiades-"+id+".cfg"], d.times["unix:pleiades-"+id+".cfg"] = iosBase, at
		if id != "cccccccccccc" {
			d.files["unix:pleiades-"+id+"-after.cfg"], d.times["unix:pleiades-"+id+"-after.cfg"] = iosBase, at.Add(2*time.Second)
		}
	}
	d.files["unix:startup-config"], d.times["unix:startup-config"] = iosBase, now
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return d, nil
	})
	return d
}

// TestCheckpoints_ListsAndPrunesOnlyMintedFiles: listing changes nothing;
// keep deletes all but the newest; older_than deletes by age; a check
// reports the same files and deletes none; a startup configuration is never
// touched.
func TestCheckpoints_ListsAndPrunesOnlyMintedFiles(t *testing.T) {
	d := threeChanges(t)
	rc := newStubContext()
	res, err := Checkpoints(context.Background(), rc, nil, map[string]any{})
	if err != nil || res.Changed {
		t.Fatalf("list: %+v, %v", res, err)
	}
	listed := rc.stats[statCheckpoints].([]any)
	if len(listed) != 3 || listed[0].(map[string]any)["id"] != "cccccccccccc" || listed[0].(map[string]any)["after"] != "" {
		t.Fatalf("listed = %v (want three, newest first, the failed change with no after file)", listed)
	}

	rc = newStubContext()
	res, err = CheckCheckpoints(context.Background(), rc, nil, map[string]any{"prune": true, "keep": 1})
	if err != nil || !res.Changed || len(rc.stats[statDeleted].([]string)) != 4 || len(d.files) != 6 {
		t.Fatalf("check: %+v, %v, deleted %v, files %d", res, err, rc.stats[statDeleted], len(d.files))
	}

	rc = newStubContext()
	res, err = Checkpoints(context.Background(), rc, nil, map[string]any{"prune": true, "older_than": "36h"})
	if err != nil || !res.Changed {
		t.Fatalf("older_than: %+v, %v", res, err)
	}
	if _, gone := d.files["unix:pleiades-aaaaaaaaaaaa.cfg"]; gone || len(d.files) != 4 {
		t.Fatalf("after older_than 36h, files = %v", slices.Sorted(mapsKeys(d.files)))
	}

	rc = newStubContext()
	if _, err := Checkpoints(context.Background(), rc, nil, map[string]any{"prune": true, "keep": 0}); err != nil {
		t.Fatal(err)
	}
	if len(d.files) != 1 || d.files["unix:startup-config"] == "" {
		t.Fatalf("after keep 0, files = %v: only the startup configuration may be left", slices.Sorted(mapsKeys(d.files)))
	}
}

// TestCheckpoints_RefusesAnUnclearPrune: prune without keep or older_than,
// keep or older_than without prune, and a bad duration are refused before
// the device is reached.
func TestCheckpoints_RefusesAnUnclearPrune(t *testing.T) {
	d := threeChanges(t)
	for name, params := range map[string]map[string]any{
		"prune alone":          {"prune": true},
		"keep without prune":   {"keep": 2},
		"a negative keep":      {"prune": true, "keep": -1},
		"a duration that isnt": {"prune": true, "older_than": "three days"},
	} {
		if _, err := Checkpoints(context.Background(), newStubContext(), nil, params); err == nil {
			t.Errorf("%s was not refused", name)
		}
	}
	if len(d.commands) != 0 {
		t.Errorf("the device was reached: %v", d.commands)
	}
}

// mapsKeys returns m's keys.
func mapsKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestCheckpoints_AgeIsTheDevicesOwn: a router whose clock was never set
// (1993, as IOS starts) dates its files by that clock; older_than measures
// against it, so only the old changes go, never every one (the review's F6).
func TestCheckpoints_AgeIsTheDevicesOwn(t *testing.T) {
	d := threeChangesAt(t, time.Date(1993, 3, 1, 12, 0, 0, 0, time.UTC))
	if _, err := Checkpoints(context.Background(), newStubContext(), nil, map[string]any{"prune": true, "older_than": "36h"}); err != nil {
		t.Fatal(err)
	}
	if _, kept := d.files["unix:pleiades-cccccccccccc.cfg"]; !kept || len(d.files) != 4 {
		t.Fatalf("after older_than 36h on a 1993 clock, files = %v", slices.Sorted(mapsKeys(d.files)))
	}
}

// TestCheckpoints_AFailedDeleteSaysWhatWent: a delete the device refuses
// fails the task, reporting the files it did delete and that it changed
// something (the review's F13).
func TestCheckpoints_AFailedDeleteSaysWhatWent(t *testing.T) {
	d := threeChanges(t)
	d.failDelete = "unix:pleiades-aaaaaaaaaaaa.cfg"
	rc := newStubContext()
	res, err := Checkpoints(context.Background(), rc, nil, map[string]any{"prune": true, "keep": 1})
	if err == nil || !strings.Contains(err.Error(), "busy") || !res.Changed {
		t.Fatalf("res %+v, err %v", res, err)
	}
	deleted, _ := rc.stats[statDeleted].([]string)
	if !slices.Contains(deleted, "unix:pleiades-aaaaaaaaaaaa-after.cfg") || slices.Contains(deleted, d.failDelete) {
		t.Fatalf("deleted = %v", deleted)
	}
}
