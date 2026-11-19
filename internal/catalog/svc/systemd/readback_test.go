// Package systemd_test: the post-change reading (Phase 41c). After a real
// change the method emits the very service.<unit> and service_state.<unit>
// facts facts.gather would store from the same listings; a converged run and a
// check emit none.
package systemd_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/systemd"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gatheredFacts is what the shared parser makes of the listings the fake prints
// for nginx.service in the given unit-file state and active state, split as the
// gatherer splits them.
func gatheredFacts(unitFile, active string) map[string]any {
	sub := "dead"
	if active == "active" {
		sub = "running"
	}
	listing := "nginx.service " + unitFile + " enabled\n" + hostfacts.ServiceSectionMark + "\n" +
		"nginx.service loaded " + active + " " + sub + " nginx\n" + hostfacts.ServiceSectionMark + "\n"
	out := map[string]any{}
	for _, f := range hostfacts.ParseSystemd(listing) {
		for _, part := range hostfacts.SplitServiceState(f) {
			out[part.Key] = part.Value
		}
	}
	return out
}

func TestStart_ReadsTheUnitBackAsTheGatherersFacts(t *testing.T) {
	h := newHarness(t, stoppedEnabled)
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := gatheredFacts("enabled", "active")
	if len(want) != 2 {
		t.Fatalf("the reference listing made %v, want a service and its state", want)
	}
	for key, value := range want {
		if got := h.rc.stats[key]; !reflect.DeepEqual(got, value) {
			t.Errorf("%s = %v, want the gatherer's %v", key, got, value)
		}
	}
}

func TestStart_AConvergedRunReadsNothingBack(t *testing.T) {
	h := newHarness(t, runningEnabled)
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := h.rc.stats[hostfacts.ItemKey(hostfacts.ServiceFamily, "nginx.service")]; ok {
		t.Fatal("a run that changed nothing emitted a service fact")
	}
}

// readBackWarnings is the warnings a run left.
func readBackWarnings(h *harness) string {
	w, _ := h.rc.stats[sdk.StatWarnings].([]string)
	return strings.Join(w, " | ")
}

func TestStart_AUnitNamedWithItsTypeReadsBackTheSameFacts(t *testing.T) {
	h := newHarness(t, stoppedEnabled)
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx.service")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := h.rc.stats[hostfacts.ItemKey(hostfacts.ServiceFamily, "nginx.service")]; !ok {
		t.Fatalf("no service fact for nginx.service; stats = %v", h.rc.stats)
	}
}

func TestStart_AListingThatDoesNotAnswerIsAWarning(t *testing.T) {
	t.Setenv("FAKE_LIST_SILENT", "1")
	h := newHarness(t, stoppedEnabled)
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if w := readBackWarnings(h); !strings.Contains(w, "did not answer as they always do") {
		t.Fatalf("warnings = %q, want the listing's silence said", w)
	}
}

func TestStart_AFactTheContextRefusesIsAWarning(t *testing.T) {
	h := newHarness(t, stoppedEnabled)
	h.rc.failOnKey = hostfacts.ItemKey(hostfacts.ServiceFamily, "nginx.service")
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if w := readBackWarnings(h); !strings.Contains(w, "was not emitted") {
		t.Fatalf("warnings = %q, want the refused fact said", w)
	}
}

func TestStart_AWarningThatCannotBeRecordedFailsTheTask(t *testing.T) {
	t.Setenv("FAKE_LIST_SILENT", "1")
	h := newHarness(t, stoppedEnabled)
	h.rc.failOnKey = sdk.StatWarnings
	if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err == nil {
		t.Fatal("Start succeeded though its read-back warning could not be recorded")
	}
}
