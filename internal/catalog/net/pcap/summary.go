// Package pcap implements net.pcap.summary: a packet capture in the project
// (virt.cml.capture.stop writes one) counted by protocol and address pair,
// so that traffic between sites crossing encrypted is proven from the
// packets that crossed the wire rather than from either router's counters.
package pcap

import (
	"context"
	"fmt"
	"strings"

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
	paramFlows = "flows"
)

// maxCapture bounds the capture read: what virt.cml.capture.stop writes.
const maxCapture = sdk.MaxProjectFileWrite

// defaultPairs is how many address pairs the result lists unless told, and
// defaultFlows how many flows.
const (
	defaultPairs = 20
	defaultFlows = 20
)

// maxWarnings is how many flows the warnings name one by one; the rest are
// counted in one more line.
const maxWarnings = 10

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
			NoExistsReason:  "it summarizes the capture file the task names; a file not there is refused input, not a lookup",
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
		Description: "Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each TCP connection is also read from its own bytes, both ways in sequence order, and never judged by its port: ssh (an SSH-2.0 banner, its key exchange, then everything after NEWKEYS), tls (a ClientHello or ServerHello record, then each record by its content type), http2 (HTTP/2's client preface in the clear) and grpc (http2 whose request says application/grpc). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data), the key exchange that set it up (IKE, SSH's banner and key exchange, TLS handshake records), or cleartext (everything else carried over IP, which anyone on the wire can read). A packet with no data in an encrypted connection (a SYN, a bare ACK) is counted encrypted. The same counts are given for each address pair, most packets first, so \"the sites' traffic crosses encrypted\" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent; GRE in the clear between two sites is DMVPN without its IPsec. " +
			"HTTP/2 in the clear is cleartext management: it is listed first in flows and a run warns about it. Its header blocks are decoded (HPACK compresses header values, so a search of the bytes finds nothing), and a header that carries a credential (authorization, username, password, a token, a cookie, or one its sender marked never to be indexed) is reported by its name and its connection, never its value, in cleartext_credentials, flows and a warning. Nothing is called encrypted that was not seen to be: a connection whose start the capture lacks, one whose bytes stop making sense as SSH or TLS, and the bytes after a lost segment that hid a record's or packet's header are cleartext (TLS is read again from the next segment that begins with a record). SSH and TLS are known by their framing, not their cipher, so TLS with a NULL cipher suite is counted encrypted, as ESP-NULL is. UDP is read no further than its ports, so DTLS and QUIC are cleartext UDP. " +
			"It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: \"nothing crossed in the clear\" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.",
		Params: []collection.Param{
			{Name: paramSrc, Type: "string", Required: true, Description: "The capture file, a path relative to the project directory, such as captures/core1-wan.pcap.", Format: collection.ParamFormatProjectPath},
			{Name: paramPairs, Type: "int", Default: "20", Description: "How many address pairs to list, most packets first; 0 lists none."},
			{Name: paramFlows, Type: "int", Default: "20", Description: "How many TCP connections named from their bytes to list in flows, those in the clear first; 0 lists none."},
		},
		Returns: []collection.ReturnField{
			{Name: "format", Type: "string", Returned: "always", Description: "pcap or pcapng."},
			{Name: "packets", Type: "int", Returned: "always", Description: "Every frame in the capture."},
			{Name: "bytes", Type: "int", Returned: "always", Description: "Their lengths on the wire, summed."},
			{Name: "protocols", Type: "dict", Returned: "always", Description: "Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, ssh, tls, http2, grpc, tcp, udp, arp, non-ip and the rest)."},
			{Name: "encrypted", Type: "int", Returned: "always", Description: "IP packets that were ESP, ESP in UDP, SSH after NEWKEYS or TLS application data, and those carrying no data in an encrypted connection."},
			{Name: "key_exchange", Type: "int", Returned: "always", Description: "IP packets that were IKE, SSH's banner and key exchange, or TLS handshake records."},
			{Name: "cleartext", Type: "int", Returned: "always", Description: "Every other IP packet: what a reader of the wire saw in the clear."},
			{Name: "pairs", Type: "list of dict", Returned: "always", Description: "Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext."},
			{Name: "flows", Type: "list of dict", Returned: "always", Description: "Each TCP connection named from its bytes (ssh, tls, http2, grpc), those that carried a credential in the clear first, then other cleartext management, then most packets first: client and server (address:port; the client sent the first SYN), protocol, packets, encrypted, key_exchange, cleartext, cleartext_management, and cleartext_credentials (the names of the headers only)."},
			{Name: "cleartext_credentials", Type: "int", Returned: "always", Description: "Headers that carried a credential in the clear, one for each header name on each connection; flows names them."},
			{Name: "cleartext_management", Type: "int", Returned: "always", Description: "Connections of a management protocol (http2, grpc) that crossed in the clear."},
			{Name: "warnings", Type: "list of string", Returned: "when a credential or management traffic crossed in the clear", Description: "One line for each connection that did, naming its headers but never a value, at most ten and then a count of the rest."},
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
	limit, err := countParam(params, paramPairs, defaultPairs)
	if err != nil {
		return fail(err)
	}
	flowLimit, err := countParam(params, paramFlows, defaultFlows)
	if err != nil {
		return fail(err)
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
		"unexamined":            s.Unread + s.Protocols[pcapsum.NonIP].Packets,
		"flows":                 flows(s.Flows, flowLimit),
		"cleartext_credentials": s.CleartextCredentials,
		"cleartext_management":  s.CleartextManagement,
	} {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	if err := sdk.RecordWarnings(rc, warnings(s.Flows)); err != nil {
		return collection.Result{}, err
	}
	if readErr != nil {
		return fail(fmt.Errorf("%s: %w (counted up to there)", src, readErr))
	}
	return collection.Result{}, nil
}

// countParam is the int parameter name, def when it is not set; a negative
// one is refused.
func countParam(params map[string]any, name string, def int) (int, error) {
	n, set, err := sdk.IntParam(params, name)
	if err != nil {
		return 0, err
	}
	if !set {
		return def, nil
	}
	if n < 0 {
		return 0, fmt.Errorf("%s is 0 or more", name)
	}
	return n, nil
}

// flows is the first limit of fs as a result holds them. A credential's
// header is named; its value was never kept.
func flows(fs []pcapsum.Flow, limit int) []any {
	fs = fs[:min(len(fs), limit)]
	out := make([]any, len(fs))
	for i, f := range fs {
		creds := make([]any, len(f.CleartextCredentials))
		for j, c := range f.CleartextCredentials {
			creds[j] = c
		}
		out[i] = map[string]any{
			"client": f.Client, "server": f.Server, "protocol": f.Protocol, "packets": f.Packets,
			"encrypted": f.Encrypted, "key_exchange": f.KeyExchange, "cleartext": f.Cleartext,
			"cleartext_management": f.CleartextManagement, "cleartext_credentials": creds,
		}
	}
	return out
}

// warnings are what a run always shows of fs: each flow that carried a
// credential in the clear, by its headers' names, and each other cleartext
// management flow; fs lists those first.
func warnings(fs []pcapsum.Flow) []string {
	var out []string
	more := 0
	for _, f := range fs {
		if len(f.CleartextCredentials) == 0 && !f.CleartextManagement {
			break
		}
		if len(out) == maxWarnings {
			more++
			continue
		}
		if len(f.CleartextCredentials) > 0 {
			out = append(out, fmt.Sprintf("a credential crossed in the clear: %s from %s to %s carried %s (only the header names are reported)",
				f.Protocol, f.Client, f.Server, strings.Join(f.CleartextCredentials, ", ")))
			continue
		}
		out = append(out, fmt.Sprintf("management traffic crossed in the clear: %s from %s to %s, %d packets", f.Protocol, f.Client, f.Server, f.Cleartext))
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("and %d more flows in the clear (listed in flows, credentials first)", more))
	}
	return out
}

// counters is a protocol table as a result holds it.
func counters(m map[string]pcapsum.Counter) map[string]any {
	out := make(map[string]any, len(m))
	for k, c := range m {
		out[k] = map[string]any{"packets": c.Packets, "bytes": c.Bytes}
	}
	return out
}
