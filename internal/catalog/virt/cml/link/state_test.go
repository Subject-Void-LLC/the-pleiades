// Tests of virt.cml.link.stop and virt.cml.link.start against cmltest's
// controller: a link stopped by its ends with an undo that starts it, a
// second stop unchanged, a start with an undo that stops it, a check that
// changes nothing, and a link never run refused a start.
package link_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestState_StopAndStartWithTheirUndos is the round trip.
func TestState_StopAndStartWithTheirUndos(t *testing.T) {
	ctl, lab := withLab(t)
	if _, _, err := run(t, ctl, "virt.cml.link.create", false, with(nil)); err != nil {
		t.Fatal(err)
	}
	stateNow := func() string {
		ctl.Mu.Lock()
		defer ctl.Mu.Unlock()
		for _, l := range ctl.Labs[lab].Links {
			if l.State == "" {
				return "STARTED"
			}
			return l.State
		}
		return ""
	}

	res, rc, err := run(t, ctl, "virt.cml.link.stop", true, with(nil))
	if err != nil || !res.Changed || stateNow() != "STARTED" {
		t.Fatalf("check: %+v, %v, state %s", res, err, stateNow())
	}
	res, rc, err = run(t, ctl, "virt.cml.link.stop", false, with(nil))
	if err != nil || !res.Changed || stateNow() != "STOPPED" || rc.Stats["state"] != "STARTED" {
		t.Fatalf("stop: %+v, %v, state %s, stats %v", res, err, stateNow(), rc.Stats)
	}
	id := rc.Stats["id"]
	if inv, ok := rc.Inverse(); !ok || inv[sdk.InverseFQCNKey] != "virt.cml.link.start" || inv["params"].(map[string]any)["link"] != id || inv["params"].(map[string]any)["lab"] != lab {
		t.Fatalf("stop's undo = %v", inv)
	}
	if res, rc, err := run(t, ctl, "virt.cml.link.stop", false, with(nil)); err != nil || res.Changed {
		t.Fatalf("a second stop: %+v, %v", res, err)
	} else if _, ok := rc.Inverse(); ok {
		t.Error("an unchanged stop recorded an undo")
	}

	res, rc, err = run(t, ctl, "virt.cml.link.start", false, map[string]any{"lab": lab, "link": id})
	if err != nil || !res.Changed || stateNow() != "STARTED" {
		t.Fatalf("start by id: %+v, %v, state %s", res, err, stateNow())
	}
	if inv, ok := rc.Inverse(); !ok || inv[sdk.InverseFQCNKey] != "virt.cml.link.stop" {
		t.Fatalf("start's undo = %v", inv)
	}

	ctl.Mu.Lock()
	for _, l := range ctl.Labs[lab].Links {
		l.State = "DEFINED_ON_CORE"
	}
	ctl.Mu.Unlock()
	if _, _, err := run(t, ctl, "virt.cml.link.start", false, with(nil)); err == nil || !strings.Contains(err.Error(), "never run") {
		t.Fatalf("a link never run: %v", err)
	}
}

// TestState_RefusesAnUnclearLinkAndChecksALaterOne: a link named both ways,
// or by fewer than four ends, is refused; one in a lab an earlier task makes
// is unchecked in a check and fails a run.
func TestState_RefusesAnUnclearLinkAndChecksALaterOne(t *testing.T) {
	ctl, _ := withLab(t)
	for name, params := range map[string]map[string]any{
		"both ways":    with(map[string]any{"link": "x"}),
		"three ends":   {"lab": "cvd", "a": "CORE1", "a_interface": "Ethernet0/1", "b": "BR1"},
		"no lab":       {"link": "x"},
		"no such link": {"lab": "cvd", "link": "00000000-0000-4000-8000-999999999999"},
	} {
		if _, _, err := run(t, ctl, "virt.cml.link.stop", false, params); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	var unchecked *collection.CannotCheckError
	if _, _, err := run(t, ctl, "virt.cml.link.stop", true, with(map[string]any{"lab": "later"})); !errors.As(err, &unchecked) {
		t.Errorf("a lab an earlier task makes, checked: %v", err)
	}
}
