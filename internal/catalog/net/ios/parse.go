// net.ios.parse: one show command on a Cisco IOS or IOS XE device, its
// output parsed by pkg/iosparse, a port of Cisco's genieparser parsers.
package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The stats net.ios.parse records.
const (
	// statParsed is the parsed result, in genieparser's shape.
	statParsed = "parsed"
	// statParser is the genieparser class whose port parsed it.
	statParser = "parser"
)

// parserArchiveDiffs is the parser whose output holds configuration
// lines, and so secrets, which are masked before anything reads them.
const parserArchiveDiffs = "ShowArchiveConfigDifferences"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.parse",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"telnet",
				"console",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameCiscoIOS,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// Read-only: every covered command is a show command, so a check
			// runs it for real.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "It runs one show command and changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc: collection.Doc{
				Summary:     "Runs a show command on a Cisco IOS or IOS XE device and returns its output parsed into Genie's schema.",
				Description: "Runs one show command over the same session net.ios.command opens and parses its output with a parser ported from Cisco's Genie (genieparser, Apache License 2.0), returning the same nested structure Genie returns for that command, so Genie's own schema documentation describes the result and a later task can compare two reads field by field, such as the state before a change and after its rollback. Each parser is held to every one of genieparser's own recorded device outputs and expected results for the command forms it covers. Output in which the parser finds nothing (no OSPF neighbors yet) is an empty result with empty true, not a failure, so a before and after can compare it; a command the device refuses (% BGP not active) fails the task. A command no parser covers is refused before the device is reached, naming the commands that are covered. It only reads, so it reports unchanged, and a check runs it as a real run does.",
				Params: []collection.Param{
					{Name: "command", Type: "string", Required: true, Description: "The show command to run and parse, one of the commands a parser covers (pleiades doc net.ios.parse lists them), for example \"show ip interface brief\" or \"show ip route\". IOS's abbreviations are not accepted: write the command in full."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
					{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
				},
				Returns: []collection.ReturnField{
					{Name: "parsed", Type: "dict", Returned: "always", Description: "The command's output in Genie's schema for it: for show ip interface brief, interface, keyed by interface name, each with ip_address, interface_is_ok, method, status and protocol."},
					{Name: "parser", Type: "string", Returned: "always", Description: "The Genie parser class whose port parsed it, such as ShowIpInterfaceBrief."},
					{Name: "empty", Type: "bool", Returned: "always", Description: "Whether the parser found nothing in the output, parsed then being empty."},
					{Name: "stdout", Type: "string", Returned: "always", Description: "The command's output as the device printed it, except that the secrets in show archive config differences (a password, secret, key or SNMP community) are masked, as net.ios.replace masks them, before it is parsed or recorded."},
				},
				Examples: []collection.Example{
					{Name: "Read the interfaces before a change, to compare after its rollback", RunbookYAML: "- name: Read the interfaces\n  net.ios.parse:\n    command: show ip interface brief\n  register: interfaces_before\n"},
				},
				SeeAlso: []string{"net.ios.command", "net.ios.facts", "net.ios.replace"},
			},
		},
		Invoke: Parse,
		// Parse itself, since it only reads.
		Check: Parse,
	})
}

// statEmpty says the parser found nothing in the output.
const statEmpty = "empty"

// Parse implements the "net.ios.parse" collection method: it refuses a
// command no parser covers before opening a session, runs the command,
// and records the output and its parse.
func Parse(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.parse"

	command, err := sdk.RequiredStringParam(params, paramCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	parser, err := iosparse.Covers(command)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	out, err := session.Command(ctx, command)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if parser == parserArchiveDiffs {
		out = maskDiff(out)
	}
	// A command the device refused printed an IOS error, not its output.
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return collection.Result{}, fmt.Errorf("%s: device rejected %q: %s", fqcn, command, strings.TrimSpace(firstErrorLine(out)))
	}
	if err := rc.SetStat(statCommandStdout, out); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	parser, parsed, err := iosparse.Parse(command, out)
	empty := errors.Is(err, iosparse.ErrEmpty)
	switch {
	case empty:
		// Nothing to report is a state a before and after can compare (no
		// OSPF neighbors yet), so it is an empty result, not a failure.
		parsed = map[string]any{}
	case err != nil:
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statEmpty, empty); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statParser, parser); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statParsed, parsed); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: false}, nil
}

// maskDiff masks the secrets in show archive config differences output.
// Each changed line carries a leading "+" or "-" after its indentation,
// which would hide the line's first word from pkg/netcfg's masking, so
// the markers are taken out, the whole text masked as one configuration
// (a key chain block spans lines), and each marker put back where it was:
// masking replaces words after a line's indentation and never the
// indentation itself.
func maskDiff(out string) string {
	// marker records, for each line, where its "+" or "-" stood and which
	// it was.
	type marker struct {
		at   int
		sign string
	}
	lines := strings.Split(out, "\n")
	markers := make([]*marker, len(lines))
	for i, line := range lines {
		body := strings.TrimLeft(line, " \t")
		if body != "" && (body[0] == '+' || body[0] == '-') {
			at := len(line) - len(body)
			markers[i] = &marker{at: at, sign: body[:1]}
			lines[i] = line[:at] + body[1:]
		}
	}
	// Mask keeps the line count, so each line still lines up with its
	// marker.
	masked := strings.Split(netcfg.Mask(strings.Join(lines, "\n")), "\n")
	for i, m := range markers {
		if m != nil && i < len(masked) && m.at <= len(masked[i]) {
			masked[i] = masked[i][:m.at] + m.sign + masked[i][m.at:]
		}
	}
	return strings.Join(masked, "\n")
}
