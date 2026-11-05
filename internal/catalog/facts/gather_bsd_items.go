// Package facts: the packages and services probes of FreeBSD and macOS
// (Phase 41c). FreeBSD's packages come from pkg and its services from rc;
// a Mac's packages from Homebrew and pkgutil's receipts, and its services
// from launchd's system domain. Each is read without root: a FreeBSD daemon
// whose pid file only root may read reports its state as unknown, never as
// stopped.
package facts

import (
	"fmt"
	"strings"
	"unicode"
)

// freebsdPackageProbes reads pkg's database, where pkg is bootstrapped
// (pkg -N; an unbootstrapped pkg would offer to install itself).
func freebsdPackageProbes() []gatherProbe {
	return []gatherProbe{{
		subset:  gatherSubsetPackages,
		family:  gatherFamilyPackage,
		present: `pkg -N >/dev/null 2>&1`,
		command: orFailed(`pkg query '%n\t%v\t%q'`),
		parse:   func(out string) []gatherFact { return packageFacts(parsePkg(out)) },
	}}
}

// parsePkg reads pkg query's lines (name, version, ABI such as
// FreeBSD:15:amd64), the architecture being the ABI's last part.
func parsePkg(out string) []installedPackage {
	var pkgs []installedPackage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" {
			continue
		}
		abi := strings.Split(fields[2], ":")
		pkgs = append(pkgs, installedPackage{name: fields[0], version: fields[1], arch: abi[len(abi)-1], source: "pkg"})
	}
	return pkgs
}

// freebsdServiceProbes reads rc: every script (service -l), the enabled ones
// (service -e), and the status of each enabled one, which takes a few
// seconds where every script's would take many more and tell little about
// scripts that do not start at boot (their state is unknown). The listing is
// split without globbing (set -f), and a name that would read as an option
// to service is not asked about.
func freebsdServiceProbes() []gatherProbe {
	return []gatherProbe{{
		subset: gatherSubsetServices,
		family: gatherFamilyService,
		// A non-root login on a host hiding other users' processes
		// (security.bsd.see_other_uids=0) cannot see a running daemon, and rc
		// then says it is not running; the probe says so first
		// (procsHiddenMark), and such a daemon's state is unknown (sweep
		// item 11, a repeat in kind of 818). Each listing that fails fails
		// the probe.
		present: `command -v service >/dev/null 2>&1`,
		command: orFailed(`service -l`) + `; echo '` + serviceSectionMark + `'; ` + orFailed(`service -e`) + `; echo '` + serviceSectionMark + `'; ` +
			`if [ "$(id -u)" != 0 ] && ! ps -p 1 >/dev/null 2>&1; then echo '` + procsHiddenMark + `'; fi; ` +
			`set -f; for p in $(service -e); do s=${p##*/}; case $s in -*) continue;; esac; st=$(service "$s" onestatus 2>&1 | head -1); printf '%s\t%s\n' "$s" "$st"; done`,
		parse: parseRC,
		check: checkRC,
	}}
}

// parseRC reads freebsdServiceProbes' three sections. An rcorder placeholder
// (DAEMON, LOGIN: all capitals) is not a service and is left out.
func parseRC(out string) []gatherFact {
	sections := strings.Split(out, serviceSectionMark)
	if len(sections) != 3 {
		return nil
	}
	enabled := map[string]bool{}
	for _, line := range strings.Split(sections[1], "\n") {
		if p := strings.TrimSpace(line); p != "" {
			enabled[p[strings.LastIndex(p, "/")+1:]] = true
		}
	}
	state := map[string]string{}
	hidden := strings.Contains(sections[2], procsHiddenMark)
	for _, line := range strings.Split(sections[2], "\n") {
		name, status, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		switch {
		case strings.Contains(status, " is running"):
			state[name] = "running"
		case strings.Contains(status, " is not running") && !hidden:
			// Where other users' processes are hidden, rc cannot tell a
			// stopped daemon from one it cannot see: unknown.
			state[name] = "stopped"
		}
	}
	var facts []gatherFact
	seen := map[string]bool{}
	for _, line := range strings.Split(sections[0], "\n") {
		name := strings.TrimSpace(line)
		if name == "" || seen[name] || placeholder(name) {
			continue
		}
		seen[name] = true
		st := state[name]
		if st == "" {
			st = "unknown"
		}
		status := "disabled"
		if enabled[name] {
			status = "enabled"
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyService, name), value: map[string]any{
			"state": st, "enabled": enabled[name], "status": status, "source": "rc",
		}})
	}
	return facts
}

