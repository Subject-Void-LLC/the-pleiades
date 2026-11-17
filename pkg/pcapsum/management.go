// Package pcapsum: which HTTP, HTTP/2 and gRPC flows are management. A
// flow in the clear is cleartext management only when its server is a
// management endpoint, since HTTP in the clear is also every lab's test
// web server (the CML lab's python http.server on 8080, flagged as
// management in the clear before, on 2026-11-17). A server is one when the
// flow's requests name a management API by its path, RESTCONF (RFC 8040's
// /restconf root, or its /.well-known/host-meta discovery), gNMI
// (/gnmi.gNMI/), gNOI (/gnoi.), Cisco NX-API (/ins, and its REST login
// /api/aaaLogin.json), Arista eAPI (/command-api), IOS's HTTP exec
// (/level/15/exec, any privilege level) or IOS XE's web interface
// (/webui), or when the caller lists it in
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
	// ManagementNXAPI is a flow to Cisco NX-API: its /ins endpoint, or the
	// REST API's /api/aaaLogin.json login.
	ManagementNXAPI = "nx-api"
	// ManagementEAPI is a flow to Arista eAPI's /command-api.
	ManagementEAPI = "eapi"
	// ManagementIOSExec is a flow to IOS's HTTP exec, /level/15/exec.
	ManagementIOSExec = "ios-exec"
	// ManagementWebUI is a flow to IOS XE's web interface, /webui.
	ManagementWebUI = "webui"
	// ManagementListed is a flow whose server Options.Management lists.
	ManagementListed = "listed"
)

// pathIs reports whether target is path itself, or path followed by a
// query or (when sub is set) a further segment.
func pathIs(target, path string, sub bool) bool {
	return target == path || strings.HasPrefix(target, path+"?") || sub && strings.HasPrefix(target, path+"/")
}

// iosExec is IOS's HTTP exec path: /level/<privilege>/exec, then the
// command's words as segments.
func iosExec(target string) bool {
	rest, ok := strings.CutPrefix(target, "/level/")
	if !ok {
		return false
	}
	level, rest, _ := strings.Cut(rest, "/")
	if level == "" || strings.Trim(level, "0123456789") != "" {
		return false
	}
	return pathIs("/"+rest, "/exec", true)
}

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
	case pathIs(target, "/restconf", true), pathIs(target, "/.well-known/host-meta", false):
		return ManagementRESTCONF
	case strings.HasPrefix(target, "/gnmi.gNMI/"):
		return ManagementGNMI
	case strings.HasPrefix(target, "/gnoi."):
		return ManagementGNOI
	case pathIs(target, "/ins", false), pathIs(target, "/api/aaaLogin.json", false):
		return ManagementNXAPI
	case pathIs(target, "/command-api", false):
		return ManagementEAPI
	case iosExec(target):
		return ManagementIOSExec
	case pathIs(target, "/webui", true):
		return ManagementWebUI
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
