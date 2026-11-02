// Tests of predictMade's fallback, which no registered check reaches: each
// refuses a call naming none of its path parameters before it returns.
package file

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// statsOnly keeps the stats a check sets.
type statsOnly map[string]any

func (s statsOnly) InjectSecrets() map[string]string { return nil }
func (s statsOnly) SetStat(k string, v any) error    { s[k] = v; return nil }
func (s statsOnly) EmitFact(k string, v any) error   { s[k] = v; return nil }

// wanting is statsOnly in a check that keeps predictions.
type wanting struct{ statsOnly }

// Predictions returns an empty snapshot.
func (wanting) Predictions() *wire.Predictions { return &wire.Predictions{} }

// TestPredictMade_NamingNoPathLeavesTheDeviceUnknown: a check that succeeded
// without any of the named parameters made a path nobody can name, so the
// device's predictions become unknown rather than missing it.
func TestPredictMade_NamingNoPathLeavesTheDeviceUnknown(t *testing.T) {
	check := predictMade("file.test", func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}, "path", "dest")
	rc := wanting{statsOnly{}}
	if _, err := check(context.Background(), rc, nil, map[string]any{"path": ""}); err != nil {
		t.Fatal(err)
	}
	if rc.statsOnly[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("recorded %#v, want unknown", rc.statsOnly[sdk.StatPredictedPaths])
	}
}

// TestPredictMade_ADeviceItCannotReachLeavesTheDeviceUnknown: a path that
// cannot be located, because the device cannot be reached to read it, is not
// recorded where it might resolve.
func TestPredictMade_ADeviceItCannotReachLeavesTheDeviceUnknown(t *testing.T) {
	check := predictMade("file.test", func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}, "path")
	rc := wanting{statsOnly{}}
	if _, err := check(context.Background(), rc, nil, map[string]any{"path": "/x"}); err != nil {
		t.Fatal(err)
	}
	if rc.statsOnly[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("recorded %#v, want unknown", rc.statsOnly[sdk.StatPredictedPaths])
	}
}
