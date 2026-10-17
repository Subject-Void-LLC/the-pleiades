// container.start: a stopped or created container started.
package container

import (
	"context"
	"fmt"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// startAction is container.start's change.
var startAction = action{
	fqcn: "container.start",
	ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerStart},
	decide: func(state string, _ map[string]any) (bool, error) {
		switch state {
		case "running", "restarting":
			return false, nil
		case "created", "exited":
			return true, nil
		case "paused":
			return false, fmt.Errorf("it is paused, which start does not resume; container.stop stops a paused container, and a start after that runs it")
		}
		return false, fmt.Errorf("it is %s, which cannot be started", state)
	},
	do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, _ map[string]any) error {
		_, err := s.API.ContainerStart(ctx, c.ID, client.ContainerStartOptions{})
		return err
	},
	after:   "running",
	inverse: "container.stop",
}

func init() { registerAction(startAction) }
