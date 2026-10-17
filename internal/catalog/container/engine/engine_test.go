// container.engine.info against this machine's real Docker engine.
package engine_test

import (
	"context"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestEngineInfo_ReportsTheRealEngine: what info reports is what the engine
// itself says, checked against the engine's own answers, and a check
// reports the same.
func TestEngineInfo_ReportsTheRealEngine(t *testing.T) {
	h := newHarness(t)
	d, ok := collection.Lookup("container.engine.info")
	if !ok {
		t.Fatal("container.engine.info is not registered")
	}
	ctx := context.Background()
	sys, err := h.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ver, err := h.api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{false, true} {
		rc := fresh()
		run := d.Invoke
		if check {
			run = d.Check
		}
		res, err := run(ctx, rc, h.device, map[string]any{})
		if err != nil || res.Changed {
			t.Fatalf("info (check %t) = %+v, %v", check, res, err)
		}
		want := map[string]any{
			"engine": "docker", "carrier": "local", "version": ver.Version, "api_version": ver.APIVersion,
			"storage_driver": sys.Info.Driver, "cgroup_driver": sys.Info.CgroupDriver, "os": ver.Os, "arch": ver.Arch,
			"kernel_version": sys.Info.KernelVersion, "cpus": sys.Info.NCPU, "memory_bytes": sys.Info.MemTotal,
		}
		for k, v := range want {
			if rc.stats[k] != v {
				t.Errorf("check %t: %s = %v, the engine says %v", check, k, rc.stats[k], v)
			}
		}
		if rc.stats["cgroup_version"] != sys.Info.CgroupVersion {
			t.Errorf("cgroup_version = %v, the engine says %v", rc.stats["cgroup_version"], sys.Info.CgroupVersion)
		}
		if _, ok := rc.stats["rootless"].(bool); !ok {
			t.Errorf("rootless = %#v, want a bool", rc.stats["rootless"])
		}
		if n, ok := rc.stats["images"].(int); !ok || n < 1 {
			t.Errorf("images = %#v; the harness pulled one", rc.stats["images"])
		}
		if _, ok := rc.stats["inverse"]; ok {
			t.Error("a read recorded an undo")
		}
	}
}

// TestEngineInfo_RefusesAnEngineTheDeviceDoesNotRun: naming Podman on a
// Docker-only device is refused before contact.
func TestEngineInfo_RefusesAnEngineTheDeviceDoesNotRun(t *testing.T) {
	h := newHarness(t)
	d, _ := collection.Lookup("container.engine.info")
	if _, err := d.Invoke(context.Background(), fresh(), h.device, map[string]any{"engine": "podman"}); err == nil {
		t.Error("naming an engine the device does not run was not refused")
	}
}
