// Package yang: the NETCONF source of net.yang.schema, a session over the
// device's SSH "netconf" subsystem asking RFC 6022's get-schema. It opens its
// own session as each catalog package does (internal/catalog/net/netconf and
// internal/catalog/wait each have one): a Collection package may import only
// pkg/, so the session code cannot be shared across them.
package yang

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// netconfSource fetches sources over one NETCONF session.
type netconfSource struct {
	session *netconf.Session
	sub     *remoteexec.Subsystem
	conn    *remoteexec.Conn
	// unverified says the session's host key was not verified.
	unverified bool
}

// openNETCONF opens a session to device and refuses one whose server offers
// no get-schema, naming the other protocol.
func openNETCONF(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (source, error) {
	dev, ok := device.(capability.NetconfCapable)
	if !ok {
		return nil, fmt.Errorf("device %q does not expose a NETCONF port (it does not implement %s)", device.Name(), capability.NameNetconf)
	}
	conn, err := sdk.ConnectPort(ctx, rc, device, params, fqcn, dev.NetconfPort())
	if err != nil {
		// A NETCONF listener is an SSH server of its own, often with its own
		// key, as net.netconf.config says.
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return nil, fmt.Errorf("%w; its NETCONF listener presents a host key of its own, separate from its SSH one: trust it with pleiades trust-host %s --first-connect --netconf", err, device.Name())
		}
		return nil, err
	}
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	unverified, _ := sdk.BoolParamOr(params, sdk.ParamInsecureSkipHostKeyVerify, false)
	// The YANG library is cached for the run by the device and the account,
	// and only for a session whose host key was verified, as
	// net.netconf.config's sessions do.
	cacheKey := string(device.ID()) + "\x00" + rc.InjectSecrets()[wire.SecretUsername]
	if unverified {
		cacheKey = ""
	}
	session, err := netconf.Open(ctx, sub, netconf.Options{DiscoveryCacheKey: cacheKey})
	if err != nil {
		_ = sub.Close()
		_ = conn.Close()
		return nil, err
	}
	s := &netconfSource{session: session, sub: sub, conn: conn, unverified: unverified}
	if !session.CanGetSchema() {
		s.close()
		return nil, fmt.Errorf("device %q's NETCONF server does not list ietf-netconf-monitoring (RFC 6022), so it offers no get-schema: fetch over RESTCONF with %s: %s, if the device serves it", device.Name(), paramVia, viaRESTCONF)
	}
	return s, nil
}

// list is what the session's discovery names: the library's schema entries,
// or for a server with no library read, the modules its hello names.
func (s *netconfSource) list(context.Context) (listing, error) {
	d := s.session.Discovery()
	l := listing{entries: d.Schemas, completeness: d.Completeness, libraryError: d.LibraryError}
	if len(l.entries) == 0 {
		for _, m := range d.Modules {
			l.entries = append(l.entries, yanglib.Schema{Name: m.Name, Revision: m.Revision})
		}
	}
	return l, nil
}

// fetch asks get-schema for e.
func (s *netconfSource) fetch(ctx context.Context, e yanglib.Schema, limit int) ([]byte, error) {
	return s.session.GetSchema(ctx, e.Name, e.Revision, limit)
}

// warnings are what the task says of the session: an unverified host key,
// and the device's own warnings.
func (s *netconfSource) warnings() []string {
	var out []string
	if s.unverified {
		out = append(out, "the NETCONF host key was not verified, so the sources saved may not be the device's")
	}
	for _, w := range s.session.TakeWarnings() {
		out = append(out, "the device warned: "+w.Error())
	}
	return out
}

// close ends the session, the subsystem channel and then the connection.
func (s *netconfSource) close() {
	_ = s.sub.Close()
	_ = s.conn.Close()
}
