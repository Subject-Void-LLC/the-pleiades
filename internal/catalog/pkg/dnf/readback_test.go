// Package dnf_test: the post-change reading (Phase 41c). After a real upgrade
// the method emits the very fact facts.gather would store for the same rpm row,
// so the next gather reads it as unchanged.
package dnf_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestUpgrade_ReadsTheUpgradedPackageBackAsTheGatherersFact(t *testing.T) {
	h := newHarness(t, installedOld)
	if _, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	// The row the fake prints is the row real rpm prints in the gatherer's
	// format; the gatherer's parser makes its fact from it.
	want := hostfacts.ParseRPM("curl\t(none)\t1.0-1\tx86_64\n")
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

// readBackWarnings is the warnings a task left.
func readBackWarnings(h *harness) string {
	w, _ := h.rc.stats[sdk.StatWarnings].([]string)
	return strings.Join(w, " | ")
}

func TestUpgrade_AReadBackThatExitsNonZeroIsAWarning(t *testing.T) {
	state := installedOld
	state.readbackExit = 2
	h := newHarness(t, state)
	if _, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if w := readBackWarnings(h); !strings.Contains(w, "rpm -q exited 2 reading its package fact back") {
		t.Fatalf("warnings = %q, want the read-back's exit", w)
	}
}

func TestUpgrade_ANameRPMHoldsNoPackageUnderIsNoWarning(t *testing.T) {
	state := installedOld
	state.readbackExit = 1
	h := newHarness(t, state)
	if _, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("vim", nil)); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if w := readBackWarnings(h); w != "" {
		t.Fatalf("warnings = %q, want none: a name dnf resolved to another package holds none under its own", w)
	}
	if _, ok := h.rc.stats[hostfacts.ItemKey(hostfacts.PackageFamily, "vim")]; ok {
		t.Fatal("a package fact was emitted for a name rpm holds no package under")
	}
}

func TestUpgrade_AFactTheContextRefusesIsAWarning(t *testing.T) {
	h := newHarness(t, installedOld)
	h.rc.failOnKey = hostfacts.ItemKey(hostfacts.PackageFamily, "curl")
	if _, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if w := readBackWarnings(h); !strings.Contains(w, "its package fact was not emitted") {
		t.Fatalf("warnings = %q, want the refused fact said", w)
	}
}

// TestUpgrade_AReadBackWhoseSessionFailsIsAWarning: some session budget lets
// the upgrade and its own read through and refuses the post-change reading's
// session alone; that is a warning, never the task's failure.
func TestUpgrade_AReadBackWhoseSessionFailsIsAWarning(t *testing.T) {
	for budget := 2; budget <= 12; budget++ {
		h := newHarnessBudgeted(t, installedOld, budget)
		if _, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
			continue
		}
		if strings.Contains(readBackWarnings(h), "could not read its package fact back") {
			return
		}
	}
	t.Fatal("no session budget refused the post-change reading alone")
}

// TestReadBack_AWarningThatCannotBeRecordedFailsTheTask: on every changing path
// (an install, an upgrade's fresh install, an upgrade), a read-back warning a
// context refuses to record fails the task rather than vanishing.
func TestReadBack_AWarningThatCannotBeRecordedFailsTheTask(t *testing.T) {
	absentWarn := absent
	absentWarn.readbackExit = 2
	oldWarn := installedOld
	oldWarn.readbackExit = 2
	for name, run := range map[string]struct {
		state pkgState
		call  func(*harness) error
	}{
		"install": {absentWarn, func(h *harness) error {
			_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
			return err
		}},
		"upgrade installing": {absentWarn, func(h *harness) error {
			_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
			return err
		}},
		"upgrade": {oldWarn, func(h *harness) error {
			_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, run.state)
			h.rc.failOnKey = sdk.StatWarnings
			if err := run.call(h); err == nil {
				t.Fatal("the task succeeded though its read-back warning could not be recorded")
			}
		})
	}
}
