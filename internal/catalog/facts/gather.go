// Package facts implements the "facts.gather" namespaced Collection
// method, this platform's ansible.builtin.setup.
//
// This package's init registers "facts.gather" into the shared
// pkg/collection registry via collection.MustRegister. A runbook task
// naming this FQCN reaches engine.NewCollectionActionExecutor's real
// dispatch path (cmd/pleiades/run.go), which calls Gather below directly,
// since this Manifest's Status is StatusImplemented and Invoke is set.
//
// # Facts, not stats
//
// Everything this method learns goes out through sdk.RunbookContext's
// EmitFact and none of it through SetStat, and the two are not
// interchangeable. A stat is ephemeral: it exists for the rest of this
// run so a later task's condition can read it. A fact is long-lived drift
// data, kept so that "this device reported 8 GB last week and 4 GB today"
// is a question somebody can ask. A kernel version recorded as a stat
// answers nothing next month, which is the whole reason to gather it.
//
// Because this package lives under internal/, it is reachable only from
// code inside this module or a fork of it: Go's internal/ visibility rule
// blocks any other module from importing it at all. A third-party
// Collection arriving from outside this binary needs a separate,
// not-yet-built distribution mechanism (Part X's OCI distribution work),
// not this one.
package facts

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gatherParamFilter is ansible.builtin.setup's own name for the list of
// shell-style patterns narrowing which facts are gathered.
//
// Ansible's default is an empty list meaning everything, and that is kept
// exactly: an absent filter and "filter: []" both gather the whole set.
const gatherParamFilter = "filter"

// The fact keys this method emits, which are ansible.builtin.setup's own
// names so a converted playbook reading ansible_kernel keeps reading
// ansible_kernel.
//
// Every identifier in this package carries the gather prefix because a
// sibling method landing in this namespace later would share the package,
// and Go has no file-level scope to keep two files' names apart.
const (
	gatherFactHostname            = "ansible_hostname"
	gatherFactKernel              = "ansible_kernel"
	gatherFactArchitecture        = "ansible_architecture"
	gatherFactDistribution        = "ansible_distribution"
	gatherFactDistributionVersion = "ansible_distribution_version"
	gatherFactMemTotalMB          = "ansible_memtotal_mb"
	gatherFactProcessorCount      = "ansible_processor_count"
	gatherFactUptimeSeconds       = "ansible_uptime_seconds"
)

// gatherKilobytesPerMegabyte converts what /proc/meminfo reports into what
// ansible_memtotal_mb means. Ansible divides by 1024 and truncates, and
// this matches it rather than rounding, so the two agree on the same
// machine.
const gatherKilobytesPerMegabyte = 1024

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "facts.gather",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameFactGatherer,
			},
			// Nothing here needs root. Every source below is world
			// readable (uname, /etc/os-release, /proc), which is a real
			// property of the set that was chosen rather than luck: a fact
			// worth gathering on every device is one every device can be
			// asked for without elevation.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// Reading is not changing, so there is nothing to undo and no
			// run of this method will ever emit an inverse. That is the same
			// fact that makes Changed unconditionally false below.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Reading is not changing. This method runs uname and reads /etc/os-release and files under /proc, alters nothing " +
					"on the device, and so has nothing an undo could restore.",
				ReadOnly: true,
			},
			Doc: gatherDoc(),
			// Every command it sends is a fixed uname or cat (gatherProbes),
			// so a check runs it for real: reading is all a real run does.
			SupportsCheck: true,
		},
		Invoke: Gather,
		// Gather itself, since it only reads: its check answers with what
		// is on the device now, which is exactly what a real run would.
		Check: Gather,
	})
}

// gatherDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, which is the source
// the scaffolder is driven from, and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares the two for equality so
// the copies cannot drift.
func gatherDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Gathers baseline system facts from the target (OS, kernel, distribution).",
		Description: "Reads a small, fixed set of system facts over SSH and emits each one as a fact, so it is kept as drift data rather than as a value that expires at the end of the run. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed and no command is elevated, so this always reports no change. Three of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_subset has nothing to select between at this size, gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.",
		Params: []collection.Param{
			{Name: gatherParamFilter, Type: "list of string", Description: "Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so [\"ansible_distribution*\"] gathers the distribution and its version and nothing else. Leaving this out gathers everything, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: gatherFactHostname, Type: "string", Returned: "when the device reports one", Description: "The short host name, which is everything before the first dot of what uname reports."},
			{Name: gatherFactKernel, Type: "string", Returned: "when the device reports one", Description: "The kernel release, as uname -r reports it."},
			{Name: gatherFactArchitecture, Type: "string", Returned: "when the device reports one", Description: "The machine hardware name, as uname -m reports it, for example x86_64."},
			{Name: gatherFactDistribution, Type: "string", Returned: "when /etc/os-release names one", Description: "The distribution name, read from NAME in /etc/os-release, for example Ubuntu."},
			{Name: gatherFactDistributionVersion, Type: "string", Returned: "when /etc/os-release names one", Description: "The distribution version, read from VERSION_ID in /etc/os-release. A rolling release that publishes no VERSION_ID reports no such fact."},
			{Name: gatherFactMemTotalMB, Type: "int", Returned: "when /proc/meminfo reports it", Description: "Total usable memory in megabytes, from MemTotal in /proc/meminfo, truncated the way Ansible truncates it."},
			{Name: gatherFactProcessorCount, Type: "int", Returned: "when /proc/cpuinfo reports it", Description: "The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo. An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1."},
			{Name: gatherFactUptimeSeconds, Type: "int", Returned: "when /proc/uptime reports it", Description: "How long the device has been up, in whole seconds, from the first field of /proc/uptime."},
		},
		Examples: []collection.Example{
			{
				Name:        "Gather everything before deciding what to do",
				RunbookYAML: "- name: Learn what this device is\n  facts.gather:\n",
			},
			{
				Name:        "Gather only what a later condition reads",
				RunbookYAML: "- name: Learn which distribution this is\n  facts.gather:\n    filter:\n      - ansible_distribution*\n",
			},
		},
		SeeAlso: []string{"exec.command", "net.catalyst.device_facts"},
	}
}

// gatherFact is one key and value a probe managed to determine.
//
// A slice of these rather than a map, because a probe answering two facts
// would otherwise emit them in Go's randomized map order, and a method
// whose output order changes between runs is harder to read a log of than
// one whose does not.
type gatherFact struct {
	key   string
	value any
}

// gatherProbe is one command and the parser that turns its output into
// facts.
//
// keys lists every fact this probe can produce, which is what lets the
// filter drop a probe before it costs a round trip rather than after.
type gatherProbe struct {
	keys    []string
	command string
	parse   func(stdout string) []gatherFact
}

// gatherProbes is the fixed set of things this method reads, in the order
// it reads them.
//
// # One command per source, not one command for all of them
//
// Seven commands down one already-open connection costs seven round
// trips, and the alternative was one command printing seven lines that a
// parser split apart. That was rejected: a device missing /etc/os-release
// prints nothing for that line, every later line shifts up by one, and
// the parser then reports a kernel version as an architecture. One
// missing source would corrupt the rest instead of omitting itself, which
// is the worst failure shape available for data whose whole purpose is to
// be trusted later.
//
// # Parsing happens here, not on the device
//
// Every command is a plain read: uname, or cat. None of them pipes into
// awk or sed, so the remote toolchain this needs is what a BusyBox
// device already has, and the parsing lives in Go where it is testable
// rather than in a shell expression where a quoting mistake is silent.
// Every command is also a compile-time constant with nothing from the
// runbook in it, which is why no quoting of a task parameter appears
// anywhere in this file.
func gatherProbes() []gatherProbe {
	return []gatherProbe{
		{
			keys:    []string{gatherFactHostname},
			command: "uname -n",
			// uname reports the node name, which on a machine with a domain
			// is the full name. ansible_hostname is the short one and
			// ansible_fqdn is the full one, so cutting at the first dot is
			// what makes this fact mean what its name says. The full name is
			// not gathered under a second key: it would need a separate
			// resolver-dependent lookup, and a fact that sometimes resolves
			// and sometimes does not is worse than an absent one.
			parse: gatherSingle(gatherFactHostname, gatherShortName),
		},
		{
			keys:    []string{gatherFactKernel},
			command: "uname -r",
			parse:   gatherSingle(gatherFactKernel, nil),
		},
		{
			keys:    []string{gatherFactArchitecture},
			command: "uname -m",
			parse:   gatherSingle(gatherFactArchitecture, nil),
		},
		{
			keys:    []string{gatherFactDistribution, gatherFactDistributionVersion},
			command: "cat /etc/os-release",
			parse:   gatherOSRelease,
		},
		{
			keys:    []string{gatherFactMemTotalMB},
			command: "cat /proc/meminfo",
			parse:   gatherMemTotalMB,
		},
		{
			// The whole file rather than a grep for one field, because
			// grep's own absence is a failure this cannot tell apart from
			// the field's absence. It is the largest read here, growing
			// with the core count, and it is still a few tens of kilobytes
			// on a machine nobody would call small.
			keys:    []string{gatherFactProcessorCount},
			command: "cat /proc/cpuinfo",
			parse:   gatherProcessorCount,
		},
		{
			keys:    []string{gatherFactUptimeSeconds},
			command: "cat /proc/uptime",
			parse:   gatherUptimeSeconds,
		},
	}
}

