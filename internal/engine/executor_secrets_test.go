package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
)

// perDeviceTokenExecutor is a test-only ActionExecutor for fqcn
// "token_source": it returns a distinct value per resolved device (or one
// value for a controller-side call with no device), unlike the builtin
// "noop" action, whose Stats always echo the same task.Params regardless
// of which device it is running against. Every other fqcn is a no-op
// success, so a DAG can mix a token_source task with an ordinary "noop"
// task (e.g. one carrying secret_mask) in the same test.
type perDeviceTokenExecutor struct{}

func (perDeviceTokenExecutor) Execute(_ context.Context, task *engine.Task, device inventory.InventoryItem) (engine.ActionResult, error) {
	if task.FQCN != "token_source" {
		return engine.ActionResult{}, nil
	}
	name := "controller"
	if device != nil {
		name = device.Name()
	}
	return engine.ActionResult{Stats: map[string]interface{}{"token": "token-for-" + name}}, nil
}

// TestExecutor_RegisterMaskMaskedInLaterPublishedEvent proves the core
// requirement behind register_mask: a value marked secret is scrubbed out
// of a *later, unrelated* task's own output, not just wherever it was
// first produced. "mark-secret" registers a value and marks it secret via
// register_mask; "leak-secret" then uses that exact value as its own
// target, which fails to resolve (matching no inventory host or tag) and
// would otherwise embed the raw secret verbatim in its own error and
// published event message.
func TestExecutor_RegisterMaskMaskedInLaterPublishedEvent(t *testing.T) {
	const secret = "sup3r-secret-password"

	bus := event.NewInProcessBus()

	var mu sync.Mutex
	var leakMessage string
	var sawLeakEvent bool
	if err := bus.Subscribe(context.Background(), "pleiades.events.>", func(e event.Event) error {
		var payload struct {
			Task string `json:"task"`
			Data struct {
				Message string `json:"message"`
			} `json:"event_data"`
		}
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			return nil
		}
		if payload.Task != "leak-secret" {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		leakMessage = payload.Data.Message
		sawLeakEvent = true
		return nil
	}); err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	dag := buildDAG(t, fmt.Sprintf(`{
		"id": "secret-fields-leak",
		"tasks": [
			{"name": "mark-secret", "fqcn": "noop", "register": "creds", "register_mask": ["password"], "params": {"password": %q}},
			{"name": "leak-secret", "fqcn": "noop", "params": {"target": %q}}
		]
	}`, secret, secret))

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), bus, engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the second node: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 node results, got %d: %+v", len(result.Nodes), result.Nodes)
	}
	if result.Nodes[0].Err != nil {
		t.Fatalf("expected mark-secret to succeed, got: %v", result.Nodes[0].Err)
	}
	if result.Nodes[1].Err == nil {
		t.Fatalf("expected leak-secret to fail (unresolvable target), got success")
	}

	found := false
	for _, s := range result.Secrets {
		if s == secret {
			found = true
		}
	}
	if !found {
		t.Errorf("expected result.Secrets to contain the marked value, got: %v", result.Secrets)
	}

	// NodeResult.Err is never mutated: it is the caller's job to mask
	// using RunResult.Secrets, not Executor's.
	if !strings.Contains(result.Nodes[1].Err.Error(), secret) {
		t.Errorf("expected NodeResult.Err to still carry the raw, unmasked value, got: %v", result.Nodes[1].Err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		ok := sawLeakEvent
		mu.Unlock()
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawLeakEvent {
		t.Fatalf("expected a published event for leak-secret")
	}
	if strings.Contains(leakMessage, secret) {
		t.Errorf("expected the published event message to be masked, got: %q", leakMessage)
	}
	if !strings.Contains(leakMessage, "********") {
		t.Errorf("expected the published event message to contain the mask placeholder, got: %q", leakMessage)
	}
}

// TestExecutor_SecretMaskAppliesAcrossAllDevices proves secret_mask, once
// applied, protects the marked field's value under *every* device that
// register produced, not just one: "precheck" fans out across two
// devices, each producing a distinct token via perDeviceTokenExecutor;
// "mark-secret" (a later, controller-side task) marks that field secret
// after the fact via secret_mask.
func TestExecutor_SecretMaskAppliesAcrossAllDevices(t *testing.T) {
	devices := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "host-a", StubName: "host-a", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "host-b", StubName: "host-b", StubState: inventory.StateActive},
	}
	resolver := mapResolver{"all": devices}

	dag := buildDAG(t, `{
		"id": "mask-across-devices",
		"tasks": [
			{"name": "precheck", "fqcn": "token_source", "register": "precheck", "params": {"target": "all"}},
			{"name": "mark-secret", "fqcn": "noop", "secret_mask": {"register": "precheck", "fields": ["token"]}}
		]
	}`)

	x := engine.NewExecutor(resolver, perDeviceTokenExecutor{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	want := map[string]bool{"token-for-host-a": false, "token-for-host-b": false}
	for _, s := range result.Secrets {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for token, seen := range want {
		if !seen {
			t.Errorf("expected result.Secrets to contain %q, got: %v", token, result.Secrets)
		}
	}
}

// TestExecutor_SecretMaskUnknownRegisterIsError confirms a secret_mask
// referencing a register nothing ever produces fails loudly, naming the
// register, rather than silently masking nothing.
func TestExecutor_SecretMaskUnknownRegisterIsError(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "unknown-register",
		"tasks": [{"name": "mark", "fqcn": "noop", "secret_mask": {"register": "nope", "fields": ["x"]}}]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the node: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the node to fail, got %+v", result.Nodes)
	}
	if !strings.Contains(result.Nodes[0].Err.Error(), "nope") {
		t.Errorf("expected the error to name the unknown register, got: %v", result.Nodes[0].Err)
	}
}

// TestExecutor_RegisterMaskRejectsShortOrNonStringValue confirms a value
// that is not safe to substring-mask (not a string, or too short) fails
// the node loudly, and that the failure never leaks the offending value
// itself into the error message.
func TestExecutor_RegisterMaskRejectsShortOrNonStringValue(t *testing.T) {
	cases := []struct {
		name      string
		paramsRaw string
		rawValue  string
	}{
		{name: "bool value", paramsRaw: `{"flag": true}`, rawValue: "true"},
		{name: "short string", paramsRaw: `{"pin": "1234"}`, rawValue: "1234"},
		{name: "empty string", paramsRaw: `{"empty": ""}`, rawValue: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := "flag"
			switch tc.name {
			case "short string":
				field = "pin"
			case "empty string":
				field = "empty"
			}

			dag := buildDAG(t, fmt.Sprintf(`{
				"id": "invalid-secret-field",
				"tasks": [{"name": "mark", "fqcn": "noop", "register_mask": [%q], "params": %s}]
			}`, field, tc.paramsRaw))

			x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatalf("Run itself should not fail, only the node: %v", err)
			}
			if !result.HasErrors() {
				t.Fatalf("expected the node to fail, got %+v", result.Nodes)
			}
			msg := result.Nodes[0].Err.Error()
			if !strings.Contains(msg, field) {
				t.Errorf("expected the error to name the field %q, got: %v", field, msg)
			}
			if tc.rawValue != "" && strings.Contains(msg, tc.rawValue) {
				t.Errorf("expected the error to never contain the offending value %q, got: %v", tc.rawValue, msg)
			}
		})
	}
}

