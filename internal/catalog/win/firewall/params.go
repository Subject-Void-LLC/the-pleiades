// win.firewall.rule's parameters, each held to its form before WinRM is
// reached.
package firewall

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The rule's settings this method manages, as its parameters name them.
const (
	fwDisplayName   = "display_name"
	fwDescription   = "description"
	fwEnabled       = "enabled"
	fwDirection     = "direction"
	fwAction        = "action"
	fwProfiles      = "profiles"
	fwProtocol      = "protocol"
	fwLocalPort     = "local_port"
	fwRemotePort    = "remote_port"
	fwLocalAddress  = "local_address"
	fwRemoteAddress = "remote_address"
	fwProgram       = "program"
	fwService       = "service"
)

// fwFields are the managed settings, in the order a difference is named.
var fwFields = []string{fwDisplayName, fwDescription, fwEnabled, fwDirection, fwAction, fwProfiles, fwProtocol,
	fwLocalPort, fwRemotePort, fwLocalAddress, fwRemoteAddress, fwProgram, fwService}

var (
	// ruleName is a rule's Name, its identity: a built-in one such as
	// OpenSSH-Server-In-TCP, or a GUID in braces.
	ruleName = regexp.MustCompile(`^[A-Za-z0-9{][A-Za-z0-9 ._{}()@,+-]{0,254}$`)
	// serviceName is a service's short name.
	serviceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,256}$`)
	// portKeywords are the named local ports Windows accepts.
	portKeywords = []string{"rpc", "rpcepmap", "iphttps", "iphttpsin", "iphttpsout", "teredo", "playtodiscovery"}
	// addressKeywords are the named addresses Windows accepts.
	addressKeywords = []string{"localsubnet", "localsubnet4", "localsubnet6", "dns", "dhcp", "wins", "defaultgateway",
		"internet", "intranet", "intranetremoteaccess", "playtodevice"}
	profileNames = []string{"domain", "private", "public"}
	protocols    = map[string]string{"tcp": "tcp", "udp": "udp", "icmpv4": "icmpv4", "icmpv6": "icmpv6", "any": "any",
		"6": "tcp", "17": "udp", "1": "icmpv4", "58": "icmpv6"}
)

// rule is a call: the rule, what to do with it, and for state present the
// settings it gave, in their canonical form (want) and as sent (send).
type rule struct {
	name, state string
	want, send  map[string]any
}

// parseRule reads a call, holding every value to its form.
func parseRule(params map[string]any) (rule, error) {
	r := rule{want: map[string]any{}, send: map[string]any{}}
	var err error
	if r.name, err = sdk.RequiredStringParam(params, paramName); err != nil {
		return r, err
	}
	if !ruleName.MatchString(r.name) {
		return r, fmt.Errorf("%s %q is not a firewall rule's name: letters, digits, space and ._{}()@,+-, at most 255", paramName, r.name)
	}
	if r.state = sdk.StringParam(params, paramState); r.state == "" {
		r.state = "present"
	}
	switch r.state {
	case "query", "absent":
		return r, nil
	case "present":
	default:
		return r, fmt.Errorf("%s %q is not present, absent or query", paramState, r.state)
	}
	for _, f := range []string{fwDisplayName, fwDescription} {
		v, err := sdk.OptionalStringParam(params, f)
		if err != nil {
			return r, err
		}
		if _, given := params[f]; !given {
			continue
		}
		if len(v) > 1000 || strings.ContainsAny(v, "\x00\r\n") || (f == fwDisplayName && v == "") {
			return r, fmt.Errorf("%s is empty, over 1000 characters or holds a line break", f)
		}
		r.want[f], r.send[f] = v, v
	}
	if _, given := params[fwEnabled]; given {
		on, err := sdk.BoolParamOr(params, fwEnabled, true)
		if err != nil {
			return r, err
		}
		r.want[fwEnabled], r.send[fwEnabled] = on, map[bool]string{true: "True", false: "False"}[on]
	}
	for f, choices := range map[string][]string{fwDirection: {"inbound", "outbound"}, fwAction: {"allow", "block"}} {
		if v := strings.ToLower(sdk.StringParam(params, f)); v != "" {
			if !slices.Contains(choices, v) {
				return r, fmt.Errorf("%s is one of %v", f, choices)
			}
			r.want[f], r.send[f] = v, strings.ToUpper(v[:1])+v[1:]
		}
	}
	if err := r.parseLists(params); err != nil {
		return r, err
	}
	for f, pattern := range map[string]*regexp.Regexp{fwProgram: nil, fwService: serviceName} {
		v, err := sdk.OptionalStringParam(params, f)
		if err != nil {
			return r, err
		}
		if _, given := params[f]; !given {
			continue
		}
		bad := v == "" || len(v) > 260 || strings.ContainsAny(v, "\"*?<>|\x00\r\n")
		if pattern != nil && !strings.EqualFold(v, "any") && !pattern.MatchString(v) {
			bad = true
		}
		if bad {
			return r, fmt.Errorf("%s %q is not a program path or service name (or any)", f, v)
		}
		r.want[f], r.send[f] = strings.ToLower(v), v
	}
	return r, nil
}

// parseLists reads the profiles, protocol, ports and addresses.
func (r *rule) parseLists(params map[string]any) error {
	if _, given := params[fwProfiles]; given {
		list, err := stringList(params, fwProfiles)
		if err != nil {
			return err
		}
		set, err := profileSet(list)
		if err != nil {
			return err
		}
		r.want[fwProfiles] = set
		send := make([]string, len(set))
		for i, p := range set {
			send[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		r.send[fwProfiles] = send
	}
	if _, given := params[fwProtocol]; given {
		p := strings.ToLower(fmt.Sprint(params[fwProtocol]))
		canon, ok := protocols[p]
		if !ok {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 255 {
				return fmt.Errorf("%s %q is tcp, udp, icmpv4, icmpv6, any or a protocol number 0-255", fwProtocol, p)
			}
			canon = p
		}
		r.want[fwProtocol], r.send[fwProtocol] = canon, canon
	}
	for _, f := range []string{fwLocalPort, fwRemotePort, fwLocalAddress, fwRemoteAddress} {
		if _, given := params[f]; !given {
			continue
		}
		list, err := stringList(params, f)
		if err != nil {
			return err
		}
		canon, err := canonicalList(f, list)
		if err != nil {
			return err
		}
		r.want[f], r.send[f] = canon, list
	}
	return nil
}
