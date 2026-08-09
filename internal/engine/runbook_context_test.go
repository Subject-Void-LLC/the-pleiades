package engine_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// factReader is the engine-side view of a RunbookContext: everything a
// method emitted. It mirrors engine.FactCollector, redeclared here because
// the test needs to assert on a value the constructor returns as the
// narrower sdk.RunbookContext.
type factReader interface {
	Facts() map[string]interface{}
}

// TestRunbookContext_CollectsFactsAndStats proves both emission paths land
// in the same place. They share storage deliberately: both end up in
// ActionResult.Stats, and keeping two maps would only force the executor to
// merge them back.
func TestRunbookContext_CollectsFactsAndStats(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	if err := rc.EmitFact("os", "IOS-XE"); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}
	if err := rc.SetStat("device_count", 4); err != nil {
		t.Fatalf("SetStat: %v", err)
	}

	facts := rc.(factReader).Facts()
	if facts["os"] != "IOS-XE" {
		t.Errorf("facts[os] = %v, want IOS-XE", facts["os"])
	}
	if facts["device_count"] != 4 {
		t.Errorf("facts[device_count] = %v, want 4", facts["device_count"])
	}
}

// TestRunbookContext_RejectsEmptyKey proves an unusable key is an error
// rather than a silent no-op. A value stored under an empty key is
// something no later when_cel can reference, so accepting it would lose the
// method's output without telling anyone.
func TestRunbookContext_RejectsEmptyKey(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	for _, method := range []struct {
		name string
		call func() error
	}{
		{name: "EmitFact", call: func() error { return rc.EmitFact("", 1) }},
		{name: "SetStat", call: func() error { return rc.SetStat("", 1) }},
	} {
		t.Run(method.name, func(t *testing.T) {
			err := method.call()
			if err == nil {
				t.Fatal("expected an empty key to be rejected")
			}
			if !strings.Contains(err.Error(), "empty key") {
				t.Errorf("error = %q, want it to mention the empty key", err)
			}
		})
	}
}

// TestRunbookContext_SecretsAreCopied proves neither the caller's map nor
// the returned one can reach back into the context's own state. A method
// mutating what InjectSecrets handed it must not change what the next call
// sees.
func TestRunbookContext_SecretsAreCopied(t *testing.T) {
	source := map[string]string{"password": "hunter2"}
	rc := engine.NewRunbookContext(source)

	source["password"] = "changed-after-construction"
	if got := rc.InjectSecrets()["password"]; got != "hunter2" {
		t.Errorf("mutating the source map changed the context: got %q", got)
	}

	returned := rc.InjectSecrets()
	returned["password"] = "mutated-by-caller"
	if got := rc.InjectSecrets()["password"]; got != "hunter2" {
		t.Errorf("mutating the returned map changed the context: got %q", got)
	}
}

// TestRunbookContext_FactsSnapshotIsIndependent proves the same for facts:
// a caller holding an earlier snapshot must not see later writes, or the
// executor could read a map another goroutine is still filling.
func TestRunbookContext_FactsSnapshotIsIndependent(t *testing.T) {
	rc := engine.NewRunbookContext(nil)
	if err := rc.EmitFact("first", 1); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}

	snapshot := rc.(factReader).Facts()
	if err := rc.EmitFact("second", 2); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}

	if _, leaked := snapshot["second"]; leaked {
		t.Error("an earlier snapshot saw a later write")
	}
}

// TestRunbookContext_ConcurrentEmit proves the context is safe for a method
// that emits from more than one goroutine, which nothing in the SDK
// contract forbids. Run with -race, this is what catches an unguarded map.
func TestRunbookContext_ConcurrentEmit(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			// Distinct keys per goroutine, so the final count is a real
			// assertion rather than a race on one key's last writer.
			_ = rc.EmitFact(string(rune('a'+n)), n)
		}(i)
	}
	wg.Wait()

	if got := len(rc.(factReader).Facts()); got != writers {
		t.Errorf("recorded %d facts, want %d", got, writers)
	}
}

// TestNewDeviceRunbookContext proves the constructor the composition root
// passes to the executor produces a usable, empty context.
func TestNewDeviceRunbookContext(t *testing.T) {
	rc := engine.NewDeviceRunbookContext(nil)
	if rc == nil {
		t.Fatal("NewDeviceRunbookContext returned nil")
	}
	if len(rc.InjectSecrets()) != 0 {
		t.Error("expected no secrets on a context built for a device with none resolved")
	}
	if len(rc.(factReader).Facts()) != 0 {
		t.Error("expected a fresh context to carry no facts")
	}
}