// TestExecutor_SecretsConcurrentDiscoveryUnderRace confirms the run-scoped
// secret accumulator is safe under the genuine concurrency runOne already
// exercises (device fan-out): every one of N devices marks its own
// distinct token secret at once, and none are lost or corrupted. Run this
// test with -race.
func TestExecutor_SecretsConcurrentDiscoveryUnderRace(t *testing.T) {
	const deviceCount = 8

	devices := make([]inventory.InventoryItem, deviceCount)
	for i := range devices {
		id := inventory.DeviceID("host-" + string(rune('a'+i)))
		devices[i] = &inventorytest.Stub{StubID: id, StubName: string(id), StubState: inventory.StateActive}
	}
	resolver := mapResolver{"all": devices}

	dag := buildDAG(t, `{
		"id": "concurrent-secrets",
		"tasks": [{"name": "collect", "fqcn": "token_source", "register": "out", "register_mask": ["token"], "params": {"target": "all"}}]
	}`)

	x := engine.NewExecutor(resolver, perDeviceTokenExecutor{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), deviceCount)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}
	if len(result.Secrets) != deviceCount {
		t.Fatalf("expected %d distinct secrets, got %d: %v", deviceCount, len(result.Secrets), result.Secrets)
	}
}

// TestExecutor_SetMetadataAppearsInRunResult confirms a "set_metadata"
// task's result lands in RunResult.Metadata, and that an ordinary
// Register'd task (any other fqcn) does not, even though both go through
// the identical Register/Merge path into WorkflowContext.
func TestExecutor_SetMetadataAppearsInRunResult(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "metadata",
		"tasks": [
			{"name": "report", "fqcn": "set_metadata", "register": "summary", "params": {"data": {"devices_patched": 3}}},
			{"name": "other", "fqcn": "noop", "register": "other", "params": {"foo": "bar"}}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	summary, ok := result.Metadata["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result.Metadata[\"summary\"] to be present, got: %+v", result.Metadata)
	}
	stats, ok := summary[""].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a controller-side (empty device ID) entry, got: %+v", summary)
	}
	if _, ok := stats["devices_patched"]; !ok {
		t.Errorf("expected devices_patched to round-trip, got: %+v", stats)
	}

	if _, ok := result.Metadata["other"]; ok {
		t.Errorf("expected an ordinary registered task (fqcn noop) to not appear in Metadata, got: %+v", result.Metadata)
	}
}

