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

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
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

// TestInspectAll_SaysWhenEveryDeviceIsWrongTheSameWay: when every device
// read is under a facts key the command was not given, or has no row that
// opens, the fleet's own advice says which setting to check, since every
// device failing alike is the command's keys far more likely than every
// device's history; one device of another state and it says nothing.
func TestInspectAll_SaysWhenEveryDeviceIsWrongTheSameWay(t *testing.T) {
	ctx := context.Background()
	h := newHistory(t, onSQLite)
	web2 := h.client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(ctx)
	gatherRotationHistory(t, entstore.New(h.client, h.old, factstest.Envelope()), web2.DeviceID)
	for store, want := range map[string]string{"third": "wrong PLEIADES_FACTS_DIGEST_KEY", "other master key": "wrong MASTER_ENCRYPTION_KEY", "": ""} {
		report, err := h.store(store).InspectAll(ctx, entstore.InspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if (want == "" && report.Advice != "") || !strings.Contains(report.Advice, want) || report.Scanned != 2 {
			t.Errorf("store %q: the fleet's advice is %q, want it to say %q", store, report.Advice, want)
		}
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
	for _, err := range []error{entstore.ErrRekeyRefused, entstore.ErrKeysChanged, entstore.ErrNotThisDevices} {
		cause, ok := entstore.Unavailable(err)
		if !ok || !strings.Contains(cause, "controller facts inspect --device") || strings.Contains(cause, err.Error()) {
			t.Errorf("Unavailable(%v) = %q; want the inspect command named", err, cause)
		}
	}
	if cause, ok := entstore.Unavailable(facts.ErrNoKeys); !ok || !strings.Contains(cause, "PLEIADES_FACTS_DIGEST_KEY") {
		t.Errorf("Unavailable(ErrNoKeys) = %q, %v", cause, ok)
	}
	if cause, ok := entstore.Unavailable(errors.New("database is locked")); ok || cause != "" {
		t.Errorf("a fault reads as lasting: %q", cause)
	}
}

// TestNameOf_ResolvesTheIDTheLogNames: a device's ID gives its inventory
// name, and an ID no device has is ErrUnknownDevice.
func TestNameOf_ResolvesTheIDTheLogNames(t *testing.T) {
	h := newHistory(t, onSQLite)
	if name, err := h.store("").NameOf(context.Background(), h.device); err != nil || name != "web1" {
		t.Errorf("NameOf(web1's ID) = %q, %v", name, err)
	}
	if _, err := h.store("").NameOf(context.Background(), "no-such-id"); !errors.Is(err, entstore.ErrUnknownDevice) {
		t.Errorf("an unknown ID: %v, want ErrUnknownDevice", err)
	}
}
