// Tests for the job's own step of the connection persistence setting.
package launch_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestPersistConnections: on unless the field is set, and then only when
// it says on; any other value, including a boolean, reads as off.
func TestPersistConnections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields launch.Fields
		want   bool
	}{
		{"no fields", nil, true},
		{"field unset", launch.Fields{"forks": 5}, true},
		{"on", launch.Fields{launch.PersistConnectionsField: launch.PersistOn}, true},
		{"off", launch.Fields{launch.PersistConnectionsField: launch.PersistOff}, false},
		{"a boolean is not the setting", launch.Fields{launch.PersistConnectionsField: true}, false},
		{"a near miss", launch.Fields{launch.PersistConnectionsField: "On"}, false},
	} {
		if got := launch.PersistConnections(tc.fields); got != tc.want {
			t.Errorf("%s: PersistConnections = %v, want %v", tc.name, got, tc.want)
		}
	}
}
