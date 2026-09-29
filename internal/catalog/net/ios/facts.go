package ios

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.facts",
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
			// Read-only: it sends only show commands (show version, show inventory, show ip interface brief) and never enters configuration mode, so a check runs it for real.
			SupportsCheck: true,
			// A read-only fact gatherer changes nothing, so there is
			// nothing to undo. Stated the same way
			// net.catalyst.device_facts states it, rather than left
			// unanswered: collection.Register refuses an implemented
			// method that claims no reversibility without saying why.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc: collection.Doc{
				Summary:     "Gathers structured facts from a Cisco IOS device over its CLI.",
				Description: "Closes a real gap: facts.gather requires FactGathererCapable, which no Cisco device type declares, so before this method a Cisco device could be commanded and configured but never described. Opens an interactive PTY session over SSH using netcli.IOS's own paging and prompt conventions, runs the read-only show commands the requested subsets need, and emits what it parses through EmitFact rather than SetStat, the same choice facts.gather and net.catalyst.device_facts both make: a fact is long-lived drift data worth comparing across weeks, and a software version recorded as a stat answers nothing next month. Every parser in this method was written against output captured from a real Cisco IOS XE device rather than from documentation or memory. A field the device does not report is left out entirely rather than emitted as an empty string, so a condition can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed, so this always reports no change. Two of cisco.ios.ios_facts's own subsets are deliberately absent rather than accepted and ignored: config, because net.ios.config's own backup parameter already captures a running-config and doing it twice invites two answers, and hardware, because the memory and flash figures IOS reports vary enough by platform that parsing them generically would be a guess.",
				Params: []collection.Param{
					{Name: "gather_subset", Type: "list of string", Default: "[\"min\"]", Description: "Which subsets to gather: \"min\" (hostname, version, model, serial number, image, uptime, from \"show version\" and \"show inventory\"), \"interfaces\" (from \"show ip interface brief\"), or \"all\" for both. An unrecognized subset is refused rather than skipped."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "ansible_net_hostname", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The device's own hostname, read from the \"<hostname> uptime is ...\" line of \"show version\"."},
					{Name: "ansible_net_version", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The IOS XE version string, e.g. \"17.15.04c\"."},
					{Name: "ansible_net_model", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The platform model, e.g. \"C8000V\"."},
					{Name: "ansible_net_serialnum", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The chassis serial number, preferring \"show inventory\"'s Chassis SN and falling back to \"show version\"'s Processor board ID."},
					{Name: "ansible_net_image", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "The running system image file, e.g. \"bootflash:packages.conf\"."},
					{Name: "ansible_net_uptime", Type: "string", Returned: "when the min subset is gathered and the device reports it", Description: "Uptime exactly as the device words it, e.g. \"1 hour, 32 minutes\". Not converted to seconds: IOS reports a rounded phrase, and parsing it into a precise number would invent precision the device never gave."},
					{Name: "ansible_net_interfaces", Type: "list of map", Returned: "when the interfaces subset is gathered", Description: "One entry per interface: name, ip_address (omitted when the device says \"unassigned\"), status, and protocol. Status is taken whole, so \"administratively down\" is reported as written rather than truncated at the first space."},
					{Name: "ansible_net_gather_subset", Type: "list of string", Returned: "always", Description: "The subsets actually gathered, after \"all\" is expanded."},
				},
				Examples: []collection.Example{
					{Name: "Gather a device's identity before deciding anything", RunbookYAML: "- name: Learn what this router is\n  net.ios.facts:\n  register: device\n"},
					{Name: "Gather interfaces as well", RunbookYAML: "- name: Learn the interface list too\n  net.ios.facts:\n    gather_subset:\n      - all\n"},
				},
				SeeAlso: []string{"facts.gather", "net.catalyst.device_facts", "net.ios.config"},
			},
		},
		Invoke: Facts,
		// Facts itself, since it only reads.
		Check: Facts,
	})
}

