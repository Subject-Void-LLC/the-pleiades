// The engine a docker CLI method's login reaches, held to the identity
// onboarding pinned.
package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// checkEngine refuses to act unless the Docker engine the device's login
// reaches is the one onboarding pinned for the device.
//
// These methods run the docker CLI over SSH, so which engine they act on is
// whatever that login's own docker configuration points at (DOCKER_HOST, a
// docker context), not something the device's record decides. The engine
// identity onboarding pinned (capability.EngineReach.EngineID, Docker's own
// /info ID) is what container.docker.exec already compares through the
// engine port; this holds the CLI methods to the same pin, by asking docker
// info for the ID the login sees, before the first inspect. A device with no
// pinned identity was never onboarded and is refused, naming the command.
func checkEngine(ctx context.Context, conn *remoteexec.Conn, device inventory.InventoryItem) error {
	var want capability.EngineID
	if reach, ok := device.(capability.EngineReach); ok {
		want = reach.EngineID(capability.EngineDocker)
	}
	if want == "" {
		return fmt.Errorf("device %s records no pinned Docker engine identity; onboard it (pleiades onboard %s) so a task can tell its engine from another", device.Name(), device.Name())
	}
	result, err := runBounded(ctx, conn, remoteexec.QuoteCommand([]string{"docker", "info", "--format={{.ID}}"}), nil, quickCallTimeout)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker info could not read the engine's identity (exit %d): %s", result.ExitCode, failureDetail(result))
	}
	if got := strings.TrimSpace(result.Stdout); got != string(want) {
		return fmt.Errorf("the Docker engine this login reaches (%s) is not the one onboarding pinned for %s (%s); nothing was changed, and if the engine was replaced, onboard the device again",
			remoteexec.Excerpt(got, 128), device.Name(), want)
	}
	return nil
}
