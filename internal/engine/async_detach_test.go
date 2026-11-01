//go:build !windows

// Tests that an async task's SSH commands run detached on the device (pkg/
// remoteexec's detach.go) and a plain task's do not, through the real
// Executor, against the in-process SSH server, whose own command log is the
// evidence.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestAsync_AnAsyncTasksCommandsRunDetached(t *testing.T) {
	need.POSIXShell(t)
	t.Setenv("HOME", t.TempDir())
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &becomeSSHDevice{
		Stub: &inventorytest.Stub{StubID: "d1", StubName: "d1", Caps: []capability.Name{capability.NameSSHTransport}, StubState: inventory.StateActive},
		host: srv.Host, port: srv.Port,
	}
	got := map[string]string{}
	method := registerTestMethod(t, "detaches", collection.StatusImplemented,
		func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			conn, err := sdk.Connect(ctx, rc, device, map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}, "enginetest.detaches")
			if err != nil {
				return collection.Result{}, err
			}
			defer func() { _ = conn.Close() }()
			res, err := conn.Run(ctx, "echo ran")
			if err != nil {
				return collection.Result{}, err
			}
			id, _ := params["id"].(string)
			got[id] = res.Stdout
			return collection.Result{Changed: true}, nil
		})
	newContext := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) {
		return &becomeStatsContext{secrets: srv.Secrets(), stats: map[string]interface{}{}}, nil
	}
	x := engine.NewExecutor(mapResolver{"all": {device}}, engine.NewCollectionActionExecutor(&recordingFallback{}, newContext), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()))

	starts := func() int {
		n := 0
		for _, c := range srv.Commands() {
			if strings.Contains(c, ".pleiades-async") {
				n++
			}
		}
		return n
	}
	result, err := x.Run(context.Background(), yamlRunbook(t, "id: a\nhosts: all\ntasks:\n  - name: plain\n    "+method+":\n      id: plain\n"))
	if err != nil || result.HasErrors() {
		t.Fatalf("plain run: %v %+v", err, result.Nodes)
	}
	if n := starts(); n != 0 || got["plain"] != "ran\n" {
		t.Fatalf("a plain task ran %q with %d detached starts; want ran and none", got["plain"], n)
	}
	result, err = x.Run(context.Background(), yamlRunbook(t, "id: b\nhosts: all\ntasks:\n  - name: async\n    "+method+":\n      id: async\n    async: 30\n    poll: 1\n"))
	if err != nil || result.HasErrors() {
		t.Fatalf("async run: %v %+v", err, result.Nodes)
	}
	if n := starts(); n == 0 || got["async"] != "ran\n" {
		t.Fatalf("an async task ran %q with %d detached starts; want ran, detached", got["async"], n)
	}
}
