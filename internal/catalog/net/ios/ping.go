package ios

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.ping",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CiscoIOSCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// Read-only: it sends one exec-mode ping, built from parameters refused if they hold whitespace, and never enters configuration mode, so a check runs it for real.
			SupportsCheck: true,
			// A ping sends ICMP echoes and reads the replies. It
			// changes nothing on the device, so there is nothing an
			// inverse could undo.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A ping is read-only: it sends echoes and reports what came back, changing nothing on the device, so there is nothing to undo.",
			},
			Doc: collection.Doc{
				Summary:     "Runs a ping from a Cisco IOS device and reports the result.",
				Description: "Answers a different question from net.ssh.ping, and the difference is the point: net.ssh.ping proves this platform can reach the device, while this method proves the DEVICE can reach somewhere else, which is the question that actually matters when a routing or ACL change is under review. Runs IOS's own ping from an interactive PTY session and parses its \"Success rate is N percent (rx/tx)\" line, including the trailing \"round-trip min/avg/max = a/b/c ms\" clause that IOS omits entirely when nothing came back. Nothing is changed on the device, so this always reports no change. Use state to turn the result into a gate: state present (the default) fails the task when every packet is lost, and state absent fails it when anything answers, so a runbook can assert reachability or its absence without a separate condition.",
				Params: []collection.Param{
					{Name: "dest", Type: "string", Required: true, Description: "The address or hostname to ping from the device."},
					{Name: "count", Type: "int", Default: "5", Description: "How many echoes to send, passed to IOS as \"repeat\"."},
					{Name: "source", Type: "string", Description: "Source address or interface for the ping, passed to IOS as \"source\"."},
					{Name: "vrf", Type: "string", Description: "VRF to ping from, passed to IOS as \"vrf\"."},
					{Name: "state", Type: "string", Default: "present", Description: "\"present\" fails the task if the destination is unreachable (0 percent success); \"absent\" fails it if the destination answers at all. Set neither expectation by using a when condition on the returned facts instead."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "packet_loss", Type: "string", Returned: "always", Description: "Percentage of packets lost, as a string with a trailing percent sign, e.g. \"0%\"."},
					{Name: "packets_tx", Type: "int", Returned: "always", Description: "How many echoes the device sent."},
					{Name: "packets_rx", Type: "int", Returned: "always", Description: "How many replies the device received."},
					{Name: "rtt", Type: "map", Returned: "when at least one packet returned", Description: "Round-trip times in milliseconds: min, avg, max. Absent entirely when every packet was lost, because IOS prints no round-trip clause in that case."},
				},
				Examples: []collection.Example{
					{Name: "Assert the device can still reach its gateway", RunbookYAML: "- name: Confirm the upstream gateway answers\n  net.ios.ping:\n    dest: 192.0.2.1\n"},
					{Name: "Record reachability without failing the run", RunbookYAML: "- name: Measure reachability to a peer\n  net.ios.ping:\n    dest: 198.51.100.10\n    count: 10\n    state: absent\n  register: peer\n"},
				},
				SeeAlso: []string{"net.ssh.ping", "net.ios.facts"},
			},
		},
		Invoke: Ping,
		// Ping itself, since it only reads.
		Check: Ping,
	})
}

const (
	paramDest   = "dest"
	paramCount  = "count"
	paramSource = "source"
	paramVRF    = "vrf"
	paramState  = "state"
)

const (
	statPacketLoss = "packet_loss"
	statPacketsTx  = "packets_tx"
	statPacketsRx  = "packets_rx"
	statRTT        = "rtt"
)

const (
	statePresent = "present"
	stateAbsent  = "absent"
)

const defaultPingCount = 5

// rePingResult matches IOS's own summary line, in both the shapes a real
// device produces. With replies:
//
//	Success rate is 100 percent (3/3), round-trip min/avg/max = 1/1/1 ms
//
// With none, where IOS omits the round-trip clause ENTIRELY rather than
// printing zeroes:
//
//	Success rate is 0 percent (0/5)
//
// That second shape is the one a parser gets wrong, and it is why the
// round-trip group is optional here rather than assumed present. Both
// were captured from a real Cisco IOS XE device by
// pkg/netcli/live_probe_test.go's TestLiveIOSFactsShapes.
var rePingResult = regexp.MustCompile(`Success rate is (\d+) percent \((\d+)/(\d+)\)(?:, round-trip min/avg/max = (\d+)/(\d+)/(\d+) ms)?`)

