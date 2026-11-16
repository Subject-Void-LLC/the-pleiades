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
		Description: "Reads a pcap or pcapng file from the project (virt.cml.capture.stop writes one; up to 16 MiB) and counts every frame by protocol: IPsec's ESP, ESP inside UDP 4500 (NAT traversal) and IKE (UDP 500, or 4500 behind the non-ESP marker), and in the clear GRE, ICMP, OSPF, EIGRP, BGP, HSRP, VRRP, PIM, TCP, UDP, ARP and frames carrying no IP (CDP, keepalives). Each TCP connection is also read from its own bytes, both ways in sequence order, and never judged by its port: ssh (an SSH-2.0 banner, its key exchange, then everything after NEWKEYS), tls (a ClientHello or ServerHello record, then each record by its content type), http (an HTTP/1.x request or status line in the clear), http2 (HTTP/2's client preface in the clear) and grpc (http2 whose request says application/grpc). Each IP packet is one of three kinds: encrypted (ESP, ESP in UDP, SSH after NEWKEYS, TLS application data), the key exchange that set it up (IKE, SSH's banner and key exchange, TLS handshake records), or cleartext (everything else carried over IP, which anyone on the wire can read). A packet with no data in an encrypted connection (a SYN, a bare ACK) is counted encrypted. The same counts are given for each address pair, most packets first, so \"the sites' traffic crosses encrypted\" is a pair whose cleartext is 0 and whose encrypted count grew while traffic was sent. Whether it crossed inside IPsec is the pair's protocols.esp (and esp-udp), not encrypted, which counts SSH and TLS too; GRE in the clear between two sites is DMVPN without its IPsec. " +
			"HTTP/1.x and HTTP/2 in the clear are cleartext management: listed first in flows, and a run warns about them. Their headers are searched by name (HTTP/2's decoded from HPACK, which compresses header values, so a search of the bytes finds nothing), and a header that carries a credential (authorization, username, password, a token, a cookie, or one its sender marked never to be indexed) is reported by its name and its connection, never its value, in cleartext_credentials, flows and a warning. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so cleartext_credentials 0 means no credential header was found, not that no credential crossed, and Telnet, FTP and SNMP are not counted in cleartext_management. A header that could not be searched (a block over 64 KiB or one that would not decode, a name over 256 bytes, headers after a gap or a protocol switch, a credential name past the 32 kept for a connection) is counted in headers_unread. Bytes framed as encrypted that read as text are not ciphertext: a TLS application data record, or a 256-byte block of what SSH sent after NEWKEYS, of at least 64 bytes of which three quarters are printable ASCII (random bytes are about 38% printable) makes that direction's packets cleartext and flags the connection cleartext_in_encrypted, which is how a TLS NULL cipher suite, the SSH none cipher and records forged around cleartext are told. Nothing is called encrypted that was not seen to be: a connection whose start the capture lacks, one whose bytes stop making sense as SSH or TLS, the bytes after a lost segment that hid a record's or packet's header (TLS is read again from the next segment that begins with a record), a packet whose data the capture holds none of (a short snapshot length), and a repeated segment whose bytes differ from the first copy are cleartext. UDP is read no further than its ports, so DTLS and QUIC are cleartext UDP. " +
			"It reads Ethernet (VLAN tags included), raw IP and Linux cooked captures; a frame of another link type is counted as unread. A frame carrying no IP (CDP, a keepalive, but also anything inside MPLS or PPPoE) and an unread frame are not examined, so they are counted apart as unexamined, never as cleartext: \"nothing crossed in the clear\" is cleartext 0 and unexamined 0 (or every unexamined frame accounted for by its protocol). IKE and ESP in UDP are told by their ports, and ESP is counted encrypted whatever its cipher, ESP-NULL included, since its payload is not read. A file cut short is read up to the cut, and the task fails naming where. It changes nothing, and a check runs it as a run does. It runs on this machine with no device: write it without hosts.",
		Params: []collection.Param{
			{Name: paramSrc, Type: "string", Required: true, Description: "The capture file, a path relative to the project directory, such as captures/core1-wan.pcap.", Format: collection.ParamFormatProjectPath},
			{Name: paramPairs, Type: "int", Default: "20", Description: "How many address pairs to list, most packets first; 0 lists none."},
			{Name: paramFlows, Type: "int", Default: "20", Description: "How many TCP connections named from their bytes to list in flows, those in the clear first; 0 lists none."},
		},
		Returns: []collection.ReturnField{
			{Name: "format", Type: "string", Returned: "always", Description: "pcap or pcapng."},
			{Name: "packets", Type: "int", Returned: "always", Description: "Every frame in the capture."},
			{Name: "bytes", Type: "int", Returned: "always", Description: "Their lengths on the wire, summed."},
			{Name: "protocols", Type: "dict", Returned: "always", Description: "Packets and bytes by protocol name (esp, esp-udp, ike, gre, icmp, ospf, bgp, ssh, tls, http, http2, grpc, tcp, udp, arp, non-ip and the rest). Whether traffic crossed inside IPsec is esp and esp-udp here."},
			{Name: "encrypted", Type: "int", Returned: "always", Description: "IP packets that were ESP, ESP in UDP, SSH after NEWKEYS or TLS application data, and those carrying no data in an encrypted connection. It is not the IPsec count: that is protocols.esp."},
			{Name: "key_exchange", Type: "int", Returned: "always", Description: "IP packets that were IKE, SSH's banner and key exchange, or TLS handshake records."},
			{Name: "cleartext", Type: "int", Returned: "always", Description: "Every other IP packet: what a reader of the wire saw in the clear."},
			{Name: "pairs", Type: "list of dict", Returned: "always", Description: "Each address pair (a, b), most packets first: its protocols, encrypted, key_exchange and cleartext."},
			{Name: "flows", Type: "list of dict", Returned: "always", Description: "Each TCP connection named from its bytes (ssh, tls, http, http2, grpc), those that carried a credential in the clear first, then those whose encrypted-looking bytes read as text, then other cleartext management, then most packets first: client and server (address:port; the client sent the first SYN), protocol, packets, encrypted, key_exchange, cleartext, cleartext_management, cleartext_in_encrypted, cleartext_credentials (the names of the headers only, at most 32) and headers_unread."},
			{Name: "cleartext_credentials", Type: "int", Returned: "always", Description: "HTTP/1.x and HTTP/2 headers that carried a credential in the clear, one for each header name on each connection; flows names them. Credentials are searched in HTTP/1.x and HTTP/2 headers only: Telnet, FTP, SNMP communities, URL query strings and bodies are not searched, so 0 is not proof that none crossed."},
			{Name: "cleartext_management", Type: "int", Returned: "always", Description: "Connections of a management protocol named from its bytes (http, http2, grpc) that crossed in the clear. Telnet, FTP and SNMP are not named, and are not counted here."},
			{Name: "cleartext_in_encrypted", Type: "int", Returned: "always", Description: "Connections whose bytes framed as encrypted (TLS application data, SSH after NEWKEYS) read as text: a NULL cipher suite, the SSH none cipher, or records forged around cleartext. Their packets are counted cleartext."},
			{Name: "headers_unread", Type: "int", Returned: "always", Description: "Headers or header blocks that could not be searched for a credential: a block over 64 KiB or one that would not decode (and every block after it), a name over 256 bytes, headers after a gap or a protocol switch, and a credential name past the 32 kept for a connection. When it is not 0, cleartext_credentials may be short."},
			{Name: "warnings", Type: "list of string", Returned: "when a credential, text framed as encrypted, or management traffic crossed in the clear", Description: "One line for each connection that did, naming its headers but never a value and saying when some of its headers could not be searched, at most ten and then a count of the rest."},
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
		"unexamined":             s.Unread + s.Protocols[pcapsum.NonIP].Packets,
		"flows":                  flows(s.Flows, flowLimit),
		"cleartext_credentials":  s.CleartextCredentials,
		"cleartext_management":   s.CleartextManagement,
		"cleartext_in_encrypted": s.CleartextInEncrypted,
		"headers_unread":         s.HeadersUnread,
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
			"cleartext_in_encrypted": f.CleartextInEncrypted, "headers_unread": f.HeadersUnread,
		}
	}
	return out
}

