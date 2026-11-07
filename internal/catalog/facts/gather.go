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

// The fact keys this method emits, this platform's own: each in a family
// (host., os., hardware.) and lowercase, holding a native value (the
// os-release ID, a lowercase family, an integer major version) rather than
// ansible.builtin.setup's name and word for it, which pkg/ansiblefacts
// maps them to.
//
// Every identifier in this package carries the gather prefix because a
// sibling method landing in this namespace later would share the package,
// and Go has no file-level scope to keep two files' names apart.
const (
	gatherFactHostName      = "host.name"
	gatherFactHostNodename  = "host.nodename"
	gatherFactHostUptime    = "host.uptime_seconds"
	gatherFactKernelName    = "os.kernel.name"
	gatherFactKernelRelease = "os.kernel.release"
	gatherFactDistribution  = "os.distribution"
	gatherFactVersion       = "os.version"
	gatherFactVersionMajor  = "os.version_major"
	gatherFactCodename      = "os.codename"
	gatherFactEdition       = "os.edition"
	gatherFactArchitecture  = "hardware.architecture"
	gatherFactMemoryMB      = "hardware.memory_mb"
	gatherFactSwapMB        = "hardware.swap_mb"
	gatherFactCPUSockets    = "hardware.cpu.sockets"
	gatherFactCPUCores      = "hardware.cpu.cores"
	gatherFactCPUThreads    = "hardware.cpu.threads"
)

