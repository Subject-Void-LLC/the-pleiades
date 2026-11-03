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
	d := collection.Descriptor{
		Name: "facts.gather",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameFactGatherer,
			},
			// Nothing here needs root: every source is one an ordinary
			// login reads (uname, /etc/os-release, /proc, sysctl, the
			// package databases, the service managers' listings, CIM and
			// the registry). Beyond plain reads it runs the platform's own
			// listing tools (rc scripts' status, launchctl, and Homebrew
			// only as the account that owns it), each only reading, which
			// is a real property of the set that was chosen rather than
			// luck: a fact worth gathering on every device is one every
			// device can be asked for without elevation.
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
				Notes: "Reading is not changing. This method runs fixed reads (uname, /etc/os-release, /proc, sysctl, package and service " +
					"listings, CIM and the registry on Windows), alters nothing on the device, and so has nothing an undo could restore.",
				ReadOnly: true,
			},
			Doc: gatherDoc(),
			// Every command it sends is a fixed read (gatherProbesFor), so a
			// check runs it for real: reading is all a real run does.
			SupportsCheck: true,
		},
		Invoke: Gather,
		// Gather itself, since it only reads: its check answers with what
		// is on the device now, which is exactly what a real run would.
		Check: Gather,
	}
	// Only the uptime moves on its own; every other fact changes when the
	// device does.
	d.Manifest.Facts = append(collection.DeclareFacts(d.Manifest.Doc.Returns, gatherFactNames(), gatherFactUptimeSeconds),
		// One fact per item (Phase 41c): a package's version or a service's
		// state changing is the device changing.
		collection.FactSpec{Key: gatherFamilyPackage, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One installed package: version, arch and source (the database it was read from)."},
		collection.FactSpec{Key: gatherFamilyService, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One service: state, enabled, status (the manager's own word for it) and source."},
	)
	collection.MustRegister(d)
}

// gatherFactNames is every single fact this method may emit: every return
// its documentation names but a family, each of which is a fact.
func gatherFactNames() []string {
	var names []string
	for _, r := range gatherDoc().Returns {
		// A family (package.<name>) is declared as a prefix in init.
		if !strings.Contains(r.Name, "<") {
			names = append(names, r.Name)
		}
	}
	return names
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
		Summary:     "Gathers system facts from the target: OS, kernel, distribution, and on request its packages and services.",
		Description: "Reads every system fact it knows by default, over SSH or, from a Windows host, over WinRM (identity, kernel and system, distribution and its release, memory and swap, processors, uptime) and emits each one as a fact, so it is kept as drift data, which is also a stat a register, until or when reads as result.<register>.<name>. filter narrows it. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed and no command is elevated, so this always reports no change. gather_subset chooses what is read: min (identity, kernel, distribution) and hardware (memory, processors, uptime), the default; packages, one fact per installed package (package.<name>: version, arch and source, from dpkg, rpm, apk, FreeBSD's pkg, Homebrew, pkgutil's receipts, or a Windows host's Uninstall keys); and services, one fact per service (service.<name>: state, enabled and status, from systemd, OpenRC, FreeBSD's rc, launchd, or the Windows service control manager). The sources each return names are Linux's: FreeBSD and macOS answer the same facts from sysctl (and macOS its distribution from sw_vers), and Windows from CIM, where ansible_hostname is the computer name, ansible_system is Win32NT, the kernel and the distribution version are the OS version, and the distribution is the edition's caption, as Ansible reports them. Two of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.",
		Params: []collection.Param{
			{Name: gatherParamFilter, Type: "list of string", Description: "Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so [\"ansible_distribution*\"] gathers the distribution and its version and nothing else. Leaving this out gathers everything the chosen subsets gather, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out."},
			{Name: gatherParamSubset, Type: "list of string", Description: "The subsets to gather: min and hardware as ansible.builtin.setup names them, packages and services (Ansible's package_facts and service_facts), or all, every one; a name with a leading ! leaves that subset out, and a list of only such names starts from min and hardware. min is gathered unless !min is named. Leaving this out gathers min and hardware, what this method always read, so a task never starts listing a host's packages without asking. An unknown name is refused."},
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
			{Name: gatherFactNodename, Type: "string", Returned: "when the device reports one", Description: "The full node name uname -n reports, before ansible_hostname cuts it at the first dot."},
			{Name: gatherFactSystem, Type: "string", Returned: "when the device reports one", Description: "The kernel's name, as uname -s reports it: Linux, FreeBSD, Darwin."},
			{Name: gatherFactDistributionMajor, Type: "string", Returned: "when /etc/os-release names a version", Description: "VERSION_ID up to its first dot, such as 24 for 24.04."},
			{Name: gatherFactDistributionRelease, Type: "string", Returned: "when /etc/os-release names a codename", Description: "VERSION_CODENAME from /etc/os-release, such as noble."},
			{Name: gatherFactSwapTotalMB, Type: "int", Returned: "when /proc/meminfo reports it", Description: "Total swap in megabytes, from SwapTotal in /proc/meminfo; 0 on a device with no swap."},
			{Name: gatherFactProcessorVCPUs, Type: "int", Returned: "when /proc/cpuinfo lists processors", Description: "The number of logical processors, counted as the processor entries in /proc/cpuinfo."},
			{Name: gatherFactProcessorCores, Type: "int", Returned: "when /proc/cpuinfo reports cpu cores", Description: "The cores per processor package, from the first cpu cores field in /proc/cpuinfo."},
			{Name: "package.<name>", Type: "map", Returned: "with the packages subset, for each installed package", Description: "One installed package: version, arch and source (dpkg, rpm, apk, pkg, brew, brew-cask, pkgutil or registry). A name a key cannot hold is percent-encoded; a name two packages share takes :arch, then @source, then #2 and on in the order the source lists them. A package its source gives no version has none."},
			{Name: "service.<name>", Type: "map", Returned: "with the services subset, for each service the service manager lists", Description: "One service by its manager's name for it: state, enabled, status and source. From systemd (source systemd), state is the unit's sub-state (running, exited, dead, failed, inactive and the others systemd reports, such as activating or auto-restart) and status the unit file's state (enabled, disabled, static, masked, generated, indirect and so on), enabled meaning enabled or enabled-runtime; from OpenRC (openrc), state is running, stopped or OpenRC's own word (crashed, inactive), or unknown before OpenRC has booted, and status the runlevels the script is in, or disabled, enabled meaning a runlevel other than shutdown; from FreeBSD's rc, state is running, stopped or unknown (an rc script that is not enabled, or whose status only root may read) and status enabled or disabled; from launchd, state is running or stopped and status the last exit status launchctl prints; from the Windows service control manager (scm), state is the manager's own word lower-cased (running, stopped, start pending) and status the start mode (auto, delayed, manual, disabled, boot, system), enabled meaning it starts with Windows."},
		},
		Examples: []collection.Example{
			{
				Name:        "Gather everything before deciding what to do",
				RunbookYAML: "- name: Learn what this device is\n  facts.gather:\n",
			},
			{
				Name:        "Gather the installed packages and services",
				RunbookYAML: "- name: Learn what is installed and running\n  facts.gather:\n    gather_subset:\n      - packages\n      - services\n",
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
	// subset is the gather_subset this probe belongs to.
	subset string
	// family, when set, is the prefix of a family of one fact per item
	// (package.): the probe emits keys it cannot list ahead, so filter
	// narrows its facts as they are emitted rather than its keys up front.
	family  string
	keys    []string
	command string
	parse   func(stdout string) []gatherFact
}

// The platforms facts.gather reads, by what uname -s answers.
const (
	platformLinux   = "Linux"
	platformFreeBSD = "FreeBSD"
	platformDarwin  = "Darwin"
)

// gatherProbesFor is the probes of platform, as uname -s names it (or
// Windows, chosen by the transport): Linux's for Linux and for anything it
// does not know, since its sources are the ones a device most often has and
// a missing one only leaves facts out. They run in the order listed.
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
func gatherProbesFor(platform string) []gatherProbe {
	switch platform {
	case platformFreeBSD:
		return append(append(bsdHostProbes(platform), freebsdPackageProbes()...), freebsdServiceProbes()...)
	case platformDarwin:
		return append(append(bsdHostProbes(platform), darwinPackageProbes()...), darwinServiceProbes()...)
	case platformWindows:
		return windowsProbes()
	}
	probes := gatherHostProbes()
	probes = append(probes, linuxPackageProbes()...)
	return append(probes, linuxServiceProbes()...)
}

// gatherEveryProbe is every platform's probes, which a plan is checked
// against before the device is reached.
func gatherEveryProbe() []gatherProbe {
	var all []gatherProbe
	for _, p := range []string{platformLinux, platformFreeBSD, platformDarwin, platformWindows} {
		all = append(all, gatherProbesFor(p)...)
	}
	return all
}

// gatherHostProbes are the min and hardware subsets' probes: what this
// method read before it had subsets.
func gatherHostProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactHostname, gatherFactNodename},
			command: "uname -n",
			// uname reports the node name, which on a machine with a domain
			// is the full name. ansible_hostname is the short one and
			// ansible_fqdn is the full one, so cutting at the first dot is
			// what makes this fact mean what its name says. The full name is
			// not gathered under a second key: it would need a separate
			// resolver-dependent lookup, and a fact that sometimes resolves
			// and sometimes does not is worse than an absent one.
			parse: gatherNodeNames,
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactSystem},
			command: "uname -s",
			parse:   gatherSingle(gatherFactSystem),
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactKernel},
			command: "uname -r",
			parse:   gatherSingle(gatherFactKernel),
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactArchitecture},
			command: "uname -m",
			parse:   gatherSingle(gatherFactArchitecture),
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactDistribution, gatherFactDistributionVersion, gatherFactDistributionMajor, gatherFactDistributionRelease},
			command: "cat /etc/os-release",
			parse:   gatherOSRelease,
		},
		{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactMemTotalMB, gatherFactSwapTotalMB},
			command: "cat /proc/meminfo",
			parse:   gatherMemory,
		},
		{
			// The whole file rather than a grep for one field, because
			// grep's own absence is a failure this cannot tell apart from
			// the field's absence. It is the largest read here, growing
			// with the core count, and it is still a few tens of kilobytes
			// on a machine nobody would call small.
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactProcessorCount, gatherFactProcessorVCPUs, gatherFactProcessorCores},
			command: "cat /proc/cpuinfo",
			parse:   gatherProcessors,
		},
		{
			subset:  gatherSubsetHardware,
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
	choice, err := gatherPlan(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	patterns := choice.patterns

	run, platform, closeRun, err := gatherRunner(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer closeRun()
	probes := choice.probesFor(platform)

	names := gatherNames{}
	emitted := 0
	for _, probe := range probes {
		stdout, err := run(ctx, probe.command)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, probe.name(), err)
		}

		var keep []gatherFact
		for _, fact := range probe.parse(stdout) {
			// A probe answering two facts can survive a filter that wants
			// only one of them, so the keys are checked again here. Dropping
			// this would emit ansible_distribution_version to a task that
			// asked for ansible_distribution alone.
			if probe.family != "" {
				// A family's member is held to the filter itself: its key
				// was not known until the device listed it.
				if !gatherMatches(patterns, fact.key) {
					continue
				}
				// Two databases may both list a package (dpkg and rpm,
				// Homebrew and pkgutil): one never overwrites the other.
				fact = names.distinct(fact)
			} else if !gatherWanted(probe.keys, fact.key) {
				continue
			}
			keep = append(keep, fact)
		}
		// One task's observation is stored only whole and only up to
		// collection.MaxFactsPerObservation facts, so a device listing more
		// would store none of them while the task succeeded; it fails
		// instead, saying how to gather less.
		if emitted += len(keep); emitted > collection.MaxFactsPerObservation {
			return collection.Result{}, fmt.Errorf("%s: %s lists more than the %d facts one task may record; gather less with %s or %s",
				fqcn, device.Name(), collection.MaxFactsPerObservation, gatherParamSubset, gatherParamFilter)
		}
		for _, fact := range keep {
			if err := rc.EmitFact(fact.key, fact.value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, fact.key, err)
			}
		}
	}

	return collection.Result{Changed: false}, nil
}

