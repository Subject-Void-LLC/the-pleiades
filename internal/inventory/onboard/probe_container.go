// The container host probe: find the engines a container host runs, how a
// task reaches each, and the identity to pin.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// rootfulDockerSocket is where a system Docker engine listens.
const rootfulDockerSocket = "/var/run/docker.sock"

type containerProber struct{}

func init() { Register(container.TypeHost, containerProber{}) }

func (containerProber) Protocol() string { return "engine" }

// Probe finds the engines a container host runs. A host with an address is
// probed over its SSH login; one with an engine_tls address over mutual
// TLS, and both when it has both, which must then answer as the same
// engine; one with neither is the machine running this process, probed
// through its own sockets when AllowLocalEngine was called.
func (containerProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.SSHTransportCapable)
	if !ok {
		return Probed{}, errors.New("the device does not say where it is")
	}
	tlsAddr := ""
	if t, ok := device.(capability.EngineTLSCapable); ok {
		tlsAddr = t.EngineTLSAddress()
	}

	var found []engineFound
	var tried []string
	user := ""
	if dev.SSHHost() != "" {
		auth, err := remoteexec.AuthFromSecrets(secrets)
		if err != nil {
			return Probed{}, err
		}
		conn, err := remoteexec.Shared(remoteexec.Options{}).Connect(ctx, nil, remoteexec.Target{Host: dev.SSHHost(), Port: dev.SSHPort()}, auth)
		if err != nil {
			return Probed{}, err
		}
		defer func() { _ = conn.Close() }()
		f, login, notes, err := probeRemoteEngines(ctx, conn)
		if err != nil {
			return Probed{}, err
		}
		found, user, tried = f, login.user, notes
	}
	if tlsAddr != "" {
		cfg, err := devicetls.For(device).Config(secrets)
		if err != nil {
			return Probed{}, fmt.Errorf("the device's TLS settings: %w", err)
		}
		e, err := probeTLSEngine(ctx, tlsAddr, containerengine.TLS(tlsAddr, cfg))
		if err != nil {
			tried = append(tried, err.Error())
		} else if found, err = withTLS(found, e); err != nil {
			return Probed{}, err
		}
	}
	if dev.SSHHost() == "" && tlsAddr == "" {
		f, notes, err := probeLocalEngines(ctx)
		if err != nil {
			return Probed{}, err
		}
		// The account is recorded with the engines it could open, through the
		// one function a session compares it with.
		if user, err = containerengine.LocalUser(); err != nil {
			return Probed{}, err
		}
		found, tried = f, notes
	}
	if len(found) == 0 {
		return Probed{}, fmt.Errorf("no container engine answered: %s", strings.Join(tried, "; "))
	}
	return engineProbed(found, user), nil
}

// withTLS adds an engine reached over TLS to what SSH found: as one more
// carrier to the same engine when the identities agree, as a new engine when
// SSH found no Docker, and refused when they disagree, since the record would
// then describe two engines as one.
func withTLS(found []engineFound, tls engineFound) ([]engineFound, error) {
	for i, e := range found {
		if e.kind != tls.kind {
			continue
		}
		if e.id.ID != tls.id.ID {
			return nil, fmt.Errorf("the engine over TLS is %s and the one over SSH is %s; a container host describes one Docker engine", tls.id.ID, e.id.ID)
		}
		if !slices.Contains(e.carriers, capability.CarrierTLS) {
			found[i].carriers = append(found[i].carriers, capability.CarrierTLS)
		}
		return found, nil
	}
	return append(found, tls), nil
}