// gatherKilobytesPerMegabyte converts what /proc/meminfo reports into
// hardware.memory_mb's whole megabytes. It divides and truncates, the one
// rule every platform's memory and swap are held to.
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
			// package databases, the service managers' listings, the socket
			// tables, the account files, stat, CIM and the registry). Beyond plain reads it runs the platform's own
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
	// Only the uptime and a service's state move on their own; every other
	// fact changes when the device does.
	d.Manifest.Facts = append(collection.DeclareFacts(d.Manifest.Doc.Returns, gatherFactNames(), gatherFactHostUptime),
		// One fact per item (Phase 41c): a package's version or a service's
		// start setting changing is the device changing.
		collection.FactSpec{Key: gatherFamilyPackage, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One installed package: version, arch and source (the database it was read from)."},
		collection.FactSpec{Key: gatherFamilyService, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One service: enabled, status (the manager's own word for its start setting) and source."},
		// A service's state is not: a timer's job runs and exits, a service
		// started on demand comes and goes, on a device nobody changed, so it
		// is kept as its latest value and never shown as drift.
		collection.FactSpec{Key: gatherFamilyServiceState, Prefix: true, ItemNames: true, Kind: collection.FactVolatile, Type: collection.FactString,
			Description: "What one service is doing (running, stopped, the manager's own word), its latest value only."},
		// A path's digest or owner changing, a new listener, an account
		// added: each is the device changing.
		collection.FactSpec{Key: gatherFamilyFile, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One path the task named: state (present, absent, unreadable) and, when present, type, size, owner, mode, sha256 and link target; never the content."},
		collection.FactSpec{Key: gatherFamilyPort, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One listening socket: protocol, address, port and, where the login may see it, the process."},
		collection.FactSpec{Key: gatherFamilyUser, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One local account: uid, gid, home and shell (sid and enabled on Windows); never a password or its hash."},
		collection.FactSpec{Key: gatherFamilyGroup, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "One local group: gid (sid on Windows) and its listed members."},
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
		Summary:     "Gathers system facts from the target: OS, kernel, distribution, and on request its packages, services, listening ports, accounts and named files.",
		Description: "Reads every system fact it knows by default, over SSH or, from a Windows host, over WinRM (identity, kernel and system, distribution and its release, memory and swap, processors, uptime) and emits each one as a fact under this platform's own key (os.distribution, hardware.memory_mb) holding a native value (the os-release ID, a lowercase family, an integer major version), not under ansible.builtin.setup's names, which are not stored, so it is kept as drift data, which is also a stat a register, until or when reads as result.<register>.<name>. filter narrows it. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading os.distribution can tell \"this device does not say\" from \"this device says nothing\". Nothing is changed and no command is elevated, so this always reports no change. gather_subset chooses what is read: min (identity, kernel, distribution) and hardware (memory, processors, uptime), the default; packages, one fact per installed package (package.<name>: version, arch and source, from dpkg, rpm, apk, FreeBSD's pkg, Homebrew, pkgutil's receipts, or a Windows host's Uninstall keys); services, two facts per service (service.<name>: enabled and status, and service_state.<name>: its state, which moves on its own and so is kept as its latest value only, never as drift), from systemd, OpenRC, FreeBSD's rc, launchd, or the Windows service control manager, leaving out a systemd unit that lasts only as long as a login, a connection or a process: a transient unit, a user@ or user-runtime-dir@ instance, or one connection's instance of an Accept=yes socket (a lingering user's user@<uid>.service is left out too, though it lasts as long as the host and not a login: that is a choice, so a user's own service manager is never listed as a service; and systemd before version 230 does not report a transient unit as transient, so there such a unit is still listed); ports, one fact per listening socket (port.<protocol>:<address>:<port>, from the kernel's socket tables on Linux, sockstat on FreeBSD, netstat on macOS, and the TCP/IP cmdlets on Windows); users, one fact per local account and group (user.<name>, group.<name>, from /etc/passwd and /etc/group read without their password field, a Mac's local directory, or a Windows host's local accounts), never a password or its hash; and files, one fact per path named in files (file.<path>: metadata and a SHA-256 digest, plain for a file anyone may read and keyed for any other, never the content), where a path the login cannot reach is unreadable, never absent. The sources each return names are Linux's: FreeBSD and macOS answer the same facts from sysctl (and macOS its distribution from sw_vers), and Windows from CIM, where host.name is the computer name, os.kernel.name, os.family and os.distribution are windows, the kernel release and os.version are the OS version, and os.edition is the edition's caption. A family is read or the task says it was not: a source on the device whose listing fails, or answers without the structure it always prints, fails the task, so no item reads as removed; a family no source on the device answers fails a task that named its subset, and is a warning for one that reached it through all. Two of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.",
		Params: []collection.Param{
			{Name: gatherParamFilter, Type: "list of string", Description: "Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its key matches any pattern, so [\"os.*\"] gathers the operating system's facts (its distribution, family, version, codename and kernel) and nothing else. Leaving this out gathers everything the chosen subsets gather, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out. A service's state is service_state.<name>, which a pattern for service.<name> alone does not match."},
			{Name: gatherParamSubset, Type: "list of string", Description: "The subsets to gather: min and hardware as ansible.builtin.setup names them, packages and services (Ansible's package_facts and service_facts), ports, users and files, or all, every one; a name with a leading ! leaves that subset out wherever it stands in the list, unless the task also names that subset itself, as Ansible reads it (files with paths in files and !files together are still refused), and a list of only such names starts from min and hardware. min is gathered unless !min is named. Leaving this out gathers min and hardware, what this method always read, so a task never starts listing a host's packages without asking. files is gathered whenever files names paths, all gathers it only then, and naming files with no paths is refused. An unknown name is refused."},
			{Name: gatherParamFiles, Type: "list of string", Description: "Absolute paths to describe, at most 64: for each, whether it is present, absent or unreadable and, when present, its type, size, owner and group (uid and gid on a POSIX device), mode (attributes on Windows), the SHA-256 and size of a regular file anyone may read, and a link's target. Content is never read back. A file not world-readable (on a POSIX device, one its mode or a directory above it keeps from others; on Windows, one whose ACL does not let Everyone, Authenticated Users or Users read it) keeps no plain digest and no size, since a stored digest of a small secret can be guessed offline by anyone who may read the facts: its digest is kept only as hmac_sha256, HMAC-SHA-256 under a key the project (from its master key) or the Controller (PLEIADES_FACTS_DIGEST_KEY) holds, with hmac_key naming the key, so an unchanged file keeps its value and a change still shows. Where no such key is held the file is described without a digest. A path is absent only when the device says it does not exist; one inside a directory the login may not search, or that Windows will not open, is unreadable. A link is described as itself, not what it points to. A network path (a UNC path such as \\\\server\\share) is refused."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: gatherFactHostName, Type: "string", Returned: "when the device reports one", Description: "The short host name, which is everything before the first dot of what uname -n reports; on Windows the computer name."},
			{Name: gatherFactKernelRelease, Type: "string", Returned: "when the device reports one", Description: "The kernel release, as uname -r reports it; on Windows the OS version."},
			{Name: gatherFactArchitecture, Type: "string", Returned: "when the device reports one", Description: "The machine hardware name, as uname -m reports it, for example x86_64 or aarch64; on Windows the same names, read from the processor architecture Windows names (AMD64 is x86_64, ARM64 is aarch64, x86 is i686), and none for one it names otherwise."},
			{Name: gatherFactDistribution, Type: "string", Returned: "when the device names one", Description: "The distribution, as a lowercase identifier: ID in /etc/os-release as the file writes it (debian, ubuntu, rhel, rocky, almalinux, ol, fedora, amzn, alpine, arch, opensuse-leap, sles and the rest), freebsd on FreeBSD, macos on macOS and windows on Windows. An /etc/os-release naming no ID reports none."},
			{Name: gatherFactVersion, Type: "string", Returned: "when the device names one", Description: "The distribution version: VERSION_ID in /etc/os-release, on Debian the point release /etc/debian_version holds when it is more precise (12.12 where VERSION_ID is 12, and forky/sid on testing, which has no VERSION_ID), and on the SUSE family VERSION_ID's major and minor parts; ProductVersion on macOS and the OS version on Windows. A rolling release that publishes no VERSION_ID reports no such fact."},
			{Name: gatherFactMemoryMB, Type: "int", Returned: "when the device reports it", Description: "Total usable memory in whole megabytes, truncated: from MemTotal in /proc/meminfo, hw.physmem on FreeBSD, hw.memsize on macOS and TotalPhysicalMemory on Windows."},
			{Name: gatherFactCPUSockets, Type: "int", Returned: "when /proc/cpuinfo reports it, or Windows does", Description: "The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo (NumberOfProcessors on Windows). An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1."},
			{Name: gatherFactHostUptime, Type: "int", Returned: "when the device reports it", Description: "How long the device has been up, in whole seconds, from the first field of /proc/uptime; from the boot time and the device's own clock on FreeBSD, macOS and Windows."},
			{Name: gatherFactHostNodename, Type: "string", Returned: "when the device reports one", Description: "The full node name uname -n reports, before host.name cuts it at the first dot; on Windows the DNS host name, with the domain when the host is joined to one."},
			{Name: gatherFactKernelName, Type: "string", Returned: "when the device reports one", Description: "The kernel's name, lowercase: linux, freebsd or darwin, as uname -s reports it, and windows on Windows."},
			{Name: gatherFactVersionMajor, Type: "int", Returned: "when the device names a version whose first part is a whole number", Description: "The distribution version up to its first dot, as a whole number, such as 24 for 24.04. A version whose first part is not one (Debian testing's forky/sid) reports none."},
			{Name: gatherFactFamily, Type: "string", Returned: "when the device names its distribution", Description: "The family the distribution belongs to, lowercase: debian, redhat, suse, arch, alpine, freebsd, macos or windows. A distribution whose os-release ID this method has not measured has a family only where Ansible's own family table lists its NAME (Linux Mint is debian), lowercased (gentoo, slackware): a family is never guessed."},
			{Name: gatherFactCodename, Type: "string", Returned: "when the distribution names one", Description: "The release's codename: VERSION_CODENAME in /etc/os-release (bookworm, noble), then UBUNTU_CODENAME, then the name in parentheses in VERSION (Blue Onyx on Rocky Linux 9.6, Plow on Red Hat). A distribution that names none (Fedora, Alpine, Amazon Linux, Oracle Linux, CentOS Stream, the SUSE family) reports none, and so do FreeBSD, macOS and Windows."},
			{Name: gatherFactEdition, Type: "string", Returned: "on Windows, when the host names it", Description: "The Windows edition's caption, such as Microsoft Windows Server 2022 Datacenter."},
			{Name: gatherFactSwapMB, Type: "int", Returned: "when the device reports it, never on macOS or Windows", Description: "Total swap in whole megabytes, truncated, from SwapTotal in /proc/meminfo (vm.swap_total on FreeBSD); 0 on a device with no swap. macOS and Windows report none: a Mac's swap files are made and removed as memory is needed, so their total moves on its own, and a Windows page file is not swap."},
			{Name: gatherFactCPUThreads, Type: "int", Returned: "when the device reports it", Description: "The number of logical processors, counted as the processor entries in /proc/cpuinfo (hw.ncpu on FreeBSD and macOS, NumberOfLogicalProcessors on Windows)."},
			{Name: gatherFactCPUCores, Type: "int", Returned: "when /proc/cpuinfo reports cpu cores, or Windows does", Description: "The cores per processor package, from the first cpu cores field in /proc/cpuinfo (the first package's NumberOfCores on Windows)."},
			{Name: "package.<name>", Type: "map", Returned: "with the packages subset, for each installed package", Description: "One installed package: version, arch and source (dpkg, rpm, apk, pkg, brew, brew-cask, pkgutil or registry). A name a key cannot hold is percent-encoded; a name two packages share takes :arch, then @source, then #2 and on in the order the source lists them. A package its source gives no version has none."},
			{Name: "service.<name>", Type: "map", Returned: "with the services subset, for each service the service manager lists", Description: "One service by its manager's name for it: enabled, status and source; what it is doing now is service_state.<name>. From systemd (source systemd), status is the unit file's state (enabled, disabled, static, masked, generated, indirect and so on), enabled meaning enabled or enabled-runtime, and a unit that lasts only as long as a login, a connection or a process (a transient unit, a user@ or user-runtime-dir@ instance, one connection's instance of an Accept=yes socket) not listed, a lingering user's user@<uid>.service left out as well by choice though it lasts as long as the host, and a transient unit still listed on systemd before 230, which does not report it as transient; from OpenRC (openrc), status is the runlevels the script is in, or disabled, enabled meaning a runlevel other than shutdown; from FreeBSD's rc, status is enabled or disabled; from launchd, status is enabled or disabled, a daemon launchd has not loaded because it is disabled being listed all the same, and a job launchd starts once per connection or session (a label ending in a UUID, such as com.openssh.sshd.<UUID>) not listed, since a new one comes with every login; from the Windows service control manager (scm), status is the start mode (auto, delayed, manual, disabled, boot, system), enabled meaning it starts with Windows, and a per-user service is listed once by its template's name with per_user true, never by the instance Windows makes for each signed-in user (Template_<LUID>), whose name changes with every sign-in."},
			{Name: "service_state.<name>", Type: "string", Returned: "with the services subset, for each service its manager says the state of", Description: "What the service service.<name> names is doing, kept as its latest value only and never as drift, since services start and stop on their own (a timer's job, a service started when something asks for it). From systemd, the unit's sub-state (running, exited, dead, failed, inactive and the others systemd reports, such as activating or auto-restart); from OpenRC, running, stopped or OpenRC's own word (crashed, inactive), or unknown before OpenRC has booted; from FreeBSD's rc, running, stopped or unknown (an rc script that is not enabled, or whose status only root may read); from launchd, running or stopped; from Windows, the manager's own word lower-cased (running, stopped, start pending). A per-user service's template has none."},
			{Name: "file.<path>", Type: "map", Returned: "with the files subset, for each path in files", Description: "One named path: state (present, absent or unreadable); when present, type (file, directory, link, fifo, socket, char or block), size (not for a directory, nor a file not everyone may read), uid, gid, owner, group and mode as four octal digits on a POSIX device, attributes and owner on Windows, for a regular file world_readable and, when anyone may read it, sha256 and size, or when not, hmac_sha256 and hmac_key (a keyed digest the store can compare and nobody without the key can test guesses against; on a Runner the task's own result holds it sealed to the Controller, as sha256_sealed, and only the stored fact holds it keyed), readable, and link_target for a link. The path is percent-encoded in the key as any item name is."},
			{Name: "port.<protocol>:<address>:<port>", Type: "map", Returned: "with the ports subset, for each listening socket", Description: "One listening TCP socket, or UDP socket bound with no peer: protocol (tcp or udp), address (0.0.0.0 or :: for any; in brackets in the key when it is IPv6), port, uid on Linux, and process, the owning process's name, where the login may see it (on Linux its own processes, or every one as root; on FreeBSD every one unless security.bsd.see_other_uids is off; every one on macOS; on Windows every one an administrator sees). A socket taking both families is ::."},
			{Name: "user.<name>", Type: "map", Returned: "with the users subset, for each local account", Description: "One local account: uid, gid, home, shell and source (files from /etc/passwd, dslocal from a Mac's local directory), or on Windows sid, enabled and source sam. Accounts a directory service adds (LDAP, SSSD, a domain) are not local and not listed; the password field is never read."},
			{Name: "group.<name>", Type: "map", Returned: "with the users subset, for each local group", Description: "One local group: gid (sid on Windows), members as the group lists them (an account whose primary group it is is not repeated), and source. A Windows group whose members could not be listed has no members key."},
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
				Name:        "Record a configuration file's digest and who listens where",
				RunbookYAML: "- name: Describe sshd's configuration and the listening sockets\n  facts.gather:\n    gather_subset:\n      - ports\n    files:\n      - /etc/ssh/sshd_config\n",
			},
			{
				Name:        "Gather only what a later condition reads",
				RunbookYAML: "- name: Learn which distribution this is\n  facts.gather:\n    filter:\n      - os.*\n",
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
	// commandFor, when set, builds the command from the paths the task
	// named (the files subset); probesFor sets command from it.
	commandFor func(paths []string) string
	parse      func(stdout string) []gatherFact
	// present, for a family probe reached over SSH, is the shell test that
	// its source is on the device; the command runs only where it succeeds
	// (guarded), and the probe answers "absent" otherwise (gather_outcome.go).
	present string
	// check, when set, says whether a family probe's answer has the
	// structure its source always prints; one that lacks it failed.
	check func(stdout string) error
	// label, when set, is how an error names the probe (name).
	label string
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
// Every command is a compile-time constant with nothing from the runbook in
// it, but one: the files probe's, which holds the paths the task named, each
// quoted where it is built (posixFileProbe, windowsFileProbe).
func gatherProbesFor(platform string) []gatherProbe {
	var probes []gatherProbe
	switch platform {
	case platformFreeBSD:
		probes = append(append(bsdHostProbes(platform), freebsdPackageProbes()...), freebsdServiceProbes()...)
		probes = append(probes, posixFileProbe(statFormatBSD, modeFormatBSD), freebsdPortProbe())
		return append(probes, posixUserProbes()...)
	case platformDarwin:
		probes = append(append(bsdHostProbes(platform), darwinPackageProbes()...), darwinServiceProbes()...)
		probes = append(probes, posixFileProbe(statFormatBSD, modeFormatBSD), darwinPortProbe())
		return append(probes, darwinUserProbes()...)
	case platformWindows:
		return windowsProbes()
	}
	probes = gatherHostProbes()
	probes = append(probes, linuxPackageProbes()...)
	probes = append(probes, linuxServiceProbes()...)
	probes = append(probes, posixFileProbe(statFormatGNU, modeFormatGNU))
	probes = append(probes, linuxPortProbes()...)
	return append(probes, posixUserProbes()...)
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
			keys:    []string{gatherFactHostName, gatherFactHostNodename},
			command: "uname -n",
			// uname reports the node name, which on a machine with a domain
			// is the full name. host.name is the short one and host.nodename
			// the full one, so cutting at the first dot is
			// what makes this fact mean what its name says. The full name is
			// not gathered under a second key: it would need a separate
			// resolver-dependent lookup, and a fact that sometimes resolves
			// and sometimes does not is worse than an absent one.
			parse: gatherNodeNames,
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactKernelName},
			command: "uname -s",
			// Lowercase, as every native word is: linux, freebsd, darwin.
			parse: gatherLower(gatherFactKernelName),
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactKernelRelease},
			command: "uname -r",
			parse:   gatherSingle(gatherFactKernelRelease),
		},
		{
			subset:  gatherSubsetMin,
			keys:    []string{gatherFactArchitecture},
			command: "uname -m",
			parse:   gatherSingle(gatherFactArchitecture),
		},
		{
			subset: gatherSubsetMin,
			keys: []string{gatherFactDistribution, gatherFactFamily, gatherFactVersion, gatherFactVersionMajor,
				gatherFactCodename},
			command: osReleaseCommand,
			parse:   gatherOSRelease,
		},
		{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactMemoryMB, gatherFactSwapMB},
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
			keys:    []string{gatherFactCPUSockets, gatherFactCPUThreads, gatherFactCPUCores},
			command: "cat /proc/cpuinfo",
			parse:   gatherProcessors,
		},
		{
			subset:  gatherSubsetHardware,
			keys:    []string{gatherFactHostUptime},
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
	// The named paths are held to the device's own form before any probe
	// runs, so a path written for the other kind of system reads nothing.
	if err := gatherFilesFor(choice.files, platform); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	probes := choice.probesFor(platform)

	names := gatherNames{}
	emitted := 0
	// What each family's sources answered, in the order first seen, so the
	// verdict below names them in a stable order.
	families := map[string]*familyState{}
	var familyOrder []string
	for _, probe := range probes {
		stdout, err := run(ctx, probe.command)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, probe.name(), err)
		}
		// A family's listing says whether its source was read (FAILURE_PATTERNS 925): an empty answer is
		// "none" only from a source that was there
		// and answered. The files family names its paths and answers each,
		// so it has no source to be absent.
		if probe.family != "" && probe.commandFor == nil {
			st, ok := families[probe.family]
			if !ok {
				st = &familyState{subset: probe.subset}
				families[probe.family] = st
				familyOrder = append(familyOrder, probe.family)
			}
			outcome, body, err := readOutcome(stdout, probe.check)
			switch outcome {
			case outcomeAbsent:
				continue
			case outcomeFailed:
				st.failures = append(st.failures, fmt.Sprintf("%s: %v", probe.name(), err))
				continue
			}
			st.read = true
			stdout = body
		}

		var keep []gatherFact
		for _, fact := range probe.parse(stdout) {
			// A probe answering two facts can survive a filter that wants
			// only one of them, so the keys are checked again here. Dropping
			// this would emit os.version to a task that asked for
			// os.distribution alone.
			if probe.family == "" {
				if gatherWanted(probe.keys, fact.key) {
					keep = append(keep, fact)
				}
				continue
			}
			// Two databases may both list a package (dpkg and rpm, Homebrew
			// and pkgutil): one never overwrites the other. Told apart before
			// the filter, and whether or not it keeps the item, so the filter
			// reads the key that is stored (package.glibc:i686 for
			// package.glibc:*, FAILURE_PATTERNS 930) and a key does not change
			// with the filter. A service's state is split off here too.
			for _, item := range familyItems(names, fact) {
				// A family's member is held to the filter itself: its key was
				// not known until the device listed it.
				if gatherMatches(patterns, item.key) {
					keep = append(keep, item)
				}
			}
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
	// A family a source failed to list, or one no source answers for a task
	// that named its subset, fails the task: a failed task stores none of
	// what it emitted, so no item reads as removed (FAILURE_PATTERNS 925).
	var warnings []string
	for _, family := range familyOrder {
		st := families[family]
		warning, err := familyVerdict(family, *st, choice.explicit[st.subset])
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, device.Name(), err)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
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
		if _, err := path.Match(fnmatchPattern(pattern), ""); err != nil {
			return gatherChoice{}, fmt.Errorf("%s pattern %q is malformed: %w", gatherParamFilter, pattern, err)
		}
	}
	files, err := gatherFilePaths(params)
	if err != nil {
		return gatherChoice{}, err
	}
	choice := gatherChoice{chosen: chosen, explicit: gatherExplicit(params), patterns: patterns, files: files}
	// Named paths a filter would drop are a contradiction, not a wish to
	// read nothing.
	if len(files) > 0 && chosen[gatherSubsetFiles] && !gatherFamilyWanted(patterns, gatherFamilyFile) {
		return gatherChoice{}, fmt.Errorf("%s names paths and %s %q matches no %s fact", gatherParamFiles, gatherParamFilter, patterns, gatherFamilyFile)
	}
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
	chosen map[string]bool
	// explicit is the subsets the task named itself, not through all: a
	// family no source answers fails only those (familyVerdict).
	explicit map[string]bool
	patterns []string
	// files is the paths the files subset reads.
	files []string
}

// probesFor is the probes choice runs on platform, a probe built from the
// task's paths given its command and its facts' keys.
func (choice gatherChoice) probesFor(platform string) []gatherProbe {
	probes := choice.narrow(gatherProbesFor(platform))
	for i, p := range probes {
		if p.present != "" {
			// Named in an error by the listing it runs, not by the guard
			// wrapped around it.
			probes[i].label = p.command
			probes[i].command = guarded(p.present, p.command)
		}
		if p.commandFor == nil {
			continue
		}
		parse, paths := p.parse, choice.files
		probes[i].command = p.commandFor(paths)
		probes[i].parse = func(out string) []gatherFact { return filesFacts(paths, parse(out)) }
	}
	return probes
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
			// A services probe is wanted for its states alone too.
			for _, family := range familiesOf(probe.family) {
				if gatherFamilyWanted(choice.patterns, family) {
					planned = append(planned, probe)
					break
				}
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
// package.nginx, package.*, pack* and *nginx all can and os.* cannot.
// Which members it wants is decided as they are emitted.
func gatherFamilyWanted(patterns []string, family string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		i := strings.IndexAny(pattern, `*?[\`)
		if i < 0 {
			// With no wildcard a pattern names one key, which is a member
			// only when it is longer than the family's prefix: package
			// alone matches no package and is the typo the plan refuses
			// (FAILURE_PATTERNS 930).
			if len(pattern) > len(family) && strings.HasPrefix(pattern, family) {
				return true
			}
			continue
		}
		literal := pattern[:i]
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
		if matched, _ := path.Match(fnmatchPattern(pattern), key); matched {
			return true
		}
	}
	return false
}

// fnmatchPattern is pattern, written for Python's fnmatch as Ansible's
// filter reads it, in path.Match's grammar: a class fnmatch negates with !
// ([!d]) is negated with ^ here, where [!d] would be a class holding ! and d;
// a ^ opening a class is a character to fnmatch and a negation to path.Match,
// so it is escaped; and a backslash is a character to fnmatch and an escape
// to path.Match, so it is doubled (FAILURE_PATTERNS 934). Keys
// hold no slash, so the two agree on what * spans.
func fnmatchPattern(pattern string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
			continue
		case !inClass && c == '[':
			inClass = true
			b.WriteByte(c)
			if i+1 < len(pattern) && pattern[i+1] == '!' {
				b.WriteByte('^')
				i++
			} else if i+1 < len(pattern) && pattern[i+1] == '^' {
				b.WriteString(`\^`)
				i++
			}
			continue
		case inClass && c == ']':
			inClass = false
		}
		b.WriteByte(c)
	}
	return b.String()
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
			for _, family := range familiesOf(probe.family) {
				if !seen[family] {
					keys = append(keys, family+"*")
					seen[family] = true
				}
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
// os.kernel.release must be able to tell a device that did not answer from
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

// gatherLower is gatherSingle with the value lowercased, for a fact whose
// native value is a lowercase word (uname -s's Linux is linux).
func gatherLower(key string) func(string) []gatherFact {
	single := gatherSingle(key)
	return func(stdout string) []gatherFact {
		facts := single(stdout)
		for i := range facts {
			facts[i].value = strings.ToLower(facts[i].value.(string))
		}
		return facts
	}
}

// gatherShortName returns everything before the first dot, which is what
// turns uname's node name into host.name.
func gatherShortName(name string) string {
	short, _, _ := strings.Cut(name, ".")
	return short
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
	return []gatherFact{{key: gatherFactMemoryMB, value: kilobytes / gatherKilobytesPerMegabyte}}
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
	return []gatherFact{{key: gatherFactCPUSockets, value: len(sockets)}}
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
	return []gatherFact{{key: gatherFactHostUptime, value: int64(seconds)}}
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