// placeholder reports whether an rc script's name is all capitals, as
// rcorder's ordering placeholders are.
func placeholder(name string) bool {
	for _, r := range name {
		if unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// darwinPackageProbes reads Homebrew's formulae and casks, where Homebrew is
// installed in either of its places (it is not on a non-login shell's PATH),
// and pkgutil's installer receipts. brew is a script whoever owns Homebrew's
// prefix can change, so it is run only by the account that owns it and never
// as root: run as anyone else, it would run that owner's code with this
// login's rights. A login that does not own Homebrew reports no brew
// packages.
func darwinPackageProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset: gatherSubsetPackages,
			family: gatherFamilyPackage,
			// Present when a Homebrew this login owns is installed and the
			// login is not root; each list that fails fails the probe, where
			// the loop's break used to make every failure exit 0.
			present: `( for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$b" ] && [ -O "$b" ] && [ "$(id -u)" != 0 ] && exit 0; done; exit 1 )`,
			command: `for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do if [ -x "$b" ] && [ -O "$b" ]; then ` +
				orFailed(`HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$b" list --versions --formula`) + `; echo '` + serviceSectionMark + `'; ` +
				orFailed(`HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$b" list --versions --cask`) + `; break; fi; done`,
			parse: func(out string) []gatherFact { return packageFacts(parseBrew(out)) },
		},
		{
			subset: gatherSubsetPackages,
			family: gatherFamilyPackage,
			// pkgutil --pkgs failing ran the loop zero times and exited 0;
			// it fails the probe now, and so does an empty listing, since
			// macOS always holds Apple's own receipts.
			present: `command -v pkgutil >/dev/null 2>&1`,
			command: `set -f; if pkgs=$(pkgutil --pkgs); then for p in $pkgs; do case $p in -*) continue;; esac; pkgutil --pkg-info "$p"; echo; done; ` +
				`else echo '` + gatherFailedMark + `'; fi`,
			parse: func(out string) []gatherFact { return packageFacts(parsePkgutil(out)) },
			check: listsSomething("pkgutil --pkgs"),
		},
	}
}

// parseBrew reads brew list --versions' lines ("name 1.2.3 [1.2.4]") for
// formulae, then after the mark for casks; a name with several versions
// installed reports the last brew lists.
func parseBrew(out string) []installedPackage {
	formulae, casks, _ := strings.Cut(out, serviceSectionMark)
	var pkgs []installedPackage
	for i, part := range []string{formulae, casks} {
		source := "brew"
		if i == 1 {
			source = "brew-cask"
		}
		for _, line := range strings.Split(part, "\n") {
			fields := strings.Fields(line)
			// Homebrew's own status lines ("==> Downloading ...") are not
			// packages.
			if len(fields) < 2 || strings.HasPrefix(fields[0], "==>") {
				continue
			}
			pkgs = append(pkgs, installedPackage{name: fields[0], version: fields[len(fields)-1], source: source})
		}
	}
	return pkgs
}

// parsePkgutil reads pkgutil --pkg-info's blocks (package-id: and version:
// lines, a blank line between receipts).
func parsePkgutil(out string) []installedPackage {
	var pkgs []installedPackage
	var id, version string
	flush := func() {
		if id != "" && version != "" {
			pkgs = append(pkgs, installedPackage{name: id, version: version, source: "pkgutil"})
		}
		id, version = "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case ok && key == "package-id":
			flush()
			id = strings.TrimSpace(value)
		case ok && key == "version":
			version = strings.TrimSpace(value)
		}
	}
	flush()
	return pkgs
}

// darwinServiceProbes reads launchd's system domain (its daemons, readable
// without root) and which of them are disabled.
func darwinServiceProbes() []gatherProbe {
	return []gatherProbe{{
		subset:  gatherSubsetServices,
		family:  gatherFamilyService,
		present: `command -v launchctl >/dev/null 2>&1`,
		command: orFailed(`launchctl print system`) + `; echo '` + serviceSectionMark + `'; ` + orFailed(`launchctl print-disabled system`),
		parse:   parseLaunchd,
		check:   checkLaunchd,
	}}
}

// parseLaunchd reads launchctl print system's services block (pid, last
// exit status, label; a pid of 0 is not running) and print-disabled's
// ("label" => disabled) list. A daemon launchd lists as neither is enabled.
func parseLaunchd(out string) []gatherFact {
	printed, disabledList, found := strings.Cut(out, serviceSectionMark)
	if !found {
		return nil
	}
	disabled := map[string]bool{}
	for _, line := range strings.Split(disabledList, "\n") {
		label, verdict, ok := strings.Cut(strings.TrimSpace(line), "=>")
		if ok && strings.TrimSpace(verdict) == "disabled" {
			disabled[strings.Trim(strings.TrimSpace(label), `"`)] = true
		}
	}
	var facts []gatherFact
	in := false
	for _, line := range strings.Split(printed, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && strings.HasPrefix(line, "\tservices = {"):
			in = true
			continue
		case in && strings.HasPrefix(line, "\t}"):
			in = false
			continue
		case !in:
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			continue
		}
		label := fields[len(fields)-1]
		state := "stopped"
		if fields[0] != "0" && fields[0] != "-" {
			state = "running"
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyService, label), value: map[string]any{
			"state": state, "enabled": !disabled[label], "status": fields[1], "source": "launchd",
		}})
	}
	return facts
}

// procsHiddenMark is the line the FreeBSD services probe prints when this
// login cannot see other users' processes.
const procsHiddenMark = "--pleiades-procs-hidden--"

// checkRC refuses an rc answer that lacks its three sections or lists no
// script: rc always has some.
func checkRC(out string) error {
	sections := strings.Split(out, serviceSectionMark)
	if len(sections) != 3 {
		return fmt.Errorf("it did not print its three sections")
	}
	if strings.TrimSpace(sections[0]) == "" {
		return fmt.Errorf("service -l named no script")
	}
	return nil
}

// checkLaunchd refuses a launchd answer without its two sections or without
// the system domain's services block, which always exists: launchctl's print
// output is not an API (launchctl(1)), and a layout this does not read would
// otherwise answer "no services".
func checkLaunchd(out string) error {
	printed, _, found := strings.Cut(out, serviceSectionMark)
	if !found {
		return fmt.Errorf("it did not print its two sections")
	}
	for _, line := range strings.Split(printed, "\n") {
		if strings.HasPrefix(line, "\tservices = {") {
			return nil
		}
	}
	return fmt.Errorf("launchctl print system has no services block this reads")
}
