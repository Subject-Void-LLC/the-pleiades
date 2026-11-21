// The inspection's other promises (inspect.go, inspect_check.go): a fact is
// named only when asked and only when its row opens, a fleet inspection
// reports the devices that are not healthy, an unknown device is refused,
// and the refusals a facts route answers with name the command to run.
package entstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// failureUnder is the first failure inspecting web1 through store found
// under the keys of role, with the fact names shown.
func failureUnder(t *testing.T, store *entstore.Store, role string) *factsreport.RowFailure {
	t.Helper()
	got, err := store.InspectDevice(context.Background(), "web1", entstore.InspectOptions{ShowFactNames: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Under {
		if c.Role == role && c.Failure != nil {
			return c.Failure
		}
	}
	t.Fatalf("no failure under the %s key: %+v", role, got.Under)
	return nil
}

func TestInspect_NamesAFactOnlyWhenAskedAndOnlyWhenItOpens(t *testing.T) {
	h := newHistory(t, onSQLite)
	users := h.userRows()
	h.copyPayload(users[1], users[2])
	if f := failureUnder(t, h.store(""), factsreport.RolePrevious); f.Kind != factsreport.FaultHashMismatch || f.Fact != "user.zq9operator" {
		t.Errorf("a row that opens: %+v, want its fact named", f)
	}
	// A payload that does not open names nothing: there is nothing to read.
	h.copyPayload(h.stable(0), h.stable(1))
	if f := failureUnder(t, h.store(""), factsreport.RolePrevious); f.Kind != factsreport.FaultDoesNotOpen || f.Fact != "" {
		t.Errorf("a row that does not open: %+v, want no fact named", f)
	}
}

func TestInspectAll_ReportsTheDevicesThatAreNotHealthy(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	// web2 verifies under the previous key; web1 does not; web3 stores
	// nothing; web4 is healthy under the current key.
	for _, name := range []string{"web2", "web3", "web4"} {
		d := h.client.Device.Create().SetName(name).SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.9"}).SaveX(ctx)
		switch name {
		case "web2":
			gatherRotationHistory(t, entstore.New(h.client, h.old, factstest.Envelope()), d.DeviceID)
		case "web4":
			gatherRotationHistory(t, entstore.New(h.client, newFactKeys(t, 7), factstest.Envelope()), d.DeviceID)
		}
	}
	h.exec("UPDATE facts SET chain = '' WHERE id = ?", h.stable(0))
	raw := rawFactTables(t, h.db)
	report, err := h.store("").InspectAll(ctx, entstore.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rawFactTables(t, h.db); got != raw {
		t.Fatal("a fleet inspection changed the database")
	}
	var names []string
	for _, d := range report.Devices {
		names = append(names, d.Device+"="+string(d.State))
	}
	if report.Scope != factsreport.ScopeFleet || report.Scanned != 3 || report.NeedsRepair != 1 ||
		strings.Join(names, ",") != "web1=refused,web2=rekey_pending" || report.Keys != h.store("").Keys() {
		t.Errorf("the fleet report: %+v (%s); want web1 refused and web2 pending, of three devices with facts", report, names)
	}
	all, err := h.store("").InspectAll(ctx, entstore.InspectOptions{All: true})
	if err != nil || len(all.Devices) != 3 {
		t.Errorf("--all: %d devices, %v; want every device with stored facts", len(all.Devices), err)
	}
}

func TestInspect_RefusesAnUnknownDevice(t *testing.T) {
	h := newHistory(t, onSQLite)
	if _, err := h.store("").InspectDevice(context.Background(), "no-such-device", entstore.InspectOptions{}); !errors.Is(err, entstore.ErrUnknownDevice) {
		t.Errorf("an unknown device: %v, want ErrUnknownDevice", err)
	}
}

// TestUnavailable_NamesTheInspectCommand: the two refusals only an operator
// on the controller's host can look into say how, and still name no fact,
// key identity or text of the error.
func TestUnavailable_NamesTheInspectCommand(t *testing.T) {
	for _, err := range []error{entstore.ErrRekeyRefused, entstore.ErrKeysChanged} {
		cause, ok := entstore.Unavailable(err)
		if !ok || !strings.Contains(cause, "controller facts inspect --device") || strings.Contains(cause, err.Error()) {
			t.Errorf("Unavailable(%v) = %q; want the inspect command named", err, cause)
		}
	}
}
