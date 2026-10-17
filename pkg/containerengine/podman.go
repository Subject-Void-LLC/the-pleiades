// Podman's own report of itself, read through its libpod API, which has
// what the Docker-compatible /info does not say the same way (rootless, the
// cgroup version, the store's counts).
package containerengine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// PodmanInfoOps are the operations PodmanInfo sends.
var PodmanInfoOps = []Op{OpLibpodInfo}

// PodmanInfo is the part of Podman's libpod info a caller reports.
type PodmanInfo struct {
	// Host describes the machine and how Podman runs on it.
	Host struct {
		Arch          string `json:"arch"`
		OS            string `json:"os"`
		Kernel        string `json:"kernel"`
		Hostname      string `json:"hostname"`
		CgroupVersion string `json:"cgroupVersion"`
		CgroupManager string `json:"cgroupManager"`
		CPUs          int    `json:"cpus"`
		MemTotal      int64  `json:"memTotal"`
		Distribution  struct {
			Distribution string `json:"distribution"`
			Version      string `json:"version"`
		} `json:"distribution"`
		Security struct {
			Rootless bool `json:"rootless"`
		} `json:"security"`
	} `json:"host"`
	// Store describes the container store.
	Store struct {
		GraphDriverName string `json:"graphDriverName"`
		ContainerStore  struct {
			Number  int `json:"number"`
			Running int `json:"running"`
		} `json:"containerStore"`
		ImageStore struct {
			Number int `json:"number"`
		} `json:"imageStore"`
	} `json:"store"`
	// Version is Podman's release and API version.
	Version struct {
		APIVersion string `json:"APIVersion"`
		Version    string `json:"Version"`
	} `json:"version"`
}

// PodmanInfo reads Podman's libpod info through the session's guard, which
// must allow PodmanInfoOps. A session to any other engine is refused before
// contact.
func (s *Session) PodmanInfo(ctx context.Context) (PodmanInfo, error) {
	if s.Kind != capability.EnginePodman {
		return PodmanInfo{}, fmt.Errorf("containerengine: libpod info is Podman's, and this session reaches %s", s.Kind)
	}
	var info PodmanInfo
	if err := getJSON(ctx, s.dial, "/v5.0.0/libpod/info", &info); err != nil {
		return PodmanInfo{}, err
	}
	return info, nil
}