// Gather implements the "facts.gather" collection method: it reads a
// fixed set of system facts over SSH and emits each one it could
// determine.
//
// # An exit status is not the answer here
//
// The output of each command is handed to its parser whatever the command
// exited with, and that is deliberate rather than sloppy. The parser is
// the only thing that can tell "this device told me" from "this device
// did not": a `hostname` that prints a name and then exits 1 has answered,
// and one that exits 0 and prints nothing has not. Reading the status
// instead would trust the weaker signal of the two, and would still need
// the parser's answer afterward.
//
// A transport failure is the opposite case and is fatal. A session that
// could not be opened says nothing about the device, so carrying on would
// emit a set of facts silently missing everything after the point the
// connection broke, and a fact set that is quietly partial is worse than a
// task that failed.
//
// It never reports Changed. Reading is not changing, and a fact gatherer
// that claimed otherwise would make every run look like it converged
// something.
func Gather(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "facts.gather"

	// Planned before the connection is opened, so a filter with a typo in
	// it costs no TCP connect, key exchange or authentication round and
	// names the runbook's mistake rather than the device's.
	probes, err := gatherPlan(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	for _, probe := range probes {
		result, err := conn.Run(ctx, probe.command)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, probe.command, err)
		}

		for _, fact := range probe.parse(result.Stdout) {
			// A probe answering two facts can survive a filter that wants
			// only one of them, so the keys are checked again here. Dropping
			// this would emit ansible_distribution_version to a task that
			// asked for ansible_distribution alone.
			if !gatherWanted(probe.keys, fact.key) {
				continue
			}
			if err := rc.EmitFact(fact.key, fact.value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, fact.key, err)
			}
		}
	}

	return collection.Result{Changed: false}, nil
}

// gatherPlan reads the filter and returns the probes to run, each narrowed
// to the fact keys that survived it.
//
// A filter matching nothing is refused rather than honored. Ansible would
// return an empty fact set, and this does not, because a pattern that
// matches none of eight fixed names is a typo far more often than an
// intention, and a task that gathered nothing and reported success would
// hide it until a later condition read a fact that was never emitted.
func gatherPlan(params map[string]any) ([]gatherProbe, error) {
	patterns, _, err := sdk.StringSlice(params, gatherParamFilter)
	if err != nil {
		return nil, err
	}

	var planned []gatherProbe
	for _, probe := range gatherProbes() {
		// A fresh slice rather than filtering in place: gatherProbes hands
		// back the keys slice it built, and writing through it would edit
		// the answer the next call sees.
		var kept []string
		for _, key := range probe.keys {
			matched, err := gatherMatches(patterns, key)
			if err != nil {
				return nil, err
			}
			if matched {
				kept = append(kept, key)
			}
		}
		if len(kept) == 0 {
			continue
		}
		probe.keys = kept
		planned = append(planned, probe)
	}

	if len(planned) == 0 {
		return nil, fmt.Errorf("%s %q matches none of the facts this method gathers (%s)",
			gatherParamFilter, patterns, strings.Join(gatherAllKeys(), ", "))
	}
	return planned, nil
}