// warnings are what a run always shows of fs: each flow that carried a
// credential in the clear, by its headers' names, each whose
// encrypted-looking bytes read as text, and each other cleartext management
// flow, saying when some of its headers could not be searched; fs lists
// those first.
func warnings(fs []pcapsum.Flow) []string {
	var out []string
	more := 0
	for _, f := range fs {
		if len(f.CleartextCredentials) == 0 && !f.CleartextManagement && !f.CleartextInEncrypted {
			break
		}
		if len(out) == maxWarnings {
			more++
			continue
		}
		var line string
		switch {
		case len(f.CleartextCredentials) > 0:
			line = fmt.Sprintf("a credential crossed in the clear: %s from %s to %s carried %s (only the header names are reported)",
				f.Protocol, f.Client, f.Server, strings.Join(f.CleartextCredentials, ", "))
		case f.CleartextInEncrypted:
			line = fmt.Sprintf("bytes framed as encrypted read as text: %s from %s to %s (a NULL cipher suite, the SSH none cipher, or records forged around cleartext); they are counted cleartext",
				f.Protocol, f.Client, f.Server)
		default:
			line = fmt.Sprintf("management traffic crossed in the clear: %s from %s to %s, %d packets", f.Protocol, f.Client, f.Server, f.Cleartext)
		}
		if f.HeadersUnread > 0 {
			line += fmt.Sprintf("; %d of its headers or header blocks could not be searched, so a credential may be missing", f.HeadersUnread)
		}
		out = append(out, line)
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
