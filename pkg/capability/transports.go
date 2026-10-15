// The transports a Collection method may declare, and the capabilities
// that reach each one.
package capability

import "sort"

// The transport names a Collection method's manifest may list in
// SupportedTransports. Each is a way a task's work reaches its device.
const (
	// TransportSSH is a login over SSH: a terminal, a command, a file
	// transfer or a subsystem.
	TransportSSH = "ssh"

	// TransportWinRM is WS-Management over HTTP or HTTPS.
	TransportWinRM = "winrm"

	// TransportNetconf is an RFC 6241 NETCONF session.
	TransportNetconf = "netconf"

	// TransportHTTPS is a device's own HTTP API.
	TransportHTTPS = "https"

	// TransportEngineLocal is a container engine's API over the
	// executor's own Unix socket (EngineLocalCapable).
	TransportEngineLocal = "engine_local"

	// TransportEngineSocket is a container engine's API over an SSH
	// channel to the engine's socket on the device (EngineSocketCapable).
	TransportEngineSocket = "engine_socket"

	// TransportEngineStdio is a container engine's API over an SSH exec
	// channel running the engine's `system dial-stdio`
	// (EngineStdioCapable).
	TransportEngineStdio = "engine_stdio"

	// TransportEngineTLS is a container engine's API over TCP with mutual
	// TLS (EngineTLSCapable).
	TransportEngineTLS = "engine_tls"

	// TransportKubernetes is a Kubernetes cluster's API server
	// (KubernetesCapable).
	TransportKubernetes = "kubernetes"
)

// EngineTransports lists the container engine transports, in the order a
// method prefers them; a method that reaches an engine declares all four.
func EngineTransports() []string {
	return []string{TransportEngineLocal, TransportEngineSocket, TransportEngineStdio, TransportEngineTLS}
}

// reachedBy maps each transport to the capabilities that reach it. A
// device reaches a transport when it has any one of them.
//
// It is the one place that answers "can this device be reached over
// that?", so the plan-time rule, the run-time gate and the generic
// dispatchers all ask it rather than each keeping a list. A new transport
// is a constant above and one row here.
//
// https lists two capabilities rather than one parent because the API
// capabilities are siblings on purpose (HTTPAPICapable's own doc): a
// Catalyst Center answers an HTTP API, but one whose sign-in http.request
// cannot perform, so making CatalystAPICapable a child of HTTPAPICapable
// would let http.request pass validation against a device it cannot talk
// to. This row says only that both are HTTPS; each method's
// RequiredCapabilities still says which API it speaks.
var reachedBy = map[string][]Name{
	TransportSSH:          {NameSSHTransport},
	TransportWinRM:        {NameWinRM},
	TransportNetconf:      {NameNetconf},
	TransportHTTPS:        {NameHTTPAPI, NameCatalystAPI, NameAIProvider, NameCloudflareAPI},
	TransportEngineLocal:  {NameEngineLocal},
	TransportEngineSocket: {NameEngineSocket},
	TransportEngineStdio:  {NameEngineStdio},
	TransportEngineTLS:    {NameEngineTLS},
	TransportKubernetes:   {NameKubernetes},
}

// ReachedBy returns the capabilities that reach transport, any one of
// which is enough, and false for a transport this platform does not know.
// The slice is a copy.
func ReachedBy(transport string) ([]Name, bool) {
	names, ok := reachedBy[transport]
	if !ok {
		return nil, false
	}
	return append([]Name(nil), names...), true
}

// Transports returns every transport name this platform knows, sorted,
// for an error message or a generated reference page.
func Transports() []string {
	names := make([]string, 0, len(reachedBy))
	for name := range reachedBy {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