// gatherPlan reads gather_subset and the filter and returns the probes to
// run, each narrowed to the fact keys that survived the filter, and the
// filter's patterns, which a family's facts are held to as they are emitted.
//
// A filter matching nothing is refused rather than honored. Ansible would
// return an empty fact set, and this does not, because a pattern that
// matches none of the facts the chosen subsets gather is a typo far more
// often than an intention, and a task that gathered nothing and reported
// success would hide it until a later condition read a fact that was never
// emitted.
func gatherPlan(params map[string]any) (gatherChoice, error) {
	chosen, err := gatherSelection(params)
	if err != nil {
		return gatherChoice{}, err
	}
	patterns, _, err := sdk.StringSlice(params, gatherParamFilter)
	if err != nil {
		return gatherChoice{}, err
	}
	// A malformed pattern is refused by name rather than silently matching
	// nothing, which is what path.Match's own error exists to say. Checked
	// once here, so matching later need not.
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil {
			return gatherChoice{}, fmt.Errorf("%s pattern %q is malformed: %w", gatherParamFilter, pattern, err)
		}
	}
	choice := gatherChoice{chosen: chosen, patterns: patterns}
	// Checked against every platform's probes before the device is
	// reached, so a typo costs no connection; narrowed to the device's own
	// platform once it says which (probesFor).
	if len(choice.narrow(gatherEveryProbe())) == 0 {
		return gatherChoice{}, fmt.Errorf("%s %q matches none of the facts the subsets %s gather (%s)",
			gatherParamFilter, patterns, chosenNames(chosen), strings.Join(gatherAllKeys(chosen), ", "))
	}
	return choice, nil
}

