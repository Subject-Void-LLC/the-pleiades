package sdk_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// inverseOf reads back the recorded inverse stat as the map a rollback
// engine would later have to parse, failing if the shape is not what that
// reader expects.
func inverseOf(t *testing.T, c *recordingContext) map[string]any {
	t.Helper()
	raw, ok := c.stats[sdk.StatInverse]
	if !ok {
		t.Fatalf("no %q stat was recorded", sdk.StatInverse)
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("the %q stat is %T, want a map", sdk.StatInverse, raw)
	}
	return record
}

// TestRecordInverse covers the one function in this package that writes
// the undo instruction a rollback would run.
//
// Nothing performs a rollback yet, which is exactly why the shape has to
// be pinned now. Only the forward run can capture the values an undo
// needs, so a run that records them in the wrong shape has destroyed
// information that cannot be recovered later, and the failure surfaces
// whenever rollback is eventually built, against journals written months
// earlier by code nobody is looking at any more.
func TestRecordInverse(t *testing.T) {
	t.Run("the full record", func(t *testing.T) {
		rc := newRecordingContext()
		err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        "file.permissions",
			Params:      map[string]any{"path": "/etc/app.conf", "mode": "0600"},
			Description: "restore the mode /etc/app.conf had before this run",
		})
		if err != nil {
			t.Fatalf("RecordInverse: %v", err)
		}

		record := inverseOf(t, rc)
		if got := record["fqcn"]; got != "file.permissions" {
			t.Errorf("fqcn = %v, want file.permissions", got)
		}
		params, ok := record["params"].(map[string]any)
		if !ok {
			t.Fatalf("params is %T, want a map", record["params"])
		}
		if params["path"] != "/etc/app.conf" || params["mode"] != "0600" {
			t.Errorf("params = %v, want the resolved path and mode", params)
		}
		if got := record["description"]; got != "restore the mode /etc/app.conf had before this run" {
			t.Errorf("description = %v, want the sentence the caller supplied", got)
		}
	})

	t.Run("no description leaves the key out entirely", func(t *testing.T) {
		// Absent rather than empty, so a reader rendering a rollback plan
		// can tell "this instruction has no explanation" from "this
		// instruction explains itself as the empty string."
		rc := newRecordingContext()
		if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: "svc.systemd.stop", Params: map[string]any{"name": "nginx"}}); err != nil {
			t.Fatalf("RecordInverse: %v", err)
		}
		record := inverseOf(t, rc)
		if _, present := record["description"]; present {
			t.Errorf("an absent description was recorded anyway: %v", record)
		}
	})

	t.Run("nil params become an empty map, not a nil one", func(t *testing.T) {
		// Asserted through JSON rather than with a type assertion, and
		// the difference matters: a nil map[string]any satisfies
		// `.(map[string]any)` and reports len 0, so both of the obvious
		// checks pass whether or not the normalization happened. The only
		// place the distinction is observable is where it does damage,
		// which is the encoded journal a rollback engine reads: a nil map
		// encodes as null and an empty one as {}, and the contract says
		// params is an object.
		rc := newRecordingContext()
		if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: "svc.systemd.daemon_reload"}); err != nil {
			t.Fatalf("RecordInverse: %v", err)
		}

		encoded, err := json.Marshal(inverseOf(t, rc))
		if err != nil {
			t.Fatalf("encoding the recorded inverse: %v", err)
		}
		if strings.Contains(string(encoded), `"params":null`) {
			t.Errorf("the recorded inverse encodes as %s, want params as an object rather than null", encoded)
		}
		if !strings.Contains(string(encoded), `"params":{}`) {
			t.Errorf("the recorded inverse encodes as %s, want an empty params object", encoded)
		}
	})

	t.Run("an inverse naming no method is refused", func(t *testing.T) {
		// Refused here, where the method that produced it is still on the
		// stack, rather than written and discovered by a rollback engine
		// that can only skip it or fail.
		rc := newRecordingContext()
		err := sdk.RecordInverse(rc, sdk.Inverse{Params: map[string]any{"path": "/etc/app.conf"}})
		if err == nil {
			t.Fatal("an inverse with no FQCN was accepted")
		}
		if _, recorded := rc.stats[sdk.StatInverse]; recorded {
			t.Error("a refused inverse was recorded anyway")
		}
	})

	t.Run("a failing SetStat is reported", func(t *testing.T) {
		// The caller has to hear about this. A method that swallowed it
		// would report a successful, apparently reversible run whose undo
		// instruction was never written down.
		boom := errors.New("stat store unavailable")
		rc := newRecordingContext()
		rc.statErr = boom

		err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: "file.remove", Params: map[string]any{"path": "/tmp/x"}})
		if !errors.Is(err, boom) {
			t.Errorf("error = %v, want the underlying SetStat failure", err)
		}
	})
}
