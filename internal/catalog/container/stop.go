// container.stop: a running container stopped, killed after its timeout.
package container

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// stopAction is container.stop's change.
var stopAction = action{
	fqcn: "container.stop",
	ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerStop},
	decide: func(state string, _ map[string]any) (bool, error) {
		switch state {
		case "running", "restarting", "paused":
			return true, nil
		}
		return false, nil
	},
	do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, params map[string]any) error {
		secs, err := stopSeconds(params)
		if err != nil {
			return err
		}
		_, err = s.API.ContainerStop(ctx, c.ID, client.ContainerStopOptions{Timeout: &secs})
		return err
	},
	after:   "exited",
	inverse: "container.start",
}

func init() { registerAction(stopAction) }
