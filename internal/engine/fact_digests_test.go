// Tests that a raw digest a method hands over never outlives the method
// (fact_digests.go): through a real run, the result every later reader sees
// (report, register) and what the fact store is handed hold the keyed digest
// on the Crawl tier, a sealed one a Runner publishes, or nothing, never the
// raw value, on success and on failure.
package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// digestRaw is the SHA-256 of "pin1234\n" (printf 'pin1234\n' | sha256sum).
const digestRaw = "3f773b3737a2f378fa4589ecacc53598168149f40fa0294c8580ee8fa4455b96"

// registerDigestMethod registers enginedigest.read, which hands over a raw
// digest in a fact and in an ordinary stat, and fails after doing so when its
// param fail is set.
func registerDigestMethod(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	read := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		if err := rc.EmitFact("file.x", map[string]any{"state": "present", "sha256_raw": digestRaw}); err != nil {
			return collection.Result{}, err
		}
		if err := rc.SetStat("probe", map[string]any{"sha256_raw": digestRaw}); err != nil {
			return collection.Result{}, err
		}
		if params["fail"] == true {
			return collection.Result{}, errors.New("refused")
		}
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "enginedigest.read", Invoke: read, Check: read,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"},
			Facts: []collection.FactSpec{{Key: "file.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a file"}}}}); err != nil {
		t.Fatal(err)
	}
}

// runDigests runs enginedigest.read once with the given options, returning
// the run's result and what the store was handed.
func runDigests(t *testing.T, fail bool, actionOpts []engine.CollectionActionExecutorOption, opts ...engine.ExecutorOption) (engine.RunResult, []facts.Entry) {
	t.Helper()
	sink := &factSink{got: map[string][]facts.Entry{}}
	actions := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext, actionOpts...)
	x := engine.NewExecutor(factFleet(), actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		append(opts, engine.WithFactRecorder(sink))...)
	params := `{"target": "fleet"}`
	if fail {
		params = `{"target": "fleet", "fail": true}`
	}
	result, _ := x.Run(context.Background(), buildDAG(t, `{"id": "d", "tasks": [{"name": "r", "register": "r", "enginedigest.read": `+params+`}]}`))
	return result, sink.got["dev-1"]
}

// holdsRaw reports whether anything in v, as JSON, holds the raw digest.
func holdsRaw(t *testing.T, v any) bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(b, []byte(digestRaw))
}

func TestFactDigests_TheCrawlTierKeysEveryRawDigest(t *testing.T) {
	registerDigestMethod(t)
	keyer, err := facts.NewKeyer(bytes.Repeat([]byte{9}, 32), "project")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := keyer.Seal("dev-1", "file.x", digestRaw)
	result, stored := runDigests(t, false, []engine.CollectionActionExecutorOption{engine.WithFactDigestKeyer(keyer)})
	if holdsRaw(t, result) || holdsRaw(t, stored) {
		t.Fatal("the raw digest outlived the method")
	}
	file, _ := result.Nodes[0].Stats["file.x"].(map[string]any)
	if file["hmac_sha256"] != want || file["hmac_key"] != keyer.ID() || file["state"] != "present" {
		t.Errorf("the result holds %v", file)
	}
	if len(stored) != 1 || stored[0].Value.(map[string]any)["hmac_sha256"] != want {
		t.Errorf("the store was handed %+v", stored)
	}
	// A failed task's stats are shown too, and hold no raw digest either.
	failed, _ := runDigests(t, true, []engine.CollectionActionExecutorOption{engine.WithFactDigestKeyer(keyer)})
	if holdsRaw(t, failed) || failed.Nodes[0].Err == nil {
		t.Errorf("a failed task: %+v", failed.Nodes)
	}
}