// TestExecutor_RegisterMaskNestedPath proves register_mask's dotted-path
// support: "noop" echoes task.Params verbatim into Stats, so a nested
// params.parent.nested_secret value is reachable by register_mask entry
// "parent.nested_secret", exactly the shape secret_fields never supported.
func TestExecutor_RegisterMaskNestedPath(t *testing.T) {
	const secret = "a-very-long-nested-secret-value"

	dag := buildDAG(t, fmt.Sprintf(`{
		"id": "register-mask-nested",
		"tasks": [{
			"name": "mark",
			"fqcn": "noop",
			"register": "creds",
			"register_mask": ["parent.nested_secret"],
			"params": {"parent": {"nested_secret": %q}}
		}]
	}`, secret))

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	found := false
	for _, s := range result.Secrets {
		if s == secret {
			found = true
		}
	}
	if !found {
		t.Errorf("expected result.Secrets to contain the nested value, got: %v", result.Secrets)
	}
}

// TestExecutor_RegisterMaskAbsentPath_IsBenignSkip confirms a register_mask
// path naming a field this task's action never produced is a benign skip,
// not an error, matching the top-level-absent-field behavior secret_fields
// already had.
func TestExecutor_RegisterMaskAbsentPath_IsBenignSkip(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "register-mask-absent",
		"tasks": [{"name": "mark", "fqcn": "noop", "register_mask": ["nope.not_here"], "params": {"other": "value"}}]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected an absent register_mask path to be a benign skip, got %+v", result.Nodes)
	}
}

// TestExecutor_RegisterMaskNonMapIntermediate_Errors confirms a
// register_mask path that tries to descend through a segment which
// resolved to a non-map leaf value fails loudly, naming the path, rather
// than silently treating it as absent.
func TestExecutor_RegisterMaskNonMapIntermediate_Errors(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "register-mask-non-map",
		"tasks": [{"name": "mark", "fqcn": "noop", "register_mask": ["password.oops"], "params": {"password": "a-long-enough-string"}}]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the node: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the node to fail, got %+v", result.Nodes)
	}
	if !strings.Contains(result.Nodes[0].Err.Error(), "password.oops") {
		t.Errorf("expected the error to name the offending path, got: %v", result.Nodes[0].Err)
	}
}

// TestExecutor_RegisterMaskRegisterNamePrefix_Stripped proves a
// register_mask path optionally prefixed with this task's own Register
// name (mirroring when_cel's stat.<register> addressing, and exactly the
// shape a hand-authored runbook naturally reaches for) resolves to the
// identical field a bare, unprefixed path would: Stats has no key named
// after the register itself, so leaving the prefix on would otherwise
// silently resolve to nothing.
func TestExecutor_RegisterMaskRegisterNamePrefix_Stripped(t *testing.T) {
	const secret = "a-long-enough-secret-value"

	dag := buildDAG(t, fmt.Sprintf(`{
		"id": "register-mask-prefix",
		"tasks": [{
			"name": "get config",
			"fqcn": "noop",
			"register": "running_config",
			"register_mask": "running_config.stdout",
			"params": {"stdout": %q}
		}]
	}`, secret))

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	found := false
	for _, s := range result.Secrets {
		if s == secret {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the register-name-prefixed path to still mask the value, got: %v", result.Secrets)
	}
}
