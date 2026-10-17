// Package network implements the container.network.* methods (Phase 119e):
// an engine's networks, Docker's or Podman's, created, removed, inspected,
// and joined by containers, through the engine's own API
// (pkg/containerengine), never its command line.
//
// Every method opens one guarded session declaring the operations it sends.
// Every method that changes something reads, compares and then writes
// through one body its check shares (the Phase 46 pattern): a check makes
// every read and refusal a run makes, and predicts the write.
package network

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	cerrdefs "github.com/containerd/errdefs"
	moby "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters shared by the namespace's methods.
const (
	paramName        = "name"
	paramID          = "id"
	paramNetwork     = "network"
	paramContainer   = "container"
	paramNetworkID   = "network_id"
	paramContainerID = "container_id"
)

// Returns shared by the namespace's methods.
const (
	statName   = "name"
	statID     = "id"
	statExists = "exists"
)

// engineID is what an engine's network or container ID is.
var engineID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// predefined are the networks an engine makes itself and owns: Docker's
// bridge, host and none, and Podman's podman. They are never created or
// removed here.
var predefined = []string{"bridge", "host", "none", "default", "podman"}

// isPredefined reports whether name is a network the engine owns.
func isPredefined(name string) bool { return slices.Contains(predefined, name) }

// validNetworkName refuses a network name an engine would read as
// something else, or one the engine owns.
func validNetworkName(name string) error {
	if err := containerengine.ValidName("network", name); err != nil {
		return err
	}
	if isPredefined(name) {
		return fmt.Errorf("network %s is the engine's own, and is never created or removed by a task", name)
	}
	return nil
}

// inspectNetwork reads the network named name, reporting false when the
// engine has none by that name. An engine also answers to an ID prefix, so
// an answer for a network with another name is no network by this one.
func inspectNetwork(ctx context.Context, s *containerengine.Session, name string) (moby.Inspect, bool, error) {
	res, err := s.API.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return moby.Inspect{}, false, nil
	}
	if err != nil {
		return moby.Inspect{}, false, fmt.Errorf("reading network %s: %w", name, err)
	}
	if res.Network.Name != name {
		return moby.Inspect{}, false, nil
	}
	if !engineID.MatchString(res.Network.ID) {
		return moby.Inspect{}, false, fmt.Errorf("the engine answered an unusable ID for network %s", name)
	}
	return res.Network, true, nil
}

// guardID refuses an object whose ID is not the one the task's key names,
// so an undo recorded against one network or container never acts on
// another that took its name since.
func guardID(params map[string]any, key, kind, name, id string) error {
	want, err := containerengine.TextParam(params, key)
	if err != nil {
		return err
	}
	if want == "" {
		return nil
	}
	if !engineID.MatchString(want) {
		return fmt.Errorf("%s: %q is not a %s ID", key, want, kind)
	}
	if id != want {
		return fmt.Errorf("%s %s is now %s, not %s, so this task, recorded against the other, does nothing to it", kind, name, id, want)
	}
	return nil
}

// recordStats records each stat, stopping at the first failure.
func recordStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}