// gatherChoice is what a task asked for: its subsets and filter.
type gatherChoice struct {
	chosen   map[string]bool
	patterns []string
}

// probesFor is the probes choice runs on platform.
func (choice gatherChoice) probesFor(platform string) []gatherProbe {
	return choice.narrow(gatherProbesFor(platform))
}

// narrow keeps the probes of the chosen subsets that the filter can want,
// each narrowed to the keys it kept.
func (choice gatherChoice) narrow(probes []gatherProbe) []gatherProbe {
	var planned []gatherProbe
	for _, probe := range probes {
		if !choice.chosen[probe.subset] {
			continue
		}
		if probe.family != "" {
			if gatherFamilyWanted(choice.patterns, probe.family) {
				planned = append(planned, probe)
			}
			continue
		}
		// A fresh slice rather than filtering in place: gatherProbesFor
		// hands back the keys slice it built, and writing through it would
		// edit the answer the next call sees.
		var kept []string
		for _, key := range probe.keys {
			if gatherMatches(choice.patterns, key) {
				kept = append(kept, key)
			}
		}
		if len(kept) == 0 {
			continue
		}
		probe.keys = kept
		planned = append(planned, probe)
	}
	return planned
}

// gatherFamilyWanted reports whether the filter could want a fact of family:
// with no patterns, or a pattern whose literal start (the text before its
// first wildcard) and the family's prefix agree as far as both go, so
// package.nginx, package.*, pack* and *nginx all can and ansible_* cannot.
// Which members it wants is decided as they are emitted.
func gatherFamilyWanted(patterns []string, family string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		literal := pattern
		if i := strings.IndexAny(pattern, `*?[\`); i >= 0 {
			literal = pattern[:i]
		}
		if strings.HasPrefix(family, literal) || strings.HasPrefix(literal, family) {
			return true
		}
	}
	return false
}

// gatherMatches reports whether key is wanted by any of the patterns, with
// no patterns meaning every key is. path.Match is the same shell-style
// matching Ansible's filter uses; gatherPlan has refused a malformed
// pattern, so its error cannot arise here.
func gatherMatches(patterns []string, key string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, key); matched {
			return true
		}
	}
	return false
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

// gatherNames are the family keys a run has emitted, so a name a second item
// takes (rpm's multilib packages, a name two databases list, two Uninstall
// entries sharing a display name) is told apart: its architecture after a
// colon, as dpkg names a foreign one, then its source after an at sign,
// then a number after a hash, counted in the order the device lists them.
type gatherNames map[string]bool

// distinct is f under a key no earlier item took.
func (n gatherNames) distinct(f gatherFact) gatherFact {
	key := f.key
	if n[key] {
		value, _ := f.value.(map[string]any)
		if arch, _ := value["arch"].(string); arch != "" {
			key = itemKey(key+":", arch)
		}
		if source, _ := value["source"].(string); n[key] && source != "" {
			key = itemKey(key+"@", source)
		}
		for i, base := 2, key; n[key]; i++ {
			key = base + "#" + strconv.Itoa(i)
		}
	}
	n[key] = true
	f.key = key
	return f
}

// gatherAllKeys returns every fact name the chosen subsets can emit, in
// probe order, a family as its prefix and *, so a refused filter can say
// what it could have matched.
func gatherAllKeys(chosen map[string]bool) []string {
	var keys []string
	seen := map[string]bool{}
	for _, probe := range gatherEveryProbe() {
		if !chosen[probe.subset] {
			continue
		}
		if probe.family != "" {
			if !seen[probe.family] {
				keys = append(keys, probe.family+"*")
				seen[probe.family] = true
			}
			continue
		}
		for _, k := range probe.keys {
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
			}
		}
	}
	return keys
}

// gatherSingle builds the parser for a probe whose whole output is one
// fact's value.
//
// Empty output produces no fact rather than a fact whose value is the
// empty string. That distinction is the contract: a condition reading
// ansible_kernel must be able to tell a device that did not answer from
// one that answered with nothing.
func gatherSingle(key string) func(string) []gatherFact {
	return func(stdout string) []gatherFact {
		value := strings.TrimSpace(stdout)
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
		major, _, _ := strings.Cut(version, ".")
		facts = append(facts, gatherFact{key: gatherFactDistributionMajor, value: major})
	}
	if release := values["VERSION_CODENAME"]; release != "" {
		facts = append(facts, gatherFact{key: gatherFactDistributionRelease, value: release})
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
