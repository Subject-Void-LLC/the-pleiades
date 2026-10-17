// Pure tests of the facts container.engine.info reports from each engine's
// own answer.
package engine

import (
	"encoding/json"
	"testing"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// TestDockerFacts: rootless comes from the security options, and the
// cgroup version is one number.
func TestDockerFacts(t *testing.T) {
	sys := system.Info{Driver: "overlay2", CgroupVersion: "2", CgroupDriver: "systemd", SecurityOptions: []string{"name=seccomp,profile=builtin", "name=rootless", "name=cgroupns"},
		Containers: 4, ContainersRunning: 1, Images: 9, NCPU: 8, MemTotal: 1 << 34, KernelVersion: "6.8.0", OperatingSystem: "Ubuntu 24.04.1 LTS"}
	f := dockerFacts(sys, client.ServerVersionResult{Version: "29.8.0", APIVersion: "1.56", Os: "linux", Arch: "amd64"})
	want := map[string]any{"version": "29.8.0", "api_version": "1.56", "storage_driver": "overlay2", "rootless": true, "cgroup_version": "2",
		"cgroup_driver": "systemd", "os": "linux", "arch": "amd64", "containers": 4, "containers_running": 1, "images": 9, "cpus": 8,
		"memory_bytes": int64(1 << 34), "operating_system": "Ubuntu 24.04.1 LTS", "kernel_version": "6.8.0"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %#v, want %#v", k, f[k], v)
		}
	}
	if dockerFacts(system.Info{SecurityOptions: []string{"name=seccomp"}}, client.ServerVersionResult{})["rootless"] != false {
		t.Error("a rootful engine reported rootless")
	}
}

// TestPodmanFacts: libpod info's own fields, under Docker's names.
func TestPodmanFacts(t *testing.T) {
	var p containerengine.PodmanInfo
	if err := json.Unmarshal([]byte(`{"host":{"arch":"arm64","os":"linux","kernel":"6.10.3","cgroupVersion":"v2","cgroupManager":"systemd","cpus":4,"memTotal":8000000000,
		"distribution":{"distribution":"fedora","version":"40"},"security":{"rootless":true}},
		"store":{"graphDriverName":"overlay","containerStore":{"number":3,"running":2},"imageStore":{"number":5}},
		"version":{"APIVersion":"5.2.2","Version":"5.2.2"}}`), &p); err != nil {
		t.Fatal(err)
	}
	f := podmanFacts(p)
	want := map[string]any{"version": "5.2.2", "api_version": "5.2.2", "storage_driver": "overlay", "rootless": true, "cgroup_version": "2",
		"cgroup_driver": "systemd", "os": "linux", "arch": "arm64", "containers": 3, "containers_running": 2, "images": 5, "cpus": 4,
		"memory_bytes": int64(8000000000), "operating_system": "fedora 40", "kernel_version": "6.10.3"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %#v, want %#v", k, f[k], v)
		}
	}
}
