// Tests that the vars property a device brings (group_vars, host_vars)
// travels on a dispatch to a Runner for every device type, and never with a
// secret.
package native

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestDispatchedDevice_VarsTravelWithoutSecrets builds a real device with
// variables, takes the properties the Controller's dispatch carries
// (record.Dispatched), sends them through the wire's own JSON, and rebuilds
// the device as the Runner does: the variables arrive, and the ones named or
// shaped like a secret do not leave the Controller.
func TestDispatchedDevice_VarsTravelWithoutSecrets(t *testing.T) {
	for _, typ := range []string{"linux_server", "generic_http"} {
		t.Run(typ, func(t *testing.T) {
			ctor, ok := record.LookupType(typ)
			if !ok {
				t.Fatalf("device type %s is not registered", typ)
			}
			props := map[string]inventory.PropertyValue{
				"host": "10.0.0.1",
				inventory.VarsProperty: map[string]any{
					"site":         "lab",
					"ntp_servers":  []any{"10.0.0.2"},
					"api_password": "hunter2",
					"banner":       "AKIAIOSFODNN7EXAMPLE",
				},
			}
			if typ == "generic_http" {
				props["base_url"] = "https://api.example.com/v2"
			}
			item, err := ctor(record.Record{ID: "d1", Name: "dev1", Type: typ, Properties: props, State: inventory.StateActive})
			if err != nil {
				t.Fatal(err)
			}

			gotType, sent := record.Dispatched(item)
			payload := wire.DispatchPayload{DeviceID: "d1", DeviceName: "dev1", DeviceType: gotType, DeviceProperties: sent}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var arrived wire.DispatchPayload
			if err := json.Unmarshal(encoded, &arrived); err != nil {
				t.Fatal(err)
			}

			rebuilt, err := dispatchedDevice(arrived)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"site": "lab", "ntp_servers": []any{"10.0.0.2"}}
			if got := rebuilt.Properties().Raw()[inventory.VarsProperty]; !reflect.DeepEqual(got, want) {
				t.Errorf("the Runner's device has vars %v, want %v", got, want)
			}
		})
	}
}
