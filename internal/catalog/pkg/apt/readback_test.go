// Package apt_test: the post-change reading (Phase 41c). After a real install
// the method emits the very fact facts.gather would store for the same dpkg
// row, so the next gather reads it as unchanged; a check, which changes
// nothing, emits none.
package apt_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
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
