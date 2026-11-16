// The "wait.gnmi" method: waiting until a device's gNMI server answers its
// Capabilities with the device's credential, which on a network device can be
// minutes after its management port accepts a connection at all.
//
// It is a method of its own, as wait.netconf and wait.restconf are, because a
// manifest says what a method needs for every call: this one only a device
// serving gNMI (GNMICapable), over the gnmi transport. It shares their timing
// parameters and loop (readConnection, waitForConnection), so the waits pace
// their tries the same way.
package wait

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gnmiFQCN is this method's name, which prefixes its errors.
const gnmiFQCN = "wait.gnmi"

// gnmiStatVersion is the stat naming the gNMI version that answered.
const gnmiStatVersion = "version"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: gnmiFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportGNMI},
			RequiredCapabilities: []capability.Name{capability.NameGNMI},
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
				Notes:      "This method waits for a device's gNMI server to answer and changes nothing on it, so there is nothing to undo.",
				ReadOnly:   true,
			},
			NoBecomeReason: "it speaks gNMI over gRPC to the device's own server, which no sudo prefix reaches",
			NoCheckReason: "what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: gnmiDoc,
		},
		Invoke: GNMI,
	})
}

// GNMI implements "wait.gnmi": it tries the device's gNMI server on
// wait.connection's schedule until its Capabilities answers.
func GNMI(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readConnection(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", gnmiFQCN, err)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", gnmiFQCN)
	}
	if _, ok := device.(capability.GNMICapable); !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q serves no gNMI (it does not implement %s)", gnmiFQCN, device.Name(), capability.NameGNMI)
	}
	var version string
	var warnings []string
	tried := false
	try := func(ctx context.Context) error {
		answered, warned, err := gnmiTry(ctx, rc, device)
		if !tried {
			// The warnings are the device record's own (its TLS settings, a
			// plaintext connection), the same on every try, so they are
			// recorded once.
			tried, warnings = true, warned
		}
		if err == nil {
			version = answered
		}
		return err
	}
	waited, err := waitForConnection(ctx, req, try, connectionPace{jitter: connectionJitter, sleep: waitSleep})
	if werr := sdk.RecordWarnings(rc, warnings); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: the gNMI server of %s did not answer: %w", gnmiFQCN, device.Name(), err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{gnmiStatVersion: version})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", gnmiFQCN, err)
	}
	if err := rc.SetStat(gnmiStatVersion, version); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", gnmiFQCN, err)
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
			return collection.Result{}, fmt.Errorf("%s: %w", gnmiFQCN, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// gnmiTry is one try at the server: a new client (pkg/gnmi.ClientForContext, so the
// device's own TLS settings and credential, no proxy), its Capabilities, and
// the client closed after, so a connection from an earlier try is never taken
// for an answer. It returns the gNMI version that answered and the warnings
// the device's record gives, as net.gnmi.* record them.
//
// The certificate is checked in the handshake, before any call is sent, so
// one the device's settings do not trust ends the wait having sent nothing;
// so does a server refusing the client certificate, or its absence
// (devicetls.ClientCertificateRefusedError, which a TLS 1.3 server sends just
// after the handshake, as SONiC's does); a refused login ends it too
// (pkg/gnmi's RefusedError, after which the client sends nothing more).
// Anything else is one more failed try.
func gnmiTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem) (string, []string, error) {
	client, warnings, err := gnmi.ClientForContext(ctx, device, device.Name(), rc.InjectSecrets())
	if err != nil {
		// The record itself is unusable, which no try changes.
		return "", nil, &stopWaiting{err: err}
	}
	defer func() { _ = client.Close() }()
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return "", warnings, gnmiTryFailed(err)
	}
	return caps.Version, warnings, nil
}

// gnmiTryFailed sorts a failed try: a certificate the device's settings do
// not trust, a client certificate the server refuses (or the lack of one) and
// a refused login stop the wait; every other failure is tried again.
func gnmiTryFailed(err error) error {
	var untrusted *devicetls.UntrustedError
	var clientCert *devicetls.ClientCertificateRefusedError
	var refused *gnmi.RefusedError
	switch {
	case errors.As(err, &untrusted):
		return &stopWaiting{err: fmt.Errorf("the gNMI server answered with a certificate the device's TLS settings do not trust, so no call was sent and the wait stops here; "+
			"check it with the device's owner, then pin it as %s (or trust its authority with %s): %w", devicetls.FingerprintProperty, devicetls.CAPEMProperty, err)}
	case errors.As(err, &clientCert):
		return &stopWaiting{err: fmt.Errorf("the gNMI server refused the connection over its client certificate, which asking again does not change, so the wait stops here: %w", err)}
	case errors.As(err, &refused):
		return &stopWaiting{err: fmt.Errorf("the gNMI server refused this login, so the wait stops here rather than send it again: %w", err)}
	}
	return err
}
