// Tests for RunbookContext, the sdk.RunbookContext a child process hands
// the method it runs.
//
// The first two moved here from internal/adapters/native (where the type
// was childRunbookContext). The rest pin the copies its doc comments
// promise, since the whole point of the type is that Zero reaches every
// copy of a secret this type itself holds and none that it does not own.
package external_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

func TestRunbookContext_ZeroClearsSecrets(t *testing.T) {
	rc := external.NewRunbookContext(map[string]string{"password": "hunter2"})
	if got := rc.InjectSecrets(); got["password"] != "hunter2" {
		t.Fatalf("InjectSecrets() before Zero = %v, want password=hunter2", got)
	}

	rc.Zero()

	if got := rc.InjectSecrets(); len(got) != 0 {
		t.Errorf("InjectSecrets() after Zero = %v, want empty", got)
	}
}

func TestRunbookContext_SetStatAndEmitFactShareOneMap(t *testing.T) {
	rc := external.NewRunbookContext(nil)
	if err := rc.SetStat("a", 1); err != nil {
		t.Fatalf("SetStat: %v", err)
	}
	if err := rc.EmitFact("b", 2); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}
	facts := rc.Facts()
	if facts["a"] != 1 || facts["b"] != 2 {
		t.Errorf("Facts() = %v, want a=1 b=2", facts)
	}
}

// TestNewRunbookContext_NeverSharesTheCallersMap proves both directions of
// the copy NewRunbookContext makes. Zero must not reach back into memory
// the caller still owns (a Runner that zeroed its own dispatch payload
// mid-run would break every later task on the same device), and a caller
// editing its map afterward must not change what the method is handed.
func TestNewRunbookContext_NeverSharesTheCallersMap(t *testing.T) {
	callers := map[string]string{"username": "admin", "password": "hunter2"}
	rc := external.NewRunbookContext(callers)

	callers["password"] = "changed-after-construction"
	if got := rc.InjectSecrets()["password"]; got != "hunter2" {
		t.Errorf("InjectSecrets()[password] = %q after the caller edited its own map, want %q", got, "hunter2")
	}

	rc.Zero()
	if callers["username"] != "admin" || callers["password"] != "changed-after-construction" {
		t.Errorf("Zero() reached the caller's own map: %v", callers)
	}
}

// TestRunbookContext_ReturnedMapsAreCopies proves a method cannot reach
// back into the context through what it was handed. A method that edited
// InjectSecrets' map must not change the next call's answer, and a caller
// that edited Facts' snapshot must not change what the response carries.
func TestRunbookContext_ReturnedMapsAreCopies(t *testing.T) {
	rc := external.NewRunbookContext(map[string]string{"password": "hunter2"})
	if err := rc.SetStat("reply", "pong"); err != nil {
		t.Fatalf("SetStat: %v", err)
	}

	secrets := rc.InjectSecrets()
	secrets["password"] = "tampered"
	secrets["extra"] = "added"
	if got := rc.InjectSecrets(); got["password"] != "hunter2" || len(got) != 1 {
		t.Errorf("InjectSecrets() = %v after editing an earlier result, want only password=hunter2", got)
	}

	facts := rc.Facts()
	facts["reply"] = "tampered"
	facts["extra"] = "added"
	if got := rc.Facts(); got["reply"] != "pong" || len(got) != 1 {
		t.Errorf("Facts() = %v after editing an earlier snapshot, want only reply=pong", got)
	}
}

// TestRunbookContext_ConcurrentUseIsSafe runs a method's worth of stat
// writers, secret readers and fact snapshots at once. A method is free to
// fan work out across goroutines, and each of them may record a stat, so
// the type's own mutex is load-bearing. Under -race this is the test that
// proves it.
func TestRunbookContext_ConcurrentUseIsSafe(t *testing.T) {
	rc := external.NewRunbookContext(map[string]string{"password": "hunter2"})

	const writers = 16
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("stat-%d", i)
			// Alternate between the two recording calls, which share
			// one map, so both paths contend on the same lock.
			var err error
			if i%2 == 0 {
				err = rc.SetStat(key, i)
			} else {
				err = rc.EmitFact(key, i)
			}
			if err != nil {
				t.Errorf("recording %s: %v", key, err)
			}
			_ = rc.InjectSecrets()
			_ = rc.Facts()
		}(i)
	}
	wg.Wait()

	facts := rc.Facts()
	if len(facts) != writers {
		t.Fatalf("Facts() holds %d entries after %d concurrent writers, want %d", len(facts), writers, writers)
	}
	for i := 0; i < writers; i++ {
		if got := facts[fmt.Sprintf("stat-%d", i)]; got != i {
			t.Errorf("Facts()[stat-%d] = %v, want %d", i, got, i)
		}
	}
}