// Fact names mirror cisco.ios.ios_facts's own, so a converted playbook
// reading ansible_net_version keeps reading ansible_net_version. This is
// the same reuse-Ansible's-vocabulary reasoning internal/catalog/facts
// already follows for ansible_distribution and friends.
const (
	factHostname     = "ansible_net_hostname"
	factVersion      = "ansible_net_version"
	factModel        = "ansible_net_model"
	factSerialNum    = "ansible_net_serialnum"
	factImage        = "ansible_net_image"
	factUptime       = "ansible_net_uptime"
	factInterfaces   = "ansible_net_interfaces"
	factGatherSubset = "ansible_net_gather_subset"
)

const paramGatherSubset = "gather_subset"

const (
	subsetMin        = "min"
	subsetInterfaces = "interfaces"
	subsetAll        = "all"
)

// Every expression below was written against output captured from a real
// Cisco IOS XE device (17.15.04c on a Catalyst 8000V), not from Cisco's
// documentation and not from memory of what IOS output looks like. That
// distinction is not pedantry: Phase 86.5's own predecessor spec was
// fabricated from exactly such a memory (FAILURE_PATTERNS.md #202), and
// pkg/netcli/live_probe_test.go's TestLiveIOSFactsShapes is the
// re-runnable diagnostic that produced the samples these match.
var (
	// "Cat8kv uptime is 1 hour, 32 minutes" carries the hostname and the
	// uptime phrase in one line, which is why one expression takes both.
	reUptime = regexp.MustCompile(`(?m)^(\S+) uptime is (.+?)\s*$`)
	// "Cisco IOS XE Software, Version 17.15.04c". Deliberately anchored on
	// the IOS XE line rather than the second, differently-formatted
	// "Cisco IOS Software [IOSXE], ... Version 17.15.4c" line the same
	// output also carries: the two disagree ("17.15.04c" vs "17.15.4c"),
	// and the first is the one the platform reports as its version.
	reVersion = regexp.MustCompile(`(?m)^Cisco IOS XE Software, Version (\S+)\s*$`)
	// "cisco C8000V (VXE) processor (revision VXE) with ..."
	reModel = regexp.MustCompile(`(?m)^[Cc]isco (\S+) \([^)]*\) processor`)
	// "Processor board ID 9XLWRY40A2V", the fallback when show inventory
	// has no Chassis entry.
	reBoardID = regexp.MustCompile(`(?m)^Processor board ID (\S+)\s*$`)
	// `System image file is "bootflash:packages.conf"`
	reImage = regexp.MustCompile(`(?m)^System image file is "([^"]*)"`)
	// show inventory pairs a NAME line with a PID/VID/SN line beneath it.
	// SN can be entirely blank on a real device (the F0 module reports
	// "SN:" followed by only spaces), so the capture is deliberately
	// permissive and the caller discards an empty result.
	reInventoryName = regexp.MustCompile(`(?m)^NAME: "([^"]*)"`)
	reInventorySN   = regexp.MustCompile(`(?m)^PID:.*?,\s*VID:.*?,\s*SN:\s*(\S*)\s*$`)
)

// Facts implements the "net.ios.facts" collection method.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.facts"

	subsets, err := resolveSubsets(params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	facts := map[string]any{}

	if subsets[subsetMin] {
		version, err := session.Command(ctx, "show version")
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: show version: %w", fqcn, err)
		}
		inventoryOut, err := session.Command(ctx, "show inventory")
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: show inventory: %w", fqcn, err)
		}
		for k, v := range parseVersion(version) {
			facts[k] = v
		}
		// show inventory's Chassis serial is the authoritative one; the
		// board ID parsed above stands in only when it is absent.
		if sn := chassisSerial(inventoryOut); sn != "" {
			facts[factSerialNum] = sn
		}
	}

	if subsets[subsetInterfaces] {
		brief, err := session.Command(ctx, "show ip interface brief")
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: show ip interface brief: %w", fqcn, err)
		}
		facts[factInterfaces] = parseInterfaces(brief)
	}

	facts[factGatherSubset] = sortedSubsets(subsets)

	for _, name := range sortedKeys(facts) {
		if err := rc.EmitFact(name, facts[name]); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: false}, nil
}

