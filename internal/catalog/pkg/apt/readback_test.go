// Package apt_test: the post-change reading (Phase 41c). After a real install
// the method emits the very fact facts.gather would store for the same dpkg
// row, so the next gather reads it as unchanged; a check, which changes
// nothing, emits none.
package apt_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestInstall_ReadsTheInstalledPackageBackAsTheGatherersFact(t *testing.T) {
	h := newHarness(t, pkgState{candidate: "7.88.1-10", version: "7.88.1-10"})
	if _, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// The row the fake prints is the row real dpkg-query prints in the
	// gatherer's format; the gatherer's parser makes its fact from it.
	want := hostfacts.ParseDpkg("ii\tcurl\t7.88.1-10\tamd64\n")
	if len(want) != 1 {
		t.Fatalf("the reference row did not parse: %+v", want)
	}
	got, ok := h.rc.stats[hostfacts.PackageKey(want[0])].(map[string]any)
	if !ok {
		t.Fatalf("no %s fact was emitted; stats = %v", hostfacts.PackageKey(want[0]), h.rc.stats)
	}
	if !reflect.DeepEqual(got, hostfacts.PackageValue(want[0])) {
		t.Fatalf("emitted %v, want the gatherer's %v", got, hostfacts.PackageValue(want[0]))
	}
}

func TestCheckInstall_EmitsNoPackageFact(t *testing.T) {
	h := newHarness(t, pkgState{candidate: "7.88.1-10", version: "7.88.1-10"})
	if _, err := apt.CheckInstall(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("CheckInstall: %v", err)
	}
	if _, ok := h.rc.stats[hostfacts.ItemKey(hostfacts.PackageFamily, "curl")]; ok {
		t.Fatal("a check emitted a package fact; it changed nothing, so it read nothing back")
	}
}

// readBackWarnings is the warnings an install left.
func readBackWarnings(t *testing.T, h *harness) string {
	t.Helper()
	w, _ := h.rc.stats[sdk.StatWarnings].([]string)
	return strings.Join(w, " | ")
}

func TestInstall_AReadBackThatExitsNonZeroIsAWarning(t *testing.T) {
	h := newHarness(t, pkgState{candidate: "7.88.1-10", version: "7.88.1-10", readbackExit: 2})
	result, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil || !result.Changed {
		t.Fatalf("Install = %+v, %v; want a change and no failure", result, err)
	}
	if w := readBackWarnings(t, h); !strings.Contains(w, "dpkg-query exited 2 reading its package fact back") {
		t.Fatalf("warnings = %q, want the read-back's exit", w)
	}
}

func TestInstall_AFactTheContextRefusesIsAWarning(t *testing.T) {
	h := newHarness(t, pkgState{candidate: "7.88.1-10", version: "7.88.1-10"})
	h.rc.failOnKey = hostfacts.ItemKey(hostfacts.PackageFamily, "curl")
	if _, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if w := readBackWarnings(t, h); !strings.Contains(w, "its package fact was not emitted") {
		t.Fatalf("warnings = %q, want the refused fact said", w)
	}
}

// TestInstall_AReadBackWhoseSessionFailsIsAWarning: some session budget lets
// the install and its own read back through and refuses the post-change
// reading's session alone; that is a warning, never the task's failure.
func TestInstall_AReadBackWhoseSessionFailsIsAWarning(t *testing.T) {
	for budget := 2; budget <= 12; budget++ {
		h := newHarnessBudgeted(t, pkgState{candidate: "7.88.1-10", version: "7.88.1-10"}, budget)
		if _, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
			continue
		}
		w := readBackWarnings(t, h)
		if strings.Contains(w, "could not read its package fact back") && !strings.Contains(w, "could not read its version back") {
			return
		}
	}
	t.Fatal("no session budget refused the post-change reading alone")
}

// TestUpgrade_AWarningThatCannotBeRecordedFailsTheTask: a read-back warning is
// recorded, and a context that refuses it fails the upgrade on either of its
// changing paths (a fresh install, an upgrade of an older version) rather than
// losing it in silence.
func TestUpgrade_AWarningThatCannotBeRecordedFailsTheTask(t *testing.T) {
	for name, state := range map[string]pkgState{
		"a fresh install": {candidate: "7.88.1-10", version: "7.88.1-10", readbackExit: 2},
		"an upgrade":      {installed: true, version: "1.0", candidate: "1.1", readbackExit: 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, state)
			h.rc.failOnKey = sdk.StatWarnings
			if _, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err == nil {
				t.Fatal("Upgrade succeeded though its read-back warning could not be recorded")
			}
		})
	}
}
