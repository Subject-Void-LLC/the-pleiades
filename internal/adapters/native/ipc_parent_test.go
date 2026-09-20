package native

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestMain lets this package's own test binary double as the collection
// child: when re-exec'd with InternalCollectionRunnerArg (exactly what
// ipcCollectionExecutor.invoke does via os.Executable(), which resolves
// to this test binary when running under `go test`), it runs
// RunCollectionChild and exits immediately, before testing.M ever parses
// a single -test.* flag. This is the standard Go "TestHelperProcess"
// pattern (as used by the standard library's own os/exec tests), and it
// is what lets TestIPCCollectionExecutor_Invoke_RealSubprocess below spawn
// a genuine second OS process without any Docker or external binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == InternalCollectionRunnerArg {
		os.Exit(RunCollectionChild(context.Background()))
	}
	os.Exit(m.Run())
}

// nativeIPCEchoMethod is registered once, at package init, specifically so
// it is also registered inside the re-exec'd child process (a fresh OS
// process running this exact binary from the top): pkg/collection's
// registry is process-local, in-memory state, so a registration made only
// inside a *testing.T function body would never exist in the child at
// all, and collection.Lookup(req.FQCN) would report "not registered" for
// a method the parent test process can see perfectly well.
const nativeIPCEchoMethodName = "nativeipctest.subprocess_echo"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name:     nativeIPCEchoMethodName,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			secrets := rc.InjectSecrets()
			if err := rc.SetStat("echoed_param", params["message"]); err != nil {
				return collection.Result{}, err
			}
			if err := rc.SetStat("echoed_username", secrets["username"]); err != nil {
				return collection.Result{}, err
			}
			sshDev, ok := device.(capability.SSHTransportCapable)
			if !ok {
				return collection.Result{}, nil
			}
			if err := rc.SetStat("device_host", sshDev.SSHHost()); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		},
	})
}

// TestIPCCollectionExecutor_Invoke_RealSubprocess proves the real
// subprocess boundary end to end: a genuine second OS process (not a
// goroutine, not a fake) receives the task's params and the device's
// secret over the real stdin/fd-3 IPC framing, runs the registered
// Collection method, and returns its facts back across the real pipe.
// Per LESSONS_LEARNED #73 ("the boundary crossing is the feature, not the
// header"), the assertion that matters is echoed_username: proving the
// secret was genuinely usable *inside* the child, not merely serialized
// into the request.
func TestIPCCollectionExecutor_Invoke_RealSubprocess(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}

	device := newWireDevice(wire.DispatchPayload{
		JobID:      "job-1",
		DeviceID:   "dev-1",
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
		SSHPort:    22,
		Secrets:    map[string]string{"username": "admin"},
	})
	desc := collection.Descriptor{Name: nativeIPCEchoMethodName}

	result, facts, err := exec.invoke(context.Background(), desc, device, map[string]any{"message": "hello"}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !result.Changed {
		t.Error("Changed = false, want true")
	}
	if facts["echoed_param"] != "hello" {
		t.Errorf("facts[echoed_param] = %v, want %q", facts["echoed_param"], "hello")
	}
	if facts["echoed_username"] != "admin" {
		t.Errorf("facts[echoed_username] = %v, want %q -- the real secret must have crossed the subprocess boundary and been usable inside it", facts["echoed_username"], "admin")
	}
	if facts["device_host"] != "10.0.0.1" {
		t.Errorf("facts[device_host] = %v, want %q", facts["device_host"], "10.0.0.1")
	}
}

// TestIPCCollectionExecutor_Invoke_UnregisteredFQCNReturnsError proves a
// method the child cannot find is reported as a real error, masked
// through the device's own secrets like every other failure path.
func TestIPCCollectionExecutor_Invoke_UnregisteredFQCNReturnsError(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}

	device := newWireDevice(wire.DispatchPayload{DeviceName: "router1", DeviceHost: "10.0.0.1"})
	desc := collection.Descriptor{Name: "does.not.exist"}

	if _, _, err := exec.invoke(context.Background(), desc, device, nil, collection.ModeExecute); err == nil {
		t.Fatal("expected an error for an fqcn the child cannot find")
	}
}

// TestIPCCollectionExecutor_Invoke_CancelKillsSubprocessPromptly proves
// ctx cancellation actually reaches and terminates the child process
// (interruptible self-abort, internal/runner's executeWithLease), not
// just the in-process goroutine tree: a method that sleeps far longer
// than the test's own cancellation deadline must still cause invoke to
// return promptly once ctx is canceled.
func TestIPCCollectionExecutor_Invoke_CancelKillsSubprocessPromptly(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())

	name := "nativeipctest.slow"
	if err := collection.Register(collection.Descriptor{
		Name:     name,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
		Invoke: func(ctx context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			select {
			case <-ctx.Done():
				return collection.Result{}, ctx.Err()
			case <-time.After(30 * time.Second):
				return collection.Result{}, nil
			}
		},
	}); err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}

	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}

	device := newWireDevice(wire.DispatchPayload{DeviceName: "router1", DeviceHost: "10.0.0.1"})
	desc := collection.Descriptor{Name: name}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, _, err = exec.invoke(ctx, desc, device, nil, collection.ModeExecute)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a canceled subprocess invocation")
	}
	if elapsed > childWaitDelay+2*time.Second {
		t.Errorf("invoke took %v to return after cancellation, want well under childWaitDelay (%v) plus a small margin", elapsed, childWaitDelay)
	}
}