// Ping implements the "net.ios.ping" collection method.
func Ping(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.ping"

	dest, err := sdk.RequiredStringParam(params, paramDest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	count, present, err := sdk.IntParam(params, paramCount)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !present {
		count = defaultPingCount
	}
	if count < 1 {
		return collection.Result{}, fmt.Errorf("%s: %s must be at least 1, got %d", fqcn, paramCount, count)
	}

	state := strings.TrimSpace(strings.ToLower(sdk.StringParam(params, paramState)))
	if state == "" {
		state = statePresent
	}
	if state != statePresent && state != stateAbsent {
		return collection.Result{}, fmt.Errorf("%s: %s must be %q or %q, got %q", fqcn, paramState, statePresent, stateAbsent, state)
	}

	line, err := buildPingCommand(dest, sdk.StringParam(params, paramSource), sdk.StringParam(params, paramVRF), count, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	out, err := session.Command(ctx, line)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// A rejected ping (a bad VRF name, an unresolvable host) prints an
	// IOS "% ..." error and no summary line at all. netcli.Session.Command
	// does not check the dialect's error pattern itself, only Config does,
	// so this method checks it rather than reporting a parse failure for
	// what is really a refused command.
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return collection.Result{}, fmt.Errorf("%s: device rejected %q: %s", fqcn, line, strings.TrimSpace(firstErrorLine(out)))
	}

	m := rePingResult.FindStringSubmatch(out)
	if m == nil {
		return collection.Result{}, fmt.Errorf("%s: could not find a success-rate line in the device's reply to %q", fqcn, line)
	}

	successRate, _ := strconv.Atoi(m[1])
	rx, _ := strconv.Atoi(m[2])
	tx, _ := strconv.Atoi(m[3])

	stats := []struct {
		key   string
		value any
	}{
		{statPacketLoss, strconv.Itoa(100-successRate) + "%"},
		{statPacketsTx, tx},
		{statPacketsRx, rx},
	}
	for _, s := range stats {
		if err := rc.SetStat(s.key, s.value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	// m[4] is set only when the optional round-trip clause matched, which
	// is exactly when at least one packet returned.
	if m[4] != "" {
		min, _ := strconv.Atoi(m[4])
		avg, _ := strconv.Atoi(m[5])
		max, _ := strconv.Atoi(m[6])
		if err := rc.SetStat(statRTT, map[string]any{"min": min, "avg": avg, "max": max}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	switch {
	case state == statePresent && rx == 0:
		return collection.Result{}, fmt.Errorf("%s: %s is unreachable from this device: %d of %d packets returned", fqcn, dest, rx, tx)
	case state == stateAbsent && rx > 0:
		return collection.Result{}, fmt.Errorf("%s: %s answered but state is %q: %d of %d packets returned", fqcn, dest, stateAbsent, rx, tx)
	}

	return collection.Result{Changed: false}, nil
}

// buildPingCommand assembles IOS's own ping syntax, in IOS's own order:
// "ping [vrf NAME] DEST [repeat N] [source ADDR]". vrf really does come
// before the destination and the rest really does come after it, so this
// is not a free ordering.
//
// Every caller-supplied value is refused if it carries whitespace. That
// is the whole injection defence and it is sufficient here: the transport
// underneath already refuses an embedded newline (the only way to submit
// a second command to a CLI), and rejecting whitespace stops a value from
// smuggling an extra ping ARGUMENT into this one command.
func buildPingCommand(dest, source, vrf string, count int, fqcn string) (string, error) {
	for _, f := range []struct{ name, value string }{
		{paramDest, dest},
		{paramSource, source},
		{paramVRF, vrf},
	} {
		if f.value != "" && strings.ContainsAny(f.value, " \t\r\n") {
			return "", fmt.Errorf("%s: %s must not contain whitespace, got %q", fqcn, f.name, f.value)
		}
	}

	var b strings.Builder
	b.WriteString("ping")
	if vrf != "" {
		b.WriteString(" vrf ")
		b.WriteString(vrf)
	}
	b.WriteString(" ")
	b.WriteString(dest)
	b.WriteString(" repeat ")
	b.WriteString(strconv.Itoa(count))
	if source != "" {
		b.WriteString(" source ")
		b.WriteString(source)
	}
	return b.String(), nil
}

// firstErrorLine returns the first line matching IOS's "% ..." error
// convention, so an error message quotes what the device actually
// objected to rather than the whole reply.
func firstErrorLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "%") {
			return line
		}
	}
	return out
}
