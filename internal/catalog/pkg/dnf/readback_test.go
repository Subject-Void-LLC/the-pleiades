// Package dnf_test: the post-change reading (Phase 41c). After a real upgrade
// the method emits the very fact facts.gather would store for the same rpm row,
// so the next gather reads it as unchanged.
package dnf_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
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
