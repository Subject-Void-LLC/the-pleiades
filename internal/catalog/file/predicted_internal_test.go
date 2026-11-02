// Tests of predictMade's fallback, which no registered check reaches: each
// refuses a call naming none of its path parameters before it returns.
package file

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// statsOnly keeps the stats a check sets.
type statsOnly map[string]any

func (s statsOnly) InjectSecrets() map[string]string { return nil }
func (s statsOnly) SetStat(k string, v any) error    { s[k] = v; return nil }
func (s statsOnly) EmitFact(k string, v any) error   { s[k] = v; return nil }

// TestPredictMade_NamingNoPathLeavesTheDeviceUnknown: a check that succeeded
// without any of the named parameters made a path nobody can name, so the
// device's predictions become unknown rather than missing it.
func TestPredictMade_NamingNoPathLeavesTheDeviceUnknown(t *testing.T) {
	check := predictMade(func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}, "path", "dest")
	rc := statsOnly{}
	if _, err := check(context.Background(), rc, nil, map[string]any{"path": ""}); err != nil {
		t.Fatal(err)
	}
	if rc[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("recorded %#v, want unknown", rc[sdk.StatPredictedPaths])
	}
}
