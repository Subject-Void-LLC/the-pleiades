// Package pcapsum: which HTTP, HTTP/2 and gRPC flows are management. A
// flow in the clear is cleartext management only when its server is a
// management endpoint, since HTTP in the clear is also every lab's test
// web server (the CML lab's python http.server on 8080, flagged as
// management in the clear before, on 2026-11-17). A server is one when the
// flow's requests name a management API by its path, RESTCONF (RFC 8040's
// /restconf root, or its /.well-known/host-meta discovery), gNMI
// (/gnmi.gNMI/) or gNOI (/gnoi.), or when the caller lists it in
// Options.Management, by address or by address and port. A port alone
// never decides: a device's web interface on 80 and a web server on 80
// look the same on the wire.
package pcapsum

import (
	"net/netip"
	"strings"
)

// Options says what a read cannot learn from the capture.
type Options struct {
	// Management are the management endpoints: a device's address, which
	// makes every port on it one, or an address and port. A server listed
	// here is management whatever its flows' requests named.
	Management []netip.AddrPort
}

// The values of Flow.Management.
const (
	// ManagementRESTCONF is a flow whose requests named RESTCONF's root.
	ManagementRESTCONF = "restconf"
	// ManagementGNMI is a gRPC flow that called the gNMI service.
	ManagementGNMI = "gnmi"
	// ManagementGNOI is a gRPC flow that called a gNOI service.
	ManagementGNOI = "gnoi"
	// ManagementListed is a flow whose server Options.Management lists.
	ManagementListed = "listed"
)

// managementAPI is the management API a request's target names, or "".
// An absolute target (http://host/path), which a request to a proxy
// carries, is read by its path.
func managementAPI(target string) string {
	if i := strings.Index(target, "://"); i >= 0 {
		rest := target[i+3:]
		j := strings.IndexByte(rest, '/')
		if j < 0 {
			return ""
		}
		target = rest[j:]
	}
	switch {
	case target == "/restconf" || strings.HasPrefix(target, "/restconf/") || strings.HasPrefix(target, "/restconf?"),
		target == "/.well-known/host-meta" || strings.HasPrefix(target, "/.well-known/host-meta?"):
		return ManagementRESTCONF
	case strings.HasPrefix(target, "/gnmi.gNMI/"):
		return ManagementGNMI
	case strings.HasPrefix(target, "/gnoi."):
		return ManagementGNOI
	}
	return ""
}

// sawTarget implements headerSink: a request's target, kept when it names
// a management API and the flow has named none yet.
func (f *flow) sawTarget(target string) {
	if f.api == "" {
		f.api = managementAPI(target)
	}
}

// listed reports whether opts lists server, by its address alone (port 0)
// or by its address and port.
func (opts Options) listed(server netip.AddrPort) bool {
	for _, m := range opts.Management {
		if m.Addr().Unmap() == server.Addr().Unmap() && (m.Port() == 0 || m.Port() == server.Port()) {
			return true
		}
	}
	return false
}

// management is what makes server a management endpoint for a flow whose
// requests named api: the API first, then the list.
func (opts Options) management(server netip.AddrPort, api string) string {
	if api != "" {
		return api
	}
	if opts.listed(server) {
		return ManagementListed
	}
	return ""
}
