// Package engine_test: tests that a run's method settings fill the parameters
// a task leaves out, in process and through a Runner's invoker.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerSettingMethod registers a method declaring the run setting
// "behavior" and the ordinary parameter "path", recording the params each
// call is handed.
func registerSettingMethod(t *testing.T, suffix string, got *map[string]any) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	name := "enginetest." + suffix
	err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			Doc: collection.Doc{Params: []collection.Param{
				{Name: "behavior", Type: "string", Default: "refuse", Choices: []string{"refuse", "warn"}, RunSetting: true},
				{Name: "path", Type: "string"},
			}}},
		Invoke: func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			*got = params
			return collection.Result{}, nil
		},
	})
	if err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}

// TestWithRunSettings_FillsWhatTheTaskLeftOut proves a run setting reaches the
// method as an ordinary parameter, in process and through a Runner's
// invoker, that the task's own value wins, that a parameter which is not a
// run setting is never filled, and that the task itself is left unchanged.
func TestWithRunSettings_FillsWhatTheTaskLeftOut(t *testing.T) {
	var got map[string]any
	name := registerSettingMethod(t, "run_settings", &got)
	settings := collection.RunSettings{name + ".behavior": "warn", name + ".path": "/etc"}

	var invoked map[string]any
	invoker := engine.WithCollectionInvoker(func(_ context.Context, _ collection.Descriptor, _ inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, map[string]any, error) {
		invoked = params
		return collection.Result{}, nil, nil
	})
	inProcess := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithRunSettings(settings))
	onARunner := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithRunSettings(settings), invoker)

	task := &engine.Task{FQCN: name, Params: map[string]any{"path": "/tmp/a"}}
	if _, err := inProcess.Execute(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	if got["behavior"] != "warn" || got["path"] != "/tmp/a" {
		t.Errorf("in process the method got %v, want behavior filled and the task's path", got)
	}
	if _, err := onARunner.Execute(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	if invoked["behavior"] != "warn" || invoked["path"] != "/tmp/a" {
		t.Errorf("through the invoker the method got %v, want behavior filled and the task's path", invoked)
	}
	if _, filled := task.Params["behavior"]; filled {
		t.Errorf("the task's own params were changed: %v", task.Params)
	}

	own := &engine.Task{FQCN: name, Params: map[string]any{"behavior": "refuse"}}
	if _, err := inProcess.Execute(context.Background(), own, nil); err != nil {
		t.Fatal(err)
	}
	if got["behavior"] != "refuse" {
		t.Errorf("a task's own value became %v, want it kept", got["behavior"])
	}
}
