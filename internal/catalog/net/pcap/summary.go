// Package pcap implements net.pcap.summary: a packet capture in the project
// (virt.cml.capture.stop writes one) counted by protocol and address pair,
// so that traffic between sites crossing encrypted is proven from the
// packets that crossed the wire rather than from either router's counters.
package pcap

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fqcn is the method's name.
const fqcn = "net.pcap.summary"

// The parameters.
const (
	paramSrc   = "src"
	paramPairs = "pairs"
)

// maxCapture bounds the capture read: what virt.cml.capture.stop writes.
const maxCapture = sdk.MaxProjectFileWrite

// defaultPairs is how many address pairs the result lists unless told.
const defaultPairs = 20

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcn,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			// It reads a file of the project in the command line's own
			// process and touches no device.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceNone,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "It reads a capture file and changes nothing, so there is nothing to undo.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc:           SummaryDoc(),
		},
		Invoke: Summary,
		// Summary itself, since it only reads.
		Check: Summary,
	})
}

// SummaryDoc is net.pcap.summary's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func SummaryDoc() collection.Doc {
	return collection.Doc{
		Summary: "Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext.",
		Description: "Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP), the key exchange that set it up (IKE), or cleartext (everything else carried over IP, which anyone on the wire can read). The same counts are given for each address pair, most packets first, so \"the sites' traffic crosses encrypted\" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent; GRE in the clear between two sites is DMVPN without its IPsec. " +
			"It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: \"nothing crossed in the clear\" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.",
		Params: []collection.Param{
			{Name: paramSrc, Type: "string", Required: true, Description: "The capture file, a path relative to the project directory, such as captures/core1-wan.pcap."},
			{Name: paramPairs, Type: "int", Default: "20", Description: "How many address pairs to list, most packets first; 0 lists none."},
		},
		Returns: []collection.ReturnField{
			{Name: "format", Type: "string", Returned: "always", Description: "pcap or pcapng."},
			{Name: "packets", Type: "int", Returned: "always", Description: "Every frame in the capture."},
			{Name: "bytes", Type: "int", Returned: "always", Description: "Their lengths on the wire, summed."},
			{Name: "protocols", Type: "dict", Returned: "always", Description: "Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, tcp, udp, arp, non-ip and the rest)."},
			{Name: "encrypted", Type: "int", Returned: "always", Description: "IP packets that were ESP or ESP in UDP."},
			{Name: "key_exchange", Type: "int", Returned: "always", Description: "IP packets that were IKE."},
			{Name: "cleartext", Type: "int", Returned: "always", Description: "Every other IP packet: what a reader of the wire saw in the clear."},
			{Name: "pairs", Type: "list of dict", Returned: "always", Description: "Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext."},
			{Name: "link_types", Type: "list of string", Returned: "always", Description: "The link types the capture declares."},
			{Name: "unread", Type: "int", Returned: "always", Description: "Frames of a link type this method does not read."},
			{Name: "unexamined", Type: "int", Returned: "always", Description: "Frames this method could not judge encrypted or not: unread ones, and every one carrying no IP (non-ip)."},
		},
		Examples: []collection.Example{
			{Name: "Count what crossed a WAN link", RunbookYAML: "- name: Count what crossed CORE1's WAN link\n  net.pcap.summary:\n    src: captures/core1-wan.pcap\n  register: wan\n"},
		},
		SeeAlso: []string{"virt.cml.capture.start", "virt.cml.capture.stop"},
	}
}

// Summary implements "net.pcap.summary"; it is its own check.
func Summary(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	src, err := sdk.RequiredStringParam(params, paramSrc)
	if err != nil {
		return fail(err)
	}
	limit, set, err := sdk.IntParam(params, paramPairs)
	if err != nil {
		return fail(err)
	}
	if !set {
		limit = defaultPairs
	}
	if limit < 0 {
		return fail(fmt.Errorf("%s is 0 or more", paramPairs))
	}
	files, ok := rc.(sdk.ProjectFiles)
	if !ok {
		return fail(sdk.ErrNoProjectFiles)
	}
	data, err := files.ReadProjectFile(src, maxCapture)
	if err != nil {
		return fail(err)
	}
	s, readErr := pcapsum.Read(data)
	pairs := s.Pairs
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	listed := make([]any, len(pairs))
	for i, p := range pairs {
		listed[i] = map[string]any{"a": p.A, "b": p.B, "protocols": counters(p.Protocols), "encrypted": p.Encrypted, "key_exchange": p.KeyExchange, "cleartext": p.Cleartext}
	}
	linkTypes := make([]any, len(s.LinkTypes))
	for i, l := range s.LinkTypes {
		linkTypes[i] = l
	}
	for k, v := range map[string]any{
		"format": s.Format, "packets": s.Packets, "bytes": s.Bytes, "protocols": counters(s.Protocols),
		"encrypted": s.Encrypted, "key_exchange": s.KeyExchange, "cleartext": s.Cleartext,
		"pairs": listed, "link_types": linkTypes, "unread": s.Unread,
		"unexamined": s.Unread + s.Protocols[pcapsum.NonIP].Packets,
	} {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	if readErr != nil {
		return fail(fmt.Errorf("%s: %w (counted up to there)", src, readErr))
	}
	return collection.Result{}, nil
}

// counters is a protocol table as a result holds it.
func counters(m map[string]pcapsum.Counter) map[string]any {
	out := make(map[string]any, len(m))
	for k, c := range m {
		out[k] = map[string]any{"packets": c.Packets, "bytes": c.Bytes}
	}
	return out
}
