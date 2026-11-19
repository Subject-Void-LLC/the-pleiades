// Package hostfacts: systemd's service facts, shared by facts.gather's
// services subset and the svc.systemd.* methods that read a unit back after
// changing it (Phase 41c). Both run the same three listings and parse them the
// same way, so the facts a method emits are the very facts the next gather
// stores: service.<unit> = {enabled, status, source}, and service_state.<unit>
// = its state, split off since it moves on its own.
package hostfacts

import (
	"fmt"
	"strings"
)

// The service families: the service itself, stable, and its state, volatile.
const (
	ServiceFamily      = "service."
	ServiceStateFamily = "service_state."
)

// ServiceSectionMark separates the three listings one systemd read prints, so
// a listing that prints nothing cannot shift another into its place.
const ServiceSectionMark = "--pleiades-units--"

// SystemdListings are the three listings a systemd read runs, in order: unit
// files (whether each is enabled), loaded units (what each is doing) and loaded
// sockets (which tell a per-connection instance from a service). Every option
// before the verb: under POSIXLY_CORRECT systemctl's parser stops at the first
// word that is not an option.
var SystemdListings = []string{
	`systemctl --type=service --no-legend --no-pager --plain list-unit-files`,
	`systemctl --type=service --all --no-legend --no-pager --plain list-units`,
	`systemctl --type=socket --all --no-legend --no-pager --plain list-units`,
}

// Fact is one fact as a read makes it.
type Fact struct {
	Key   string
	Value any
}

// systemdEnabled are the unit-file states that start a unit at boot.
var systemdEnabled = map[string]bool{"enabled": true, "enabled-runtime": true}

// ParseSystemd reads the three listings: unit files (unit, state, preset),
// loaded units (unit, load, active, sub, description), then loaded sockets. A
// unit file that no loaded unit names is inactive; a template (name@.service)
// is a pattern for units, not a service, and is left out, and so is a unit
// that lasts only as long as a login, a connection or a process
// (SessionUnit). Each fact still holds its state; SplitServiceState takes it
// out.
func ParseSystemd(out string) []Fact {
	sections := strings.Split(out, ServiceSectionMark)
	if len(sections) != 3 {
		// CheckSystemd refuses such an answer before it is parsed.
		return nil
	}
	files, units, socketList := sections[0], sections[1], sections[2]
	sockets := map[string]bool{}
	for _, line := range strings.Split(socketList, "\n") {
		if fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "●")); len(fields) > 0 && strings.HasSuffix(fields[0], ".socket") {
			sockets[fields[0]] = true
		}
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
	out2 := make([]Fact, 0, len(order))
	for _, name := range order {
		s := services[name]
		if SessionUnit(name, s.status, sockets) {
			continue
		}
		out2 = append(out2, Fact{Key: ItemKey(ServiceFamily, name), Value: map[string]any{
			"state": s.state, "enabled": systemdEnabled[s.status], "status": s.status, "source": "systemd",
		}})
	}
	return out2
}

// loginUnitTemplates are the templates systemd-logind starts one instance of
// per user while that user has a session or lingers: who is logged in, not a
// service anyone configured.
var loginUnitTemplates = map[string]bool{"user@.service": true, "user-runtime-dir@.service": true}

// SessionUnit reports whether a loaded unit lasts only as long as a login, a
// connection or a process, so its key would come and go on a host that did not
// change: a transient unit (unit file state "transient"), an instance of
// user@.service or user-runtime-dir@.service, or a per-connection instance of a
// socket with Accept=yes (<socket>@<n>-..., only while <socket>.socket is
// loaded). Every other instance stays, getty@tty1.service among them.
func SessionUnit(name, status string, sockets map[string]bool) bool {
	if status == "transient" {
		return true
	}
	prefix, instance, isInstance := strings.Cut(strings.TrimSuffix(name, ".service"), "@")
	if !isInstance {
		return false
	}
	if loginUnitTemplates[prefix+"@.service"] {
		return true
	}
	// The connection's number, then a dash: systemd's own shape for every
	// socket family.
	digits := len(instance) - len(strings.TrimLeft(instance, "0123456789"))
	return digits > 0 && strings.HasPrefix(instance[digits:], "-") && sockets[prefix+".socket"]
}

// CheckSystemd refuses a systemd answer that lacks any of its three sections,
// or whose loaded units name no service: a booted systemd always has loaded
// services, so an empty listing is one that did not answer. An answer cut off
// before the socket listing is refused rather than parsed with no sockets,
// which would keep every per-connection instance SessionUnit leaves out.
func CheckSystemd(out string) error {
	sections := strings.Split(out, ServiceSectionMark)
	if len(sections) != 3 {
		return fmt.Errorf("it did not print its three sections")
	}
	for _, line := range strings.Split(sections[1], "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "●"))
		if len(fields) >= 4 && strings.HasSuffix(fields[0], ".service") {
			return nil
		}
	}
	return fmt.Errorf("systemctl list-units named no loaded service, which a booted systemd always has")
}

// SplitServiceState takes the state out of a service fact and returns it as the
// service_state fact of the same item, its key the service's own after the
// family's prefix. A fact of another family, or a service with no state, is
// returned as it is.
func SplitServiceState(f Fact) []Fact {
	value, ok := f.Value.(map[string]any)
	name, isService := strings.CutPrefix(f.Key, ServiceFamily)
	if !ok || !isService {
		return []Fact{f}
	}
	state, hasState := value["state"].(string)
	rest := make(map[string]any, len(value))
	for k, v := range value {
		if k != "state" {
			rest[k] = v
		}
	}
	out := []Fact{{Key: f.Key, Value: rest}}
	if hasState && state != "" {
		out = append(out, Fact{Key: ServiceStateFamily + name, Value: state})
	}
	return out
}
