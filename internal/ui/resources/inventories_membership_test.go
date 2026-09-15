// Package resources_test's inventory membership coverage: putting a device
// in an inventory, which is the one thing that stood between an inventory
// existing and a runbook being dispatchable against it.
//
// The store always supported this. The UI refused, because the edit form
// carried no control for either id list, so the writer copied both back
// from storage on every save to avoid clearing them.
package resources_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestInventoryForm_OffersTheFleetAndMarksCurrentMembership is the chooser
// itself: every device is offered, and the ones already in the inventory
// come back selected.
func TestInventoryForm_OffersTheFleetAndMarksCurrentMembership(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/inventories/1/edit")
	for _, want := range []string{"router-1", "router-2", "router-3"} {
		if !strings.Contains(form, want) {
			t.Errorf("the inventory edit form does not offer %s", want)
		}
	}

	// The seeded inventory holds devices 11 and 12 and not 13. Whichever
	// way the control renders selection, an unselected option cannot be
	// distinguished from a missing one unless the difference is asserted.
	block := selectBlock(t, form, "devices")
	if !strings.Contains(block, "selected") {
		t.Fatalf("the devices control marks nothing as selected:\n%s", block)
	}
}

// selectBlock returns the markup of one named select.
func selectBlock(t *testing.T, body, field string) string {
	t.Helper()
	start := strings.Index(body, `name="`+field+`"`)
	if start < 0 {
		t.Fatalf("the form rendered no control named %q", field)
	}
	open := strings.LastIndex(body[:start], "<select")
	end := strings.Index(body[start:], "</select>")
	if open < 0 || end < 0 {
		t.Fatalf("the control named %q is not a select", field)
	}
	return body[open : start+end]
}

// TestInventoryForm_AddingADeviceSticks is the whole point: a device chosen
// in the browser is in the inventory afterwards.
func TestInventoryForm_AddingADeviceSticks(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := url.Values{}
	form.Set("name", "production")
	form.Set("description", "The fleet that matters.")
	form.Add("groups", "7")
	// The two it already had, plus one it did not.
	form.Add("devices", "11")
	form.Add("devices", "12")
	form.Add("devices", "13")

	if w := h.postValues(t, "/ui/inventories/1", form); w.Code >= http.StatusBadRequest {
		t.Fatalf("editing an inventory's membership = %d: %s", w.Code, w.Body.String())
	}

	// The Devices tab is where a person would look to confirm it, so that
	// is where this asserts, rather than reaching into the store.
	devices := h.section(t, "/ui/inventories/1", "Devices")
	if !strings.Contains(devices, "router-3") {
		t.Errorf("the device added through the form is not in the inventory's Devices tab:\n%s", devices)
	}
	for _, kept := range []string{"router-1", "router-2"} {
		if !strings.Contains(devices, kept) {
			t.Errorf("adding a device dropped %s, which was already a member", kept)
		}
	}
}

// TestInventoryForm_DeselectingADeviceRemovesIt is the half that the old
// copy-from-storage writer made impossible to express.
//
// It matters more than it looks. A membership control that can only add is
// not a control, it is an accumulator, and the failure is silent: the page
// reports success and the device is still there.
func TestInventoryForm_DeselectingADeviceRemovesIt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := url.Values{}
	form.Set("name", "production")
	form.Set("description", "The fleet that matters.")
	form.Add("groups", "7")
	form.Add("devices", "11")

	if w := h.postValues(t, "/ui/inventories/1", form); w.Code >= http.StatusBadRequest {
		t.Fatalf("removing a device = %d: %s", w.Code, w.Body.String())
	}

	devices := h.section(t, "/ui/inventories/1", "Devices")
	if strings.Contains(devices, "router-2") {
		t.Error("deselecting a device did not remove it from the inventory")
	}
	if !strings.Contains(devices, "router-1") {
		t.Error("removing one device also removed the one that stayed selected")
	}
}
