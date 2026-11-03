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
// doing), read in one command.
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
	}
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
