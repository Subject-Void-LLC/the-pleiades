// Tests for the ai.model methods against a real HTTP server holding an
// Ollama host's models on disk and in memory.
package model_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai/model"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// undo is the inverse a run recorded, or nil.
func undo(t *testing.T, rc *aitest.Context) map[string]any {
	t.Helper()
	inv, _ := rc.Stats[sdk.StatInverse].(map[string]any)
	return inv
}

// TestLoad_OneResidentAtATime: a load unloads every other model first, so
// the host never holds two, and its undo loads the one that was there.
func TestLoad_OneResidentAtATime(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest", "b:latest", "c:latest")
	host.Memory = []string{"a:latest", "c:latest"}
	dev := aitest.Local(t, "ws", host, true)
	rc := aitest.NewContext()
	res, err := model.Load(context.Background(), rc, dev, map[string]any{"model": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, memory := host.State(); !res.Changed || !slices.Equal(memory, []string{"b:latest"}) {
		t.Fatalf("memory %v", memory)
	}
	reqs := host.Requests()
	loadAt := slices.Index(reqs, "POST /api/generate")
	if loadAt < 0 || strings.Count(strings.Join(reqs, "\n"), "POST /api/generate") != 3 {
		t.Fatalf("requests %v", reqs)
	}
	if inv := undo(t, rc); inv["fqcn"] != "ai.model.load" || inv["params"].(map[string]any)["model"] != "a:latest" {
		t.Errorf("undo %v", inv)
	}
	again := aitest.NewContext()
	if res, err := model.Load(context.Background(), again, dev, map[string]any{"model": "b:latest"}); err != nil || res.Changed || undo(t, again) != nil {
		t.Errorf("a second load: %+v %v %v", res, err, undo(t, again))
	}
}

// TestModels_RefuseAnUnguardedHost: every changing method refuses a host
// whose record does not declare the guard, before any change; listing
// needs no guard.
func TestModels_RefuseAnUnguardedHost(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest")
	dev := aitest.Local(t, "ws", host, false)
	for _, fqcn := range []string{"ai.model.load", "ai.model.unload", "ai.model.pull", "ai.model.remove"} {
		desc, _ := collection.Lookup(fqcn)
		if _, err := desc.Invoke(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "a"}); err == nil || !strings.Contains(err.Error(), "ai_host_guard") {
			t.Errorf("%s: %v", fqcn, err)
		}
	}
	if len(host.Requests()) != 0 {
		t.Errorf("an unguarded host was reached: %v", host.Requests())
	}
	rc := aitest.NewContext()
	if _, err := model.List(context.Background(), rc, dev, nil); err != nil {
		t.Fatal(err)
	}
	if models, _ := rc.Stats["models"].([]any); len(models) != 1 {
		t.Errorf("models %v", rc.Stats["models"])
	}
}

// TestPullAndRemove: a pull adds the model and records its removal; a
// pull of a model already there changes nothing and records no removal,
// so undoing it never removes what was there; a check changes nothing.
func TestPullAndRemove(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest")
	dev := aitest.Local(t, "ws", host, true)
	desc, _ := collection.Lookup("ai.model.pull")
	check := aitest.NewContext()
	if res, err := desc.Check(context.Background(), check, dev, map[string]any{"model": "b"}); err != nil || !res.Changed {
		t.Fatalf("check: %+v %v", res, err)
	}
	if disk, _ := host.State(); len(disk) != 1 {
		t.Fatalf("a check pulled: %v", disk)
	}
	rc := aitest.NewContext()
	if res, err := model.Pull(context.Background(), rc, dev, map[string]any{"model": "b"}); err != nil || !res.Changed {
		t.Fatalf("pull: %+v %v", res, err)
	}
	if inv := undo(t, rc); inv["fqcn"] != "ai.model.remove" {
		t.Errorf("undo %v", inv)
	}
	present := aitest.NewContext()
	if res, err := model.Pull(context.Background(), present, dev, map[string]any{"model": "a"}); err != nil || res.Changed || undo(t, present) != nil {
		t.Errorf("a model already there: %+v %v %v", res, err, undo(t, present))
	}
	host.Memory = []string{"b:latest"}
	removed := aitest.NewContext()
	if _, err := model.Remove(context.Background(), removed, dev, map[string]any{"model": "b"}); err != nil {
		t.Fatal(err)
	}
	if disk, memory := host.State(); slices.Contains(disk, "b:latest") || len(memory) != 0 {
		t.Errorf("after remove: disk %v memory %v", disk, memory)
	}
	if inv := undo(t, removed); inv["fqcn"] != "ai.model.pull" {
		t.Errorf("undo %v", inv)
	}
}

// TestLoad_ModelNotOnTheHost: loading a model the host does not have is
// refused naming the pull, and nothing is unloaded first.
func TestLoad_ModelNotOnTheHost(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest")
	host.Memory = []string{"a:latest"}
	dev := aitest.Local(t, "ws", host, true)
	if _, err := model.Load(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "zz"}); err == nil || !strings.Contains(err.Error(), "ai.model.pull") {
		t.Errorf("err %v", err)
	}
	if _, memory := host.State(); !slices.Equal(memory, []string{"a:latest"}) {
		t.Errorf("memory %v", memory)
	}
}

// TestNormalize: a name with no tag means :latest, and a registry's port
// is not a tag.
func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"qwen3": "qwen3:latest", "qwen3:14b": "qwen3:14b", "reg.example:5000/team/m": "reg.example:5000/team/m:latest", "reg.example:5000/team/m:1": "reg.example:5000/team/m:1",
	} {
		if got := model.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLoadAndRemove_AFailureAfterAnUnloadPutsTheHostBack: a load that
// unloads the resident model and then fails to load, and a remove that
// unloads and then fails to delete, leave the host holding what it held;
// the failed task's undo is dropped, so nothing else would put it back.
func TestLoadAndRemove_AFailureAfterAnUnloadPutsTheHostBack(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest", "b:latest")
	host.Memory = []string{"a:latest"}
	host.FailLoad = "b:latest"
	dev := aitest.Local(t, "ws", host, true)
	rc := aitest.NewContext()
	_, err := model.Load(context.Background(), rc, dev, map[string]any{"model": "b"})
	if err == nil || !strings.Contains(err.Error(), "loaded again") {
		t.Fatalf("a load the host refused = %v", err)
	}
	if _, memory := host.State(); !slices.Equal(memory, []string{"a:latest"}) {
		t.Errorf("after the failed load, memory %v; want a:latest back", memory)
	}

	host.FailDelete = true
	if _, err := model.Remove(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "a"}); err == nil || !strings.Contains(err.Error(), "loaded again") {
		t.Fatalf("a delete the host refused = %v", err)
	}
	if disk, memory := host.State(); !slices.Equal(memory, []string{"a:latest"}) || !slices.Contains(disk, "a:latest") {
		t.Errorf("after the failed remove, disk %v memory %v", disk, memory)
	}
}

// TestLoad_ACheckOfAModelNotYetPulledCannotBeChecked: a check of a load
// whose model is not on the host yet is reported as unable to be checked,
// not as an error, since an earlier task of the run may pull it.
func TestLoad_ACheckOfAModelNotYetPulledCannotBeChecked(t *testing.T) {
	host := aitest.NewOllama(t, "a:latest")
	dev := aitest.Local(t, "ws", host, true)
	desc, _ := collection.Lookup("ai.model.load")
	_, err := desc.Check(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "missing"})
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Fatalf("check = %v, want it unable to be checked", err)
	}
}
