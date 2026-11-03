// Package facts: the packages and services probes of FreeBSD and macOS
// (Phase 41c). FreeBSD's packages come from pkg and its services from rc;
// a Mac's packages from Homebrew and pkgutil's receipts, and its services
// from launchd's system domain. Each is read without root: a FreeBSD daemon
// whose pid file only root may read reports its state as unknown, never as
// stopped.
package facts

import (
	"strings"
	"unicode"
)

// freebsdPackageProbes reads pkg's database, where pkg is bootstrapped
// (pkg -N; an unbootstrapped pkg would offer to install itself).
func freebsdPackageProbes() []gatherProbe {
	return []gatherProbe{{
		subset:  gatherSubsetPackages,
		family:  gatherFamilyPackage,
		command: `pkg -N >/dev/null 2>&1 && pkg query '%n\t%v\t%q'`,
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
		command: `command -v service >/dev/null 2>&1 && { service -l; echo '` + serviceSectionMark + `'; service -e; echo '` + serviceSectionMark + `'; ` +
			`set -f; for p in $(service -e); do s=${p##*/}; case $s in -*) continue;; esac; st=$(service "$s" onestatus 2>&1 | head -1); printf '%s\t%s\n' "$s" "$st"; done; }`,
		parse: parseRC,
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
	for _, line := range strings.Split(sections[2], "\n") {
		name, status, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		switch {
		case strings.Contains(status, " is running"):
			state[name] = "running"
		case strings.Contains(status, " is not running"):
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
			command: `for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do if [ -x "$b" ] && [ -O "$b" ] && [ "$(id -u)" != 0 ]; then ` +
				`HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$b" list --versions --formula; echo '` + serviceSectionMark + `'; ` +
				`HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$b" list --versions --cask; break; fi; done`,
			parse: func(out string) []gatherFact { return packageFacts(parseBrew(out)) },
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: `command -v pkgutil >/dev/null 2>&1 && set -f && for p in $(pkgutil --pkgs); do case $p in -*) continue;; esac; pkgutil --pkg-info "$p"; echo; done`,
			parse:   func(out string) []gatherFact { return packageFacts(parsePkgutil(out)) },
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
			if len(fields) < 2 {
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
		command: `command -v launchctl >/dev/null 2>&1 && { launchctl print system; echo '` + serviceSectionMark + `'; launchctl print-disabled system; }`,
		parse:   parseLaunchd,
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
