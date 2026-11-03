// Package facts: the services subset's probes (Phase 41c). Each lists every
// service one service manager knows in one fixed command, guarded so a
// device without that manager prints nothing, and is parsed here into one
// fact per service: service.<name> = {state, enabled, status, source}.
package facts

import (
	"strings"
)

// serviceSectionMark separates the two listings one systemd probe prints,
// so a listing that prints nothing cannot shift the other into its place.
const serviceSectionMark = "--pleiades-units--"

// linuxServiceProbes are the services subset's probes on Linux: systemd's
// unit files (whether each is enabled) and its loaded units (what each is
// doing), read in one command, and OpenRC's scripts, runlevels and states.
func linuxServiceProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset: gatherSubsetServices,
			family: gatherFamilyService,
			// Every option before the verb: under POSIXLY_CORRECT (set by
			// some shells and by the SSH test harness) systemctl's parser
			// stops at the first word that is not an option, and options
			// after list-unit-files were read as unit names.
			command: `command -v systemctl >/dev/null 2>&1 && { systemctl --type=service --no-legend --no-pager --plain list-unit-files; ` +
				`echo '` + serviceSectionMark + `'; systemctl --type=service --all --no-legend --no-pager --plain list-units; }`,
			parse: parseSystemd,
		},
		{
			subset: gatherSubsetServices,
			family: gatherFamilyService,
			// OpenRC (Alpine, Gentoo), where systemd is not what booted:
			// every init script, the runlevels each is in, and, once OpenRC
			// has booted (its softlevel exists), each one's state. None of
			// it needs root.
			command: `command -v rc-status >/dev/null 2>&1 && [ ! -d /run/systemd/system ] && { rc-service --list; echo '` + serviceSectionMark + `'; ` +
				`rc-update show -v; echo '` + serviceSectionMark + `'; [ -e /run/openrc/softlevel ] && rc-status --all -f ini 2>/dev/null; }`,
			parse: parseOpenRC,
		},
	}
}

// openrcStates are OpenRC's words for a service's state, in the words the
// other managers' facts use where one means the same.
var openrcStates = map[string]string{"started": "running", "stopped": "stopped"}

// parseOpenRC reads the OpenRC probe's three sections: every init script,
// rc-update's runlevels per script ("sshd | default"), and rc-status's ini
// listing ("[default]", "sshd = started"). A script in a runlevel other than
// shutdown starts with the system; status is its runlevels, or disabled. A
// script rc-status does not list is stopped, and every one is unknown when
// OpenRC has not booted (rc-status printed nothing).
func parseOpenRC(out string) []gatherFact {
	sections := strings.Split(out, serviceSectionMark)
	if len(sections) != 3 {
		return nil
	}
	runlevels := map[string][]string{}
	for _, line := range strings.Split(sections[1], "\n") {
		name, levels, ok := strings.Cut(line, "|")
		if name = strings.TrimSpace(name); ok && name != "" {
			runlevels[name] = strings.Fields(levels)
		}
	}
	state := map[string]string{}
	booted := false
	for _, line := range strings.Split(sections[2], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			booted = true
			continue
		}
		name, st, ok := strings.Cut(line, "=")
		if name, st = strings.TrimSpace(name), strings.TrimSpace(st); ok && name != "" && st != "" {
			if word, known := openrcStates[st]; known {
				st = word
			}
			state[name] = st
		}
	}
	var facts []gatherFact
	seen := map[string]bool{}
	for _, line := range strings.Split(sections[0], "\n") {
		name := strings.TrimSpace(line)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		st := state[name]
		switch {
		case !booted:
			st = "unknown"
		case st == "":
			st = "stopped"
		}
		enabled := false
		for _, level := range runlevels[name] {
			enabled = enabled || level != "shutdown"
		}
		status := strings.Join(runlevels[name], ",")
		if status == "" {
			status = "disabled"
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyService, name), value: map[string]any{
			"state": st, "enabled": enabled, "status": status, "source": "openrc",
		}})
	}
	return facts
}

// systemdEnabled are the unit-file states that start a unit at boot.
var systemdEnabled = map[string]bool{"enabled": true, "enabled-runtime": true}

// parseSystemd reads the two listings: unit files (unit, state, preset)
// then loaded units (unit, load, active, sub, description). A unit file that
// no loaded unit names is inactive; a template (name@.service) is a pattern
// for units, not a service, and is left out.
func parseSystemd(out string) []gatherFact {
	files, units, found := strings.Cut(out, serviceSectionMark)
	if !found {
		return nil
	}
	type service struct{ state, status string }
	services := map[string]*service{}
	var order []string
	for _, line := range strings.Split(files, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasSuffix(fields[0], ".service") || strings.HasSuffix(fields[0], "@.service") {
			continue
		}
		if _, ok := services[fields[0]]; !ok {
			order = append(order, fields[0])
		}
		services[fields[0]] = &service{state: "inactive", status: fields[1]}
	}
	for _, line := range strings.Split(units, "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "●"))
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		s, ok := services[fields[0]]
		if !ok {
			// A loaded unit with no file of its own (an instance of a
			// template, a transient unit) has no enabled state to report.
			s = &service{status: "unknown"}
			services[fields[0]] = s
			order = append(order, fields[0])
		}
		s.state = fields[3]
	}
	out2 := make([]gatherFact, 0, len(order))
	for _, name := range order {
		s := services[name]
		out2 = append(out2, gatherFact{key: itemKey(gatherFamilyService, name), value: map[string]any{
			"state": s.state, "enabled": systemdEnabled[s.status], "status": s.status, "source": "systemd",
		}})
	}
	return out2
}