// resolveSubsets validates gather_subset and expands "all". An
// unrecognized subset is refused rather than skipped, so a typo surfaces
// as an error naming the value instead of as facts that quietly never
// appear.
func resolveSubsets(params map[string]any, fqcn string) (map[string]bool, error) {
	requested, present, err := sdk.StringSlice(params, paramGatherSubset)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !present || len(requested) == 0 {
		requested = []string{subsetMin}
	}

	out := map[string]bool{}
	for _, name := range requested {
		switch strings.TrimSpace(strings.ToLower(name)) {
		case subsetMin:
			out[subsetMin] = true
		case subsetInterfaces:
			out[subsetInterfaces] = true
		case subsetAll:
			out[subsetMin] = true
			out[subsetInterfaces] = true
		default:
			return nil, fmt.Errorf("%s: %s %q is not one of %q, %q, %q", fqcn, paramGatherSubset, name, subsetMin, subsetInterfaces, subsetAll)
		}
	}
	return out, nil
}

// parseVersion pulls what it can out of "show version". A field the
// device does not report is left out of the map entirely rather than set
// to an empty string, so a later condition can tell "this device does not
// say" from "this device says nothing", the same contract
// internal/catalog/facts's own gatherer makes.
func parseVersion(out string) map[string]any {
	facts := map[string]any{}
	if m := reUptime.FindStringSubmatch(out); m != nil {
		facts[factHostname] = m[1]
		facts[factUptime] = m[2]
	}
	if m := reVersion.FindStringSubmatch(out); m != nil {
		facts[factVersion] = m[1]
	}
	if m := reModel.FindStringSubmatch(out); m != nil {
		facts[factModel] = m[1]
	}
	if m := reBoardID.FindStringSubmatch(out); m != nil {
		facts[factSerialNum] = m[1]
	}
	if m := reImage.FindStringSubmatch(out); m != nil {
		facts[factImage] = m[1]
	}
	return facts
}

// chassisSerial returns the serial number of the entry named "Chassis"
// in "show inventory" output, or "" when there is none. A real device
// lists several entries, only one of which describes the chassis, and at
// least one of the others can carry an entirely blank serial.
func chassisSerial(out string) string {
	names := reInventoryName.FindAllStringSubmatchIndex(out, -1)
	for i, loc := range names {
		name := out[loc[2]:loc[3]]
		if !strings.EqualFold(name, "Chassis") {
			continue
		}
		end := len(out)
		if i+1 < len(names) {
			end = names[i+1][0]
		}
		if m := reInventorySN.FindStringSubmatch(out[loc[1]:end]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// parseInterfaces reads "show ip interface brief". It splits on fields
// rather than fixed columns, and takes the LAST field as the protocol
// with everything between the method and it as the status, because a real
// status can be two words ("administratively down") while a protocol is
// always one. A fixed-width or first-token parse silently truncates that
// case, which is exactly the sort of thing only real output reveals.
func parseInterfaces(out string) []map[string]any {
	interfaces := []map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[0] == "Interface" {
			continue
		}
		if fields[2] != "YES" && fields[2] != "NO" {
			continue
		}
		entry := map[string]any{
			"name":     fields[0],
			"status":   strings.Join(fields[4:len(fields)-1], " "),
			"protocol": fields[len(fields)-1],
		}
		// IOS writes "unassigned" where a real device has no address.
		// Reporting that string as an ip_address would make every
		// unaddressed interface look addressed to a CEL condition.
		if fields[1] != "unassigned" {
			entry["ip_address"] = fields[1]
		}
		interfaces = append(interfaces, entry)
	}
	return interfaces
}

func sortedSubsets(subsets map[string]bool) []string {
	out := make([]string, 0, len(subsets))
	for name, on := range subsets {
		if on {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