func TestFactDigests_ARunnerSealsThemToTheController(t *testing.T) {
	registerDigestMethod(t)
	priv, err := sealed.DeriveKey(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	seal := engine.WithFactDigestSealer(func(deviceID, factKey, raw string) (string, error) {
		plaintext, err := facts.SealPlaintext("job-1", deviceID, factKey, raw)
		if err != nil {
			return "", err
		}
		file, err := sealed.SealToKey(priv.Public(), sealed.PurposeFactDigest, plaintext)
		return string(file), err
	})
	result, stored := runDigests(t, false, []engine.CollectionActionExecutorOption{seal}, engine.WithSealedFactDigests())
	if holdsRaw(t, result) || holdsRaw(t, stored) {
		t.Fatal("the raw digest outlived the method")
	}
	if len(stored) != 1 {
		t.Fatalf("stored %+v", stored)
	}
	text, _ := stored[0].Value.(map[string]any)["sha256_sealed"].(string)
	file, err := sealed.Parse([]byte(text))
	if err != nil {
		t.Fatalf("the published value holds no sealed digest: %v", err)
	}
	plaintext, err := file.OpenWithKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := facts.OpenPlaintext(plaintext, "job-1", "dev-1", "file.x"); err != nil || raw != digestRaw {
		t.Errorf("the Controller would open %q, %v", raw, err)
	}
	// A run whose recorder does not publish to the Controller stores no
	// sealed digest, even from a sealing executor.
	_, local := runDigests(t, false, []engine.CollectionActionExecutorOption{seal})
	if len(local) != 1 || local[0].Value.(map[string]any)["sha256_sealed"] != nil {
		t.Errorf("a store not expecting sealed digests was handed %+v", local)
	}
}

func TestFactDigests_AnExecutorToldNeitherDropsThem(t *testing.T) {
	registerDigestMethod(t)
	result, stored := runDigests(t, false, nil)
	if holdsRaw(t, result) || holdsRaw(t, stored) {
		t.Fatal("the raw digest outlived the method")
	}
	file, _ := result.Nodes[0].Stats["file.x"].(map[string]any)
	if file["state"] != "present" || file["hmac_sha256"] != nil || file["sha256_sealed"] != nil {
		t.Errorf("the result holds %v", file)
	}
	// The loss is said, by fact and reason, never by value.
	warnings := strings.Join(toStrings(result.Nodes[0].Stats[sdk.StatWarnings]), "\n")
	if !strings.Contains(warnings, "the digest of file.x was not kept") || !strings.Contains(warnings, "the digest of probe was not kept") {
		t.Errorf("warnings = %q, want each lost digest named", warnings)
	}
	// A sealer that fails drops the digest too.
	failing := engine.WithFactDigestSealer(func(string, string, string) (string, error) { return "", errors.New("no key") })
	result, stored = runDigests(t, false, []engine.CollectionActionExecutorOption{failing}, engine.WithSealedFactDigests())
	if holdsRaw(t, result) || holdsRaw(t, stored) || strings.Contains(string(mustJSON(t, stored)), "sha256_sealed") {
		t.Errorf("a failed seal left %+v", stored)
	}
}

// mustJSON is v as JSON.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFactDigests_AChildsRawDigestIsHandledToo(t *testing.T) {
	registerDigestMethod(t)
	// A Runner runs each method in a child process and hands its stats back
	// through an invoker; those must be keyed, sealed or dropped as well.
	invoker := engine.WithCollectionInvoker(func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]any, error) {
		return collection.Result{}, map[string]any{"file.x": map[string]any{"state": "present", "sha256_raw": digestRaw}, sdk.StatFactKeys: []any{"file.x"}}, nil
	})
	keyer, _ := facts.NewKeyer(bytes.Repeat([]byte{9}, 32), "project")
	want, _ := keyer.Seal("dev-1", "file.x", digestRaw)
	result, stored := runDigests(t, false, []engine.CollectionActionExecutorOption{invoker, engine.WithFactDigestKeyer(keyer)})
	if holdsRaw(t, result) || holdsRaw(t, stored) {
		t.Fatal("a child's raw digest outlived the method")
	}
	if file, _ := result.Nodes[0].Stats["file.x"].(map[string]any); file["hmac_sha256"] != want {
		t.Errorf("a child's fact = %v", file)
	}
	failing := engine.WithCollectionInvoker(func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]any, error) {
		return collection.Result{}, map[string]any{"probe": map[string]any{"sha256_raw": digestRaw}}, errors.New("refused")
	})
	failed, _ := runDigests(t, true, []engine.CollectionActionExecutorOption{failing})
	if holdsRaw(t, failed) {
		t.Error("a failed child's raw digest reached the result")
	}
}

// toStrings is a warnings stat as strings.
func toStrings(v any) []string {
	switch w := v.(type) {
	case []string:
		return w
	case []any:
		out := make([]string, 0, len(w))
		for _, item := range w {
			s, _ := item.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

func TestFactDigests_ARawDigestDeeperInAValueIsDropped(t *testing.T) {
	registerDigestMethod(t)
	// A child (or an external Collection) nesting the field: neither keyed
	// nor shown, at any depth.
	nested := engine.WithCollectionInvoker(func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]any, error) {
		return collection.Result{}, map[string]any{
			"file.x":         map[string]any{"inner": map[string]any{"sha256_raw": digestRaw}, "list": []any{map[string]any{"sha256_raw": digestRaw}}},
			"private":        map[string]any{"world_readable": false, "sha256": digestRaw, "size": 7},
			sdk.StatFactKeys: []any{"file.x"},
		}, nil
	})
	keyer, _ := facts.NewKeyer(bytes.Repeat([]byte{9}, 32), "project")
	result, stored := runDigests(t, false, []engine.CollectionActionExecutorOption{nested, engine.WithFactDigestKeyer(keyer)})
	if holdsRaw(t, result) || holdsRaw(t, stored) {
		t.Fatalf("a nested raw digest, or a private file's plain one, survived: %v", result.Nodes[0].Stats)
	}
}

// rawExecutor is an ActionExecutor outside the collection executor whose
// result holds a raw digest, as no built-in one does today.
type rawExecutor struct{}

func (rawExecutor) Execute(context.Context, *engine.Task, inventory.InventoryItem) (engine.ActionResult, error) {
	return engine.ActionResult{Stats: map[string]any{"probe": map[string]any{"sha256_raw": digestRaw, "sha256_sealed": "x"}}}, nil
}

func TestFactDigests_NoExecutorsResultKeepsARawDigest(t *testing.T) {
	x := engine.NewExecutor(factFleet(), rawExecutor{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err := x.Run(context.Background(), buildDAG(t, `{"id": "d", "tasks": [{"name": "r", "register": "r", "anything.at.all": {"target": "fleet"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if holdsRaw(t, result) || strings.Contains(string(mustJSON(t, result)), "sha256_sealed") {
		t.Errorf("an executor's raw or unexpected sealed digest reached the result: %v", result.Nodes[0].Stats)
	}
}
