// Package engine implements container.engine.info (Phase 119e): a
// device's container engine, Docker or Podman, as it reports itself
// through its own API (pkg/containerengine): Docker's /info and /version,
// and Podman's libpod info, where Podman says what Docker's form does not.
package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnInfo = "container.engine.info"

// The facts info reports.
const (
	statEngine            = "engine"
	statCarrier           = "carrier"
	statVersion           = "version"
	statAPIVersion        = "api_version"
	statStorageDriver     = "storage_driver"
	statRootless          = "rootless"
	statCgroupVersion     = "cgroup_version"
	statCgroupDriver      = "cgroup_driver"
	statOS                = "os"
	statArch              = "arch"
	statKernelVersion     = "kernel_version"
	statOperatingSystem   = "operating_system"
	statContainers        = "containers"
	statContainersRunning = "containers_running"
	statImages            = "images"
	statCPUs              = "cpus"
	statMemoryBytes       = "memory_bytes"
)

// dockerOps and podmanOps are what info sends to each engine.
var (
	dockerOps = []containerengine.Op{containerengine.OpInfo, containerengine.OpVersion}
	podmanOps = containerengine.PodmanInfoOps
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnInfo,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Notes:    "Reading is not changing: the method reads the engine's own report and changes nothing.",
			ReadOnly: true,
		}, infoDoc()),
		Invoke: info,
		// Reading is the same in a check: Check is Invoke.
		Check: info,
	})
}

// info is container.engine.info's body. The engine is chosen before the
// session opens, so the session declares only the reads that engine is
// sent.
func info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	ops := dockerOps
	if device != nil {
		kind, err := containerengine.ChooseEngine(device, params)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
		}
		if kind == capability.EnginePodman {
			ops = podmanOps
		}
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnInfo, ops...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	var facts map[string]any
	switch s.Kind {
	case capability.EnginePodman:
		pod, err := s.PodmanInfo(ctx)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: reading Podman's info: %w", fqcnInfo, err)
		}
		facts = podmanFacts(pod)
	default:
		sys, err := s.API.Info(ctx, client.InfoOptions{})
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: reading the engine's info: %w", fqcnInfo, err)
		}
		ver, err := s.API.ServerVersion(ctx, client.ServerVersionOptions{})
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: reading the engine's version: %w", fqcnInfo, err)
		}
		facts = dockerFacts(sys.Info, ver)
	}
	facts[statEngine] = string(s.Kind)
	facts[statCarrier] = string(s.Carrier)
	for k, v := range facts {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{}, nil
}

// dockerFacts are the facts Docker's /info and /version report. The OS
// and architecture are /version's, in Go's names (linux, amd64), as
// Podman reports them.
func dockerFacts(sys system.Info, ver client.ServerVersionResult) map[string]any {
	return map[string]any{
		statVersion:           ver.Version,
		statAPIVersion:        ver.APIVersion,
		statStorageDriver:     sys.Driver,
		statRootless:          slices.Contains(sys.SecurityOptions, "name=rootless"),
		statCgroupVersion:     cgroupVersion(sys.CgroupVersion),
		statCgroupDriver:      sys.CgroupDriver,
		statOS:                ver.Os,
		statArch:              ver.Arch,
		statKernelVersion:     sys.KernelVersion,
		statOperatingSystem:   sys.OperatingSystem,
		statContainers:        sys.Containers,
		statContainersRunning: sys.ContainersRunning,
		statImages:            sys.Images,
		statCPUs:              sys.NCPU,
		statMemoryBytes:       sys.MemTotal,
	}
}

// podmanFacts are the facts Podman's libpod info reports, under the names
// Docker's are reported by.
func podmanFacts(p containerengine.PodmanInfo) map[string]any {
	osName := strings.TrimSpace(p.Host.Distribution.Distribution + " " + p.Host.Distribution.Version)
	return map[string]any{
		statVersion:           p.Version.Version,
		statAPIVersion:        p.Version.APIVersion,
		statStorageDriver:     p.Store.GraphDriverName,
		statRootless:          p.Host.Security.Rootless,
		statCgroupVersion:     cgroupVersion(p.Host.CgroupVersion),
		statCgroupDriver:      p.Host.CgroupManager,
		statOS:                p.Host.OS,
		statArch:              p.Host.Arch,
		statKernelVersion:     p.Host.Kernel,
		statOperatingSystem:   osName,
		statContainers:        p.Store.ContainerStore.Number,
		statContainersRunning: p.Store.ContainerStore.Running,
		statImages:            p.Store.ImageStore.Number,
		statCPUs:              p.Host.CPUs,
		statMemoryBytes:       p.Host.MemTotal,
	}
}

// cgroupVersion is a cgroup version as one number: Docker says 2, Podman
// says v2.
func cgroupVersion(v string) string { return strings.TrimPrefix(v, "v") }
