// Package netconf: the session a task opens, behind the seam this package's
// own tests swap.
package netconf

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
	"golang.org/x/crypto/ssh/knownhosts"
)

// netconfSession is the narrow surface Config actually uses from a
// *netconf.Session: enough that this package's own tests can swap in a
// canned double with no real I/O, and no more. It mirrors the role
// iosSession plays in internal/catalog/net/ios.
type netconfSession interface {
	GetConfigFrom(ctx context.Context, source netconf.Datastore, p datastore.Path) (datastore.Payload, error)
	ReadConfig(ctx context.Context, source netconf.Datastore, filter string) ([]*yangdoc.Element, error)
	WithDefaults() string
	EditConfig(ctx context.Context, req netconf.EditConfigRequest) error
	Lock(ctx context.Context) error
	Unlock(ctx context.Context) error
	Commit(ctx context.Context) error
	DiscardChanges(ctx context.Context) error
	Validate(ctx context.Context) error
	CanValidate() bool
	HasCapability(urn string) bool
	LimitToCheck()
	Target() netconf.Datastore
	Discovery() netconf.Discovery
	TakeWarnings() []netconf.RPCError
	Close() error
}

// openSession is the seam this package's own tests swap, the same role
// internal/catalog/net/{cli,ios}'s identically-named seams play there.
// It is declared per package rather than shared for a reason the build
// enforces: a Collection package may import only pkg/
// (internal/archtest's TestCatalogPackagesImportOnlyPkg), so these
// packages cannot share an internal/ helper, and moving the seam into
// pkg/netconf would export test scaffolding into a public API.
var openSession = realOpenSession

// realOpenSession dials the device's NETCONF port, opens the netconf
// subsystem on it and the session over that, writing target, or choosing the
// datastore from the hello when target is empty.
func realOpenSession(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
	// Checked here rather than left to sdk.ConnectPort's own identical
	// guard, because the capability refusal below names the device and
	// would dereference a nil one to do it. Found by a test asserting
	// this function does not panic, which it did.
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", fqcn)
	}

	// The device must implement NetconfCapable to name its port. The
	// manifest's RequiredCapabilities is what normally guarantees this,
	// so reaching the error below means something dispatched around that
	// check; it is a named refusal rather than a panic on a failed
	// assertion.
	netconfDev, ok := device.(capability.NetconfCapable)
	if !ok {
		return nil, fmt.Errorf("%s: device %q does not expose a NETCONF port (it does not implement %s)", fqcn, device.Name(), capability.NameNetconf)
	}

	conn, err := sdk.ConnectPort(ctx, rc, device, params, fqcn, netconfDev.NetconfPort())
	if err != nil {
		// A NETCONF server is an SSH server of its own, often with its own
		// key (IOS XE's is OpenSSH on 830 beside Cisco's SSH on 22), so a
		// device whose SSH key is trusted can still present an unknown one
		// here; the refusal says how to trust it.
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return nil, fmt.Errorf("%w; its NETCONF listener presents a host key of its own, separate from its SSH one: trust it with pleiades trust-host %s --first-connect --netconf", err, device.Name())
		}
		return nil, err
	}

	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	// The YANG library is cached for the run by the device's identity and
	// the account (access control may show two accounts different
	// libraries), and only for a session whose host key was verified.
	cacheKey := string(device.ID()) + "\x00" + rc.InjectSecrets()[wire.SecretUsername]
	if skip, _ := sdk.BoolParamOr(params, sdk.ParamInsecureSkipHostKeyVerify, false); skip {
		cacheKey = ""
	}
	session, err := netconf.Open(ctx, sub, netconf.Options{Target: target, ChooseTarget: target == "", DiscoveryCacheKey: cacheKey})
	if err != nil {
		_ = sub.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	return &sessionAndConn{Session: session, sub: sub, conn: conn}, nil
}

// sessionAndConn embeds a *netconf.Session (promoting every operation)
// but overrides Close so one call releases the NETCONF session's own
// subsystem channel AND the underlying *remoteexec.Conn it was opened
// on, matching internal/catalog/net/{cli,ios}'s identically-named,
// independently-declared types.
//
// It closes the stream rather than sending <close-session/>, and that is
// deliberate: Close here runs from a defer that also fires on the error
// path, where the task's context may already be cancelled, and an RPC
// issued on a dead context would replace a real error with a confusing
// one. netconf.Session.Close is the polite variant, for a caller that
// has a live context and wants one.
type sessionAndConn struct {
	*netconf.Session
	sub  *remoteexec.Subsystem
	conn *remoteexec.Conn
}

// Close releases the subsystem channel and then the connection, in that
// order: the channel is layered on the connection, so closing the
// connection first would tear the channel down underneath its own
// goroutines rather than letting them be reaped.
//
// The subsystem's error is preferred when both fail, because it is the
// one closer to whatever actually went wrong.
func (s *sessionAndConn) Close() error {
	subErr := s.sub.Close()
	connErr := s.conn.Close()
	if subErr != nil {
		return subErr
	}
	return connErr
}
