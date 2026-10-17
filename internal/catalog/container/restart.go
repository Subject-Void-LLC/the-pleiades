// container.restart: a container restarted.
package container

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// restartAction is container.restart's change.
var restartAction = action{
	fqcn: "container.restart",
	ops:  []containerengine.Op{containerengine.OpContainerInspect, containerengine.OpContainerRestart},
	decide: func(state string, _ map[string]any) (bool, error) {
		switch state {
		case "running", "restarting", "created", "exited":
			return true, nil
		case "paused":
			return false, fmt.Errorf("it is paused, which restart does not resume; container.stop stops a paused container, and a start after that runs it")
		}
		return false, fmt.Errorf("it is %s, which cannot be restarted", state)
	},
	do: func(ctx context.Context, s *containerengine.Session, c moby.InspectResponse, params map[string]any) error {
		secs, err := stopSeconds(params)
		if err != nil {
			return err
		}
		_, err = s.API.ContainerRestart(ctx, c.ID, client.ContainerRestartOptions{Timeout: &secs})
		return err
	},
	after: "running",
}

func init() { registerAction(restartAction) }
