// The "wait.restconf" method: waiting until a device's RESTCONF server
// answers an authenticated read under its root, which on a network device can
// be minutes after its HTTPS answers at all.
//
// It is a method of its own, as wait.netconf is, because what it needs of a
// device differs from wait.connection's and wait.netconf's, and a manifest
// says what a method needs for every call: this one only a device serving
// RESTCONF (RestconfCapable), over the restconf transport. It shares their
// timing parameters and loop (readConnection, waitForConnection), so the
// three pace their tries the same way.
package wait

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// restconfFQCN is this method's name, which prefixes its errors.
const restconfFQCN = "wait.restconf"

// The stats naming the root that answered, and how it was learned.
const (
	restconfStatRoot       = "root"
	restconfStatRootSource = "root_source"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: restconfFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportRestconf},
			RequiredCapabilities: []capability.Name{capability.NameRestconf},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion: ">=0.4.0",
			Status:        collection.StatusImplemented,
			// It observes and never acts, so a rollback has nothing to move
			// back, and it never emits an inverse.
			NoExistsReason: collection.NoExistsWait,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "This method waits for a device's RESTCONF server to answer and changes nothing on it, so there is nothing to undo.",
				ReadOnly:   true,
			},
			NoBecomeReason: "it speaks RESTCONF over HTTPS to the device's own API, which no sudo prefix reaches",
			NoCheckReason: "what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: restconfDoc,
		},
		Invoke: Restconf,
	})
}

// Restconf implements "wait.restconf": it tries the device's RESTCONF server
// on wait.connection's schedule until an authenticated read under its root
// answers.
func Restconf(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readConnection(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", restconfFQCN, err)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", restconfFQCN)
	}
	if _, ok := device.(capability.RestconfCapable); !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q serves no RESTCONF (it does not implement %s)", restconfFQCN, device.Name(), capability.NameRestconf)
	}
	var root, source string
	var warnings []string
	tried := false
	try := func(ctx context.Context) error {
		answered, how, warned, err := restconfTry(ctx, rc, device)
		if !tried {
			// The warnings are the device record's own (its TLS settings), the
			// same on every try, so they are recorded once.
			tried, warnings = true, warned
		}
		if err == nil {
			root, source = answered, how
		}
		return err
	}
	waited, err := waitForConnection(ctx, req, try, connectionPace{jitter: connectionJitter, sleep: waitSleep})
	if werr := sdk.RecordWarnings(rc, warnings); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: the RESTCONF server of %s did not answer: %w", restconfFQCN, device.Name(), err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{restconfStatRoot: root})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", restconfFQCN, err)
	}
	for key, value := range map[string]string{restconfStatRoot: root, restconfStatRootSource: source} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", restconfFQCN, err)
		}
	}
	for _, stat := range []struct {
		key   string
		value int
	}{
		{waitStatElapsed, int(waited.elapsed.Seconds())},
		{connectionStatTries, waited.tries},
		{connectionStatFailures, waited.failures},
	} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", restconfFQCN, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// restconfTry is one try at the server: a new client (pkg/restconf.ClientFor,
// so the device's own TLS settings and credential, no proxy, no redirect),
// host-meta for the root (read with no credential; on a 404 or a redirect,
// never followed, the conventional
// /restconf, which the capability read then decides), then the capability
// list under it, and the client's connections dropped after. It returns the
// root that answered, "/" for one at the origin itself, and how it was
// learned, and the warnings the device's record gives (a pinned certificate,
// a weak TLS setting allowed), as net.restconf.* record them.
//
// The certificate is checked in the handshake, before any request is sent, so
// one the device's settings do not trust ends the wait having sent nothing;
// a refused login ends it too (pkg/restconf's RefusedError, after which the
// client sends nothing more). Anything else is one more failed try.
func restconfTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem) (string, string, []string, error) {
	client, warnings, err := restconf.ClientForContext(ctx, device, device.Name(), rc.InjectSecrets())
	if err != nil {
		// The record itself is unusable, which no try changes.
		return "", "", nil, &stopWaiting{err: err}
	}
	defer client.Close()
	root, err := client.DiscoverRoot(ctx)
	if err == nil {
		_, err = client.Capabilities(ctx)
	}
	if err != nil {
		return "", "", warnings, restconfTryFailed(err)
	}
	if root == "" {
		root = "/"
	}
	return root, client.RootSource(), warnings, nil
}

// restconfTryFailed sorts a failed try: a certificate the device's settings
// do not trust and a refused login stop the wait; every other failure is
// tried again.
func restconfTryFailed(err error) error {
	var untrusted *devicetls.UntrustedError
	var refused *restconf.RefusedError
	switch {
	case errors.As(err, &untrusted):
		return &stopWaiting{err: fmt.Errorf("the RESTCONF server answered with a certificate the device's TLS settings do not trust, so no request was sent and the wait stops here; "+
			"check it with the device's owner, then pin it as %s (or trust its authority with %s): %w", devicetls.FingerprintProperty, devicetls.CAPEMProperty, err)}
	case errors.As(err, &refused):
		return &stopWaiting{err: fmt.Errorf("the RESTCONF server refused this login, so the wait stops here rather than send it again: %w", err)}
	}
	return err
}
