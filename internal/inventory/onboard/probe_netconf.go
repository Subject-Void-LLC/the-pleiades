// The NETCONF probe: a session opened and the server's hello read.
package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

type netconfProber struct{}

func init() { Register(generic.TypeNetconf, netconfProber{}) }

func (netconfProber) Protocol() string { return "netconf" }

// Probe opens a NETCONF session on the device's NETCONF port, exactly as
// net.netconf.config does, and records what the session discovered: the
// protocol capabilities the hello announced, the datastores, and a summary of
// the YANG modules (their count, digest and completeness). The module list
// itself is the fact store's (net.netconf.config's yang.modules), not the
// inventory record's: a real Catalyst 8000V implements 559. A completed hello,
// which pkg/netconf refuses unless it names base:1.0 or base:1.1, is what
// grants NetconfCapable.
func (netconfProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(interface {
		capability.SSHTransportCapable
		capability.NetconfCapable
	})
	if !ok {
		return Probed{}, errors.New("the device does not name a NETCONF address")
	}
	auth, err := remoteexec.AuthFromSecrets(secrets)
	if err != nil {
		return Probed{}, err
	}
	target := remoteexec.Target{Host: dev.SSHHost(), Port: dev.NetconfPort()}
	conn, err := remoteexec.Shared(remoteexec.Options{}).Connect(ctx, nil, target, auth)
	if err != nil {
		return Probed{}, err
	}
	defer func() { _ = conn.Close() }()
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		return Probed{}, fmt.Errorf("requesting the netconf subsystem: %w", err)
	}
	session, err := netconf.Open(ctx, sub, netconf.Options{DiscoveryCacheKey: string(device.ID())})
	if err != nil {
		_ = sub.Close()
		return Probed{}, fmt.Errorf("NETCONF hello: %w", err)
	}
	defer func() { _ = session.Close(ctx) }()
	return netconfProbed(session.Discovery(), session.Framing().String()), nil
}

// netconfProbed builds the result from what a session discovered and the
// framing it settled on.
func netconfProbed(d netconf.Discovery, framing string) Probed {
	facts := map[string]any{
		"netconf_capabilities": factList(d.Protocol),
		"netconf_datastores":   factList(d.Datastores),
		"framing":              factText(framing),
		"yang_module_count":    len(d.Modules),
		"yang_modules_digest":  d.ModulesDigest(),
		"yang_completeness":    string(d.Completeness),
	}
	var warnings []string
	if d.LibraryError != "" {
		warnings = append(warnings, "the device's YANG library was not read: "+d.LibraryError)
	}
	return Probed{Capabilities: []capability.Name{capability.NameNetconf}, Facts: facts, Warnings: warnings}
}
