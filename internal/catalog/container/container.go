// Package container implements the container.* namespace (Phase 119e): a
// container's life on a device's engine, Docker or Podman, through the
// engine's own API (pkg/containerengine), never its command line.
//
// Every method opens one guarded session declaring the operations it
// sends, and every method that changes something reads, compares and then
// writes through one body its check shares (the Phase 46 pattern): a
// check makes every read and refusal a run makes, and predicts the write.
package container

import (
	"context"
	"fmt"
	"regexp"

	cerrdefs "github.com/containerd/errdefs"
	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters and returns shared by the namespace's methods.
const (
	paramName  = "name"
	paramID    = "id"
	paramForce = "force"

	statID      = "id"
	statName    = "name"
	statState   = "state"
	statImage   = "image"
	statImageID = "image_id"
	statDigest  = "image_digest"
	statEnv     = "env_names"
	statPulled  = "pulled"
)

// containerID is what an engine's container ID is.
var containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// open opens the device's engine for fqcn, allowing ops.
func open(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, ops ...containerengine.Op) (*containerengine.Session, error) {
	return containerengine.Open(ctx, rc, device, params, fqcn, ops...)
}

// inspect reads the container named name, reporting false when the engine
// has none by that name; any other failure is an error.
func inspect(ctx context.Context, s *containerengine.Session, name string) (moby.InspectResponse, bool, error) {
	res, err := s.API.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return moby.InspectResponse{}, false, nil
	}
	if err != nil {
		return moby.InspectResponse{}, false, fmt.Errorf("reading container %s: %w", name, err)
	}
	if !containerID.MatchString(res.Container.ID) {
		return moby.InspectResponse{}, false, fmt.Errorf("the engine answered an unusable ID for container %s", name)
	}
	return res.Container, true, nil
}

// stateOf is a container's state as the engine names it.
func stateOf(c moby.InspectResponse) string {
	if c.State == nil {
		return "unknown"
	}
	return string(c.State.Status)
}

// guardID refuses a container whose ID is not the one the task names, so
// an undo recorded against one container never acts on another that took
// its name since.
func guardID(params map[string]any, c moby.InspectResponse) error {
	want := sdk.StringParam(params, paramID)
	if want == "" {
		return nil
	}
	if !containerID.MatchString(want) {
		return fmt.Errorf("%s: %q is not a container ID", paramID, want)
	}
	if c.ID != want {
		return fmt.Errorf("container %s is now %s, not %s, so this task, recorded against the other, does nothing to it", c.Name, c.ID, want)
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

// presentImage is the image spec names as the task runs it: by the pull
// policy missing, pinned to a digest and pulled when absent (never by a
// check); by never, the image the engine already holds.
func presentImage(ctx context.Context, s *containerengine.Session, spec containerengine.Spec, auth string, mode collection.Mode) (containerengine.PresentImage, error) {
	if spec.Pull == containerengine.PullNever {
		return s.LocalImage(ctx, spec.Image)
	}
	return s.EnsureImage(ctx, spec.Image, auth, mode == collection.ModeExecute)
}
