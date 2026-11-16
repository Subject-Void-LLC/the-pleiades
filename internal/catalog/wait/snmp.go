// The "wait.snmp" method: waiting until a device's SNMP agent answers its
// credential, which on a device just started can be well after it answers a
// ping. An agent that is up and refuses the credential (an SNMPv3 report, which
// net-snmp sends unauthenticated for a wrong passphrase or an unknown user)
// ends the wait at once rather than being asked again, so a wait is not a way
// to wait out an SNMPv3 user still being configured; a community the agent does
// not know yet is silence, and is waited through.
//
// It is a method of its own, as wait.netconf and wait.restconf are, because a
// manifest says what a method needs for every call: this one only a device
// reached by SNMP (SNMPCapable), over the snmp transport, and the SNMP family
// of its credential (UsesSNMPCredential). It shares their timing parameters
// and loop (readConnection, waitForConnection).
package wait

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// snmpFQCN is this method's name, which prefixes its errors.
const snmpFQCN = "wait.snmp"

// The stats naming what answered.
const (
	snmpStatVersion = "version"
	snmpStatPort    = "port"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: snmpFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSNMP},
			RequiredCapabilities: []capability.Name{capability.NameSNMP},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion:      ">=0.4.0",
			Status:             collection.StatusImplemented,
			UsesSNMPCredential: true,
			NoExistsReason:     collection.NoExistsWait,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "This method waits for a device's SNMP agent to answer and changes nothing on it, so there is nothing to undo.",
				ReadOnly:   true,
			},
			NoBecomeReason: "it speaks SNMP over UDP to the device's agent, which no sudo prefix reaches",
			NoCheckReason: "what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: snmpDoc,
		},
		Invoke: SNMP,
	})
}

// SNMP implements "wait.snmp": it tries the device's agent on
// wait.connection's schedule until a GET of sysUpTime.0 is answered.
func SNMP(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readConnection(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", snmpFQCN, err)
	}
	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device", snmpFQCN)
	}
	agent, ok := device.(capability.SNMPCapable)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q has no SNMP agent (it does not implement %s)", snmpFQCN, device.Name(), capability.NameSNMP)
	}
	// SNMP is UDP, which a route of SSH hops does not carry, so a try would
	// reach the agent some other way than its record says, or not at all;
	// net.snmp.* refuses such a device the same way.
	if r, routed := rc.(sdk.Router); routed && r.DeviceRoute() != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q is reached through a route of SSH hops, which carries no UDP, so its SNMP agent is not reached through it", snmpFQCN, device.Name())
	}
	var version snmp.Version
	var warnings []string
	tried := false
	try := func(ctx context.Context) error {
		answered, warned, err := snmpTry(ctx, rc, device)
		if !tried {
			// The warnings are the device record's own, the same on every
			// try, so they are recorded once.
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
		return collection.Result{}, fmt.Errorf("%s: the SNMP agent of %s did not answer: %w", snmpFQCN, device.Name(), err)
	}
	if err := sdk.RecordDiff(rc, sdk.Unchanged(map[string]any{snmpStatPort: agent.SNMPPort()})); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", snmpFQCN, err)
	}
	for _, stat := range []struct {
		key   string
		value any
	}{
		{snmpStatVersion, string(version)},
		{snmpStatPort, agent.SNMPPort()},
		{waitStatElapsed, int(waited.elapsed.Seconds())},
		{connectionStatTries, waited.tries},
		{connectionStatFailures, waited.failures},
	} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", snmpFQCN, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// snmpTry is one try at the agent: a new client (pkg/snmp.ClientFor, the
// device's own settings and SNMP credential), one GET of sysUpTime.0 bounded
// by the try's own time (ctx, connect_timeout), and the client closed after. It
// returns the version that answered and the warnings the device's record
// gives.
//
// A record or credential the client refuses ends the wait, since no try
// changes it, and so does an SNMPv3 refusal (an unknown user, a wrong digest,
// a level the user lacks, a message the agent could not decrypt), since
// sending the credential again can lock it out. A socket that could not be
// opened (a host name that does not resolve yet, as a device's name a DHCP
// lease registers as it boots) is one more failed try, and so is silence, a
// wrong community included: an SNMPv1 or SNMPv2c agent drops such a request
// without a word, so it reads as an agent not up yet until the timeout.
func snmpTry(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem) (snmp.Version, []string, error) {
	client, warnings, err := snmp.ClientFor(device, device.Name(), rc.InjectSecrets())
	var dial *snmp.DialError
	switch {
	case errors.As(err, &dial):
		return "", nil, err
	case err != nil:
		return "", nil, &stopWaiting{err: err}
	}
	defer func() { _ = client.Close() }()
	got, err := client.Get(ctx, []string{snmp.OIDSysUpTime})
	var refused *snmp.RefusedError
	switch {
	case errors.As(err, &refused):
		return "", warnings, &stopWaiting{err: fmt.Errorf("the agent refused this credential, so the wait stops here rather than send it again: %w", err)}
	case err != nil:
		return "", warnings, err
	case len(got) != 1 || got[0].Type.Absent():
		return "", warnings, errors.New("the agent answered, and holds no sysUpTime.0 for this credential")
	}
	return client.Version(), warnings, nil
}
