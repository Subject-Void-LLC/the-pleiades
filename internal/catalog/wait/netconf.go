// The "wait.netconf" method: waiting until a device's NETCONF listener
// answers, which on a network device can be minutes after the device
// reports booted and answers SSH on its own port.
//
// It is a method of its own rather than a parameter of wait.connection
// because the two need different things of a device, and a manifest says
// what a method needs for every call: wait.connection reaches any
// addressable device over SSH or WinRM, and this one only a device with a
// NETCONF listener (NetconfCapable), over the netconf transport. A
// parameter choosing the listener would leave validation unable to refuse
// that call on a device with none until it ran. It shares wait.connection's
// timing parameters and loop (readConnection, waitForConnection), so the two
// pace their tries the same way.
package wait

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// netconfFQCN is this method's name, which prefixes its errors.
const netconfFQCN = "wait.netconf"

// netconfStatPort is the stat naming the port whose listener answered.
const netconfStatPort = "port"

// defaultNetconfPort is RFC 6242's NETCONF over SSH port, tried when a
// device's NetconfPort names none.
const defaultNetconfPort = 830

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: netconfFQCN,
		Manifest: collection.Manifest{
			SupportedTransports: []string{capability.TransportNetconf},
			RequiredCapabilities: []capability.Name{
				capability.NameNetconf,
				capability.NameSSHTransport,
			},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion: ">=0.2.0",
			Status:        collection.StatusImplemented,
			// It observes and never acts, so a rollback has nothing to move
			// back, and it never emits an inverse.
			NoExistsReason: collection.NoExistsWait,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "This method waits for a device's NETCONF listener to answer and changes nothing on it, so there is nothing to undo.",
				ReadOnly:   true,
			},
			NoBecomeReason: "it speaks NETCONF over an SSH subsystem, which no sudo prefix reaches, and its question is whether this login gets in",
			NoCheckReason: "what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: netconfDoc,
		},
		Invoke: Netconf,
	})
}

// Netconf implements "wait.netconf": it tries the device's NETCONF listener
// on wait.connection's schedule until a session opens there.
func Netconf(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readConnection(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", netconfFQCN, err)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", netconfFQCN)
	}
	listener, ok := device.(capability.NetconfCapable)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q has no NETCONF listener (it does not implement %s)", netconfFQCN, device.Name(), capability.NameNetconf)
	}
	port := listener.NetconfPort()
	if port == 0 {
		port = defaultNetconfPort
	}
	// Host key verification cannot be skipped here: the parameter is not
	// declared, and is dropped in case a caller passes it anyway, so a
	// credential only ever goes to a listener whose key known_hosts holds.
	connectParams := maps.Clone(params)
	delete(connectParams, sdk.ParamInsecureSkipHostKeyVerify)
	try := func(ctx context.Context) error { return netconfTry(ctx, rc, device, connectParams, port) }
	waited, err := waitForConnection(ctx, req, try, connectionPace{jitter: connectionJitter, sleep: waitSleep})
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: the NETCONF listener of %s on port %d did not answer: %w", netconfFQCN, device.Name(), port, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{netconfStatPort: port})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", netconfFQCN, err)
	}
	for _, stat := range []struct {
		key   string
		value int
	}{
		{netconfStatPort, port},
		{waitStatElapsed, int(waited.elapsed.Seconds())},
		{connectionStatTries, waited.tries},
		{connectionStatFailures, waited.failures},
	} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", netconfFQCN, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// netconfTry is one try at the listener: a fresh login at port (never a
// session borrowed from the run's pool), the netconf subsystem, and the
// server's hello read through pkg/netconf. The SSH handshake checks the
// listener's host key before the credential is sent, so a key known_hosts
// does not hold ends the wait having sent nothing. A listener that is not
// serving yet, at any step, is one more failed try.
func netconfTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, port int) error {
	conn, err := sdk.ConnectFreshPort(ctx, rc, device, params, netconfFQCN, port)
	if err != nil {
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			// The handshake reached the key, so the listener is up: what is
			// left is trusting its key, which no number of tries changes.
			return &stopWaiting{err: fmt.Errorf("the NETCONF listener answered, but known_hosts holds no key for it, so no credential was sent and the wait stops here; "+
				"trust it with pleiades trust-host %s --first-connect --netconf, then run the task again: %w", device.Name(), err)}
		}
		return sshTryFailed(err)
	}
	defer func() { _ = conn.Close() }() // a try's connection has nothing left to report once it answered
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		return err
	}
	session, err := netconf.Open(ctx, sub, netconf.Options{})
	if err != nil {
		_ = sub.Close()
		return err
	}
	// The hello is the answer; a polite close-session the device does not
	// acknowledge leaves it no less ready.
	_ = session.Close(ctx)
	return nil
}
