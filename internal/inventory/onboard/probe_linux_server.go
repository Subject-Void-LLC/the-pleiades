// The Linux server probe: a linux_server's capabilities come from its Go
// type, except the container engines it runs, which only the server itself
// can prove.
package onboard

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

type linuxProber struct{}

func init() { Register(linux.TypeServer, linuxProber{}) }

func (linuxProber) Protocol() string { return "ssh" }

// Probe logs in and looks for container engines. A server with none is
// onboarded all the same: it simply runs none.
func (linuxProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.SSHTransportCapable)
	if !ok || dev.SSHHost() == "" {
		return Probed{}, errors.New("the device is not reachable over SSH")
	}
	auth, err := remoteexec.AuthFromSecrets(secrets)
	if err != nil {
		return Probed{}, err
	}
	conn, err := remoteexec.Shared(remoteexec.Options{}).Connect(ctx, nil, remoteexec.Target{Host: dev.SSHHost(), Port: dev.SSHPort()}, auth)
	if err != nil {
		return Probed{}, err
	}
	defer func() { _ = conn.Close() }()
	found, login, _, err := probeRemoteEngines(ctx, conn)
	if err != nil {
		return Probed{}, err
	}
	return engineProbed(found, login.user), nil
}