// gatherMatches reports whether key is wanted by any of the patterns, with
// no patterns meaning every key is.
//
// path.Match is the same shell-style matching Ansible's filter uses, and a
// malformed pattern is refused by name rather than silently matching
// nothing, which is what path.Match's own error exists to say.
func gatherMatches(patterns []string, key string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}
	for _, pattern := range patterns {
		matched, err := path.Match(pattern, key)
		if err != nil {
			return false, fmt.Errorf("%s pattern %q is malformed: %w", gatherParamFilter, pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// gatherWanted reports whether key survived the filter for its probe.
func gatherWanted(keys []string, key string) bool {
	for _, wanted := range keys {
		if wanted == key {
			return true
		}
	}
	return false
}

// gatherAllKeys returns every fact name this method can emit, in probe
// order, so a refused filter can say what it could have matched.
func gatherAllKeys() []string {
	var keys []string
	for _, probe := range gatherProbes() {
		keys = append(keys, probe.keys...)
	}
	return keys
}

// gatherSingle builds the parser for a probe whose whole output is one
// fact's value, optionally refined first.
//
// Empty output produces no fact rather than a fact whose value is the
// empty string. That distinction is the contract: a condition reading
// ansible_kernel must be able to tell a device that did not answer from
// one that answered with nothing.
func gatherSingle(key string, refine func(string) string) func(string) []gatherFact {
	return func(stdout string) []gatherFact {
		value := strings.TrimSpace(stdout)
		if refine != nil {
			value = refine(value)
		}
		if value == "" {
			return nil
		}
		return []gatherFact{{key: key, value: value}}
	}
}

// gatherShortName returns everything before the first dot, which is what
// turns uname's node name into ansible_hostname.
func gatherShortName(name string) string {
	short, _, _ := strings.Cut(name, ".")
	return short
}

// gatherOSRelease reads the distribution name and version out of
// /etc/os-release.
//
// Both are optional. A distribution that publishes NAME and no VERSION_ID
// (a rolling release does exactly this) reports one fact and not the
// other, rather than a version of "" that a later condition would compare
// against and get a wrong answer from.
func gatherOSRelease(stdout string) []gatherFact {
	values := gatherKeyValues(stdout)

	var facts []gatherFact
	if name := values["NAME"]; name != "" {
		facts = append(facts, gatherFact{key: gatherFactDistribution, value: name})
	}
	if version := values["VERSION_ID"]; version != "" {
		facts = append(facts, gatherFact{key: gatherFactDistributionVersion, value: version})
	}
	return facts
}

// gatherKeyValues parses the KEY=VALUE lines of an os-release file.
//
// The values are quoted or bare depending on the distribution
// (NAME="Ubuntu" against NAME=Fedora is a real difference between two
// real files), so the surrounding quotes come off unconditionally. A line
// with no "=" in it is skipped rather than refused: a blank last line is
// exactly that, and so is a comment.
func gatherKeyValues(stdout string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		values[key] = strings.Trim(value, "\"'")
	}
	return values
}

// gatherMemTotalMB reads MemTotal out of /proc/meminfo and converts it to
// megabytes.
//
// The missing-field case and the unparseable-field case are one branch on
// purpose: gatherFieldAfter answers "" for a file with no MemTotal line,
// and ParseInt refuses "" for the same reason it refuses "banana", so
// there is one place that decides this fact could not be determined
// rather than two that could disagree.
func gatherMemTotalMB(stdout string) []gatherFact {
	kilobytes, err := strconv.ParseInt(gatherFieldAfter(stdout, "MemTotal:"), 10, 64)
	if err != nil || kilobytes <= 0 {
		return nil
	}
	return []gatherFact{{key: gatherFactMemTotalMB, value: kilobytes / gatherKilobytesPerMegabyte}}
}

// gatherFieldAfter returns the first whitespace-separated word following
// label, on the first line that starts with it, or "" when no line does.
func gatherFieldAfter(stdout, label string) string {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == label {
			return fields[1]
		}
	}
	return ""
}

// gatherProcessorCount counts the distinct physical id values in
// /proc/cpuinfo, which is how many processor packages the device has.
//
// A file with no physical id field at all (whole architectures publish
// none) produces no fact. Ansible substitutes 1 there, and this does not:
// a 1 that means "there was nothing to read" is indistinguishable from a
// 1 that means "one socket", and drift data whose absence looks like a
// measurement is data nobody can act on.
func gatherProcessorCount(stdout string) []gatherFact {
	sockets := make(map[string]struct{})
	for _, line := range strings.Split(stdout, "\n") {
		label, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(label) != "physical id" {
			continue
		}
		sockets[strings.TrimSpace(value)] = struct{}{}
	}
	if len(sockets) == 0 {
		return nil
	}
	return []gatherFact{{key: gatherFactProcessorCount, value: len(sockets)}}
}

// gatherUptimeSeconds reads the first field of /proc/uptime, which is how
// long the device has been up, and truncates it to whole seconds.
//
// The file reports a fraction (159466.56), and the fraction is dropped
// rather than kept: a fact recorded to a hundredth of a second differs on
// every single run, so a drift report built on it would be nothing but
// noise.
func gatherUptimeSeconds(stdout string) []gatherFact {
	seconds, err := strconv.ParseFloat(gatherFirstField(stdout), 64)
	if err != nil || seconds < 0 {
		return nil
	}
	return []gatherFact{{key: gatherFactUptimeSeconds, value: int64(seconds)}}
}

// gatherFirstField returns the first whitespace-separated word of stdout,
// or "" when there is none.
func gatherFirstField(stdout string) string {
	fields := strings.Fields(stdout)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
