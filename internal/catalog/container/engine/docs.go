// container.engine.info's documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package engine

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// infoDoc is container.engine.info's documentation.
func infoDoc() collection.Doc {
	always := func(name, typ, description string) collection.ReturnField {
		return collection.ReturnField{Name: name, Type: typ, Returned: "always", Description: description}
	}
	return collection.Doc{
		Summary:     "Reads the container engine's own report of itself.",
		Description: "Reads the device's container engine as it reports itself, for a later task's when: Docker's info and version, or Podman's libpod info, which says what Docker's form does not. Both are reported under the same names: the engine and its version, the newest API version it speaks, its storage driver, whether it runs rootless, the cgroup version and driver, the OS and architecture (in Go's names, linux and amd64), the kernel, the host's operating system, its container and image counts, CPUs and memory. On a device running both engines, engine chooses which. Reading changes nothing, and a check reads the same way." + containerengine.ReachNote,
		Params:      []collection.Param{containerengine.HostKeyParam},
		Fragments:   []string{"container_engine"},
		Returns: []collection.ReturnField{
			always(statEngine, "string", "The engine: docker or podman."),
			always(statCarrier, "string", "How the engine was reached: local, socket, stdio or tls."),
			always(statVersion, "string", "The engine's release."),
			always(statAPIVersion, "string", "The newest API version the engine speaks."),
			always(statStorageDriver, "string", "The storage driver (overlay2, overlay, btrfs, ...)."),
			always(statRootless, "bool", "Whether the engine runs without root."),
			always(statCgroupVersion, "string", "The cgroup version, 1 or 2."),
			always(statCgroupDriver, "string", "The cgroup driver: systemd or cgroupfs."),
			always(statOS, "string", "The engine's OS, in Go's name (linux)."),
			always(statArch, "string", "The engine's architecture, in Go's name (amd64, arm64)."),
			always(statKernelVersion, "string", "The host's kernel release."),
			always(statOperatingSystem, "string", "The host's operating system, as the engine names it."),
			always(statContainers, "int", "How many containers the engine has, running or not."),
			always(statContainersRunning, "int", "How many of them are running."),
			always(statImages, "int", "How many images the engine has."),
			always(statCPUs, "int", "The host's CPUs, as the engine sees them."),
			always(statMemoryBytes, "int", "The host's memory in bytes, as the engine sees it."),
		},
		Examples: []collection.Example{
			{Name: "Run a container only on a rootless engine", RunbookYAML: "- name: Read the engine\n  container.engine.info:\n  register: engine\n\n- name: Run the build cache\n  container.run:\n    name: cache\n    image: redis:7.4\n  when:\n    - result.engine.rootless\n"},
		},
		SeeAlso: []string{"container.network.info", "container.volume.info", "facts.gather"},
	}
}
