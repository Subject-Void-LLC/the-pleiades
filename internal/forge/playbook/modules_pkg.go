// Package playbook: the package and service modules. Data only.
package playbook

// aptArgs and dnfArgs are the arguments each package module maps here,
// with its own aliases. A list of names becomes one task per name
// (ArgUnroll), since each native method installs one package. Refreshing
// the index is dropped with a review: the native install does not refresh
// it first, so it may install an older version than the playbook would.
var (
	aptArgs = []Arg{
		{Name: "name", Aliases: []string{"package", "pkg"}, To: "name", Handling: ArgUnroll},
		{Name: "state", Handling: ArgSelector},
		{Name: "update_cache", Aliases: []string{"update-cache"}, Handling: ArgDrop, Code: "module.semantics", Reason: "the package index is not refreshed before the install, so an older version may be installed"},
		{Name: "cache_valid_time", Handling: ArgDrop, Code: "module.semantics", Reason: "the package index is not refreshed before the install"},
	}
	dnfArgs = []Arg{
		{Name: "name", Aliases: []string{"pkg"}, To: "name", Handling: ArgUnroll},
		{Name: "state", Handling: ArgSelector},
		{Name: "update_cache", Aliases: []string{"expire-cache"}, Handling: ArgDrop, Code: "module.semantics", Reason: "the package metadata is not refreshed before the install, so an older version may be installed"},
	}
	packageArgs = []Arg{
		{Name: "name", To: "name", Handling: ArgUnroll},
		{Name: "state", Handling: ArgSelector},
	}
)

// pkgSelector maps a package state onto install, remove or upgrade under
// one namespace, with absent the value a task without state means ("" when
// the module requires state). installed and removed are dnf's and
// package's older spellings. latest maps to upgrade, whose manifest defines
// it as Ansible's own state=latest: upgrade the package when it is
// installed, install it when it is not.
func pkgSelector(absent string, older bool, install, remove, upgrade string) []Selector {
	present, gone := []string{"present"}, []string{"absent"}
	if older {
		present, gone = append(present, "installed"), append(gone, "removed")
	}
	return []Selector{{Arg: "state", Absent: absent, Choices: []Choice{
		{Values: present, Call: &Call{FQCN: install, Class: ClassAsserted, Basis: "the package present"}},
		{Values: gone, Call: &Call{FQCN: remove, Class: ClassAsserted, Basis: "the package absent"}},
		{Values: []string{"latest"}, Call: &Call{FQCN: upgrade, Class: ClassAsserted, Basis: "the package present at its newest available version"}},
	}}}
}

// svcArgs are the arguments the service modules map here; systemd's name
// also answers to service and unit.
var (
	svcArgs = []Arg{
		{Name: "name", To: "name"},
		{Name: "state", Handling: ArgSelector},
		{Name: "enabled", Handling: ArgSelector},
	}
	systemdArgs = []Arg{
		{Name: "name", Aliases: []string{"service", "unit"}, To: "name"},
		{Name: "state", Handling: ArgSelector},
		{Name: "enabled", Handling: ArgSelector},
		{Name: "daemon_reload", Aliases: []string{"daemon-reload"}, Handling: ArgSelector},
	}
)

// svcSelectors map a service's state and enabled onto native calls under
// prefix: state first, then enabled, one call each when both are given.
// restarted is an action rather than a state, so it is imperative; a
// reload has no native method.
func svcSelectors(prefix string) []Selector {
	return []Selector{
		{Arg: "state", Choices: []Choice{
			{Values: []string{"started"}, Call: &Call{FQCN: prefix + "start", Class: ClassAsserted, Basis: "the service running"}},
			{Values: []string{"stopped"}, Call: &Call{FQCN: prefix + "stop", Class: ClassAsserted, Basis: "the service stopped"}},
			{Values: []string{"restarted"}, Call: &Call{FQCN: prefix + "restart", Class: ClassImperative, Basis: "a restart is an action with no resulting state of its own"}},
			{Values: []string{"reloaded"}, Code: "args.value", Reason: "no native method reloads a service; restart it, or run the reload command"},
		}},
		{Arg: "enabled", Bool: true, Choices: []Choice{
			{Values: []string{"true"}, Call: &Call{FQCN: prefix + "enable", Class: ClassAsserted, Basis: "the service enabled at boot"}},
			{Values: []string{"false"}, Call: &Call{FQCN: prefix + "disable", Class: ClassAsserted, Basis: "the service disabled at boot"}},
		}},
	}
}

// pkgModules maps the package and service modules.
var pkgModules = []Entry{
	{Module: "ansible.builtin.package", Aliases: []string{"package"}, Args: packageArgs, Selectors: pkgSelector("", true, "pkg.install", "pkg.remove", "pkg.upgrade")},
	{Module: "ansible.builtin.apt", Aliases: []string{"apt"}, Args: aptArgs, Selectors: pkgSelector("present", false, "pkg.apt.install", "pkg.apt.remove", "pkg.apt.upgrade")},
	// dnf documents no default state, but installs unless autoremove is
	// set, which is not an argument here.
	{Module: "ansible.builtin.dnf", Aliases: []string{"dnf", "ansible.builtin.yum", "yum"}, Args: dnfArgs, Selectors: pkgSelector("present", true, "pkg.dnf.install", "pkg.dnf.remove", "pkg.dnf.upgrade")},
	{Module: "ansible.builtin.service", Aliases: []string{"service"}, Args: svcArgs, Selectors: svcSelectors("svc.")},
	{
		Module:  "ansible.builtin.systemd_service",
		Aliases: []string{"systemd_service", "ansible.builtin.systemd", "systemd"},
		Args:    systemdArgs,
		// daemon_reload first: Ansible reloads systemd before it changes the
		// unit, so a unit file written earlier is read before it starts. A
		// reload alone does nothing with the unit's name.
		Selectors: append([]Selector{{Arg: "daemon_reload", Absent: "false", Bool: true, Choices: []Choice{
			{Values: []string{"true"}, Call: &Call{FQCN: "svc.systemd.daemon_reload", Class: ClassImperative, Basis: "a reload of systemd's configuration is an action"},
				Ignores: []string{"name"}},
			{Values: []string{"false"}},
		}}}, svcSelectors("svc.systemd.")...),
	},
	{Module: "ansible.windows.win_service", Aliases: []string{"win_service"}, Args: svcArgs[:2], Selectors: svcSelectors("svc.windows.")[:1]},
}
