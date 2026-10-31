// Package linuxnet holds the reference text of the net.linux methods, which
// manage a Linux host's bridges, addresses and routes through iproute2's ip.
// The behavior is in internal/catalog/net/linux; the text is here so the
// catalog's data (internal/forge/catalogdata) and the registered manifests are
// built from one definition and cannot disagree.
package linuxnet

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// Spec is one net.linux method's reference text.
type Spec struct {
	// Name is the method's FQCN.
	Name string
	// Doc is its reference documentation.
	Doc collection.Doc
}

// privilege is every method's closing sentence about the rights it needs.
const privilege = " It runs ip as the account the task connects as, and changing the network needs root or CAP_NET_ADMIN: a task whose login lacks it fails with ip's own refusal, and become: true runs the task as root through sudo -n (see Writing runbooks). A check reads what a run reads and changes nothing."

// stateParam is the state parameter every method takes.
var stateParam = collection.Param{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present or absent."}

// hostKeyParam is the SSH host key parameter every SSH method takes.
var hostKeyParam = collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

// Specs returns the three methods' reference text, in the order its page lists
// them. The slice is fresh on each call.
func Specs() []Spec {
	return []Spec{
		{Name: "net.linux.bridge", Doc: collection.Doc{
			Summary:     "Makes a Linux bridge exist with the ports given, or not exist.",
			Description: "Reads the bridge from /sys/class/net and changes only what differs: creates it, brings it up, and attaches each port in ports that is not on it. A device of that name that is not a bridge is refused, never replaced. Ports not listed are left alone unless remove_other_ports is set. Its undo puts back what it found: a bridge it created is removed, ports it attached are detached, and a bridge it removed is made again with the ports it held (partial: a port's own addresses and settings are not recorded)." + privilege,
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The bridge's interface name."},
				stateParam,
				{Name: "ports", Type: "list of string", Description: "Interfaces that must be attached to the bridge."},
				{Name: "up", Type: "bool", Default: "true", Description: "Whether the bridge is administratively up."},
				{Name: "remove_other_ports", Type: "bool", Default: "false", Description: "Detach interfaces attached to the bridge that ports does not name."},
				{Name: "detach_ports", Type: "list of string", Description: "Interfaces to detach from the bridge. Set by an undo."},
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the bridge exists now."},
				{Name: "ports", Type: "list of string", Returned: "when it exists", Description: "Its ports now, sorted."},
				{Name: "up", Type: "bool", Returned: "when it exists", Description: "Whether it is up now."},
			},
			Examples: []collection.Example{{Name: "A switch of two ports", RunbookYAML: "- name: br0 joins eth1 and eth2\n  net.linux.bridge:\n    name: br0\n    ports: [eth1, eth2]\n  become: true\n"}},
			SeeAlso:  []string{"net.linux.address", "net.linux.route", "net.ovs.bridge"},
		}},
		{Name: "net.linux.address", Doc: collection.Doc{
			Summary:     "Makes an address present on a Linux interface, or absent.",
			Description: "Reads the interface's addresses (ip -o addr show) and adds or deletes only the one named, so a run that repeats it is unchanged. The interface must exist. Its undo is the other state of the same address." + privilege,
			Params: []collection.Param{
				{Name: "interface", Type: "string", Required: true, Description: "The interface (eth1, br0)."},
				{Name: "address", Type: "string", Required: true, Description: "The address with its prefix length, IPv4 or IPv6 (10.1.1.1/24)."},
				stateParam,
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "present", Type: "bool", Returned: "always", Description: "Whether the address is on the interface now."},
				{Name: "addresses", Type: "list of string", Returned: "always", Description: "The interface's addresses now."},
			},
			Examples: []collection.Example{{Name: "Give an interface an address", RunbookYAML: "- name: br0 has its address\n  net.linux.address:\n    interface: br0\n    address: 10.1.1.1/24\n  become: true\n"}},
			SeeAlso:  []string{"net.linux.bridge", "net.linux.route"},
		}},
		{Name: "net.linux.route", Doc: collection.Doc{
			Summary:     "Makes a route present in a Linux host's main routing table, or absent.",
			Description: "Reads the routing table (ip route show) and acts only when the route differs: a route to the same destination and metric through another gateway or device is replaced, and its undo puts the old one back exactly; a new route's undo removes it. A route removed is made again by its undo." + privilege,
			Params: []collection.Param{
				{Name: "destination", Type: "string", Required: true, Description: "The destination prefix (10.2.2.0/24), or default."},
				{Name: "via", Type: "string", Description: "The gateway address. Set via, dev or both."},
				{Name: "dev", Type: "string", Description: "The outgoing interface."},
				{Name: "metric", Type: "int", Description: "The route's metric; the kernel's default when unset."},
				stateParam,
				hostKeyParam,
			},
			Returns: []collection.ReturnField{
				{Name: "present", Type: "bool", Returned: "always", Description: "Whether the route is in the table now."},
			},
			Examples: []collection.Example{{Name: "Reach another LAN through the router", RunbookYAML: "- name: h1 reaches LAN B through r1\n  net.linux.route:\n    destination: 10.2.2.0/24\n    via: 10.1.1.1\n  become: true\n"}},
			SeeAlso:  []string{"net.linux.address", "net.frr.config"},
		}},
	}
}
