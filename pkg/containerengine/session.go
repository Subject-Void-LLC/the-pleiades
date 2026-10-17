// Package containerengine is the one place this platform speaks to a
// container engine's API, Docker's or Podman's, on a device.
//
// A Collection method opens a Session for one task: Open picks the engine
// the device advertises (capability.ContainerEngineCapable and its
// children), takes the first carrier onboarding proved for it in a fixed
// order (capability.Carriers: the machine's own socket, an SSH channel to the
// engine's socket, the engine's dial-stdio over SSH, mutual TLS), checks
// that the engine answering is the one onboarding pinned, and hands back
// Docker's own Go client (github.com/moby/moby/client) on that connection.
// Podman is reached through the same client on its Docker-compatible API.
//
// Every request the session sends, ordinary or hijacked (exec, attach),
// passes through one guard that refuses an operation the caller did not
// declare before a byte reaches the engine (guard.go, ops.go). An engine's
// API is root on its host; the guard is what keeps a method that may only
// exec from being able to create a privileged container.
package containerengine

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ParamEngine is the task parameter that names the engine on a device that
// runs more than one. It is never inferred.
const ParamEngine = "engine"

// Session is one task's guarded connection to one engine on one device.
type Session struct {
	// Kind is the engine this session reaches.
	Kind capability.EngineKind
	// Carrier is how it reaches it.
	Carrier capability.Carrier
	// API is Docker's Go client on the guarded connection. A call outside
	// the operations the session was opened with fails without reaching the
	// engine.
	API *client.Client
	// Version is the engine's release, read with its identity.
	Version string
	// APIVersion is the Engine API version every request of this session is
	// sent at, fixed once when it opens: the engine's own, or the client's
	// newest for a newer engine, and never below apiFloor.
	APIVersion string

	dial    Dialer
	release []func()
}

// reachCapability is the capability a device must advertise for each
// carrier.
var reachCapability = map[capability.Carrier]capability.Name{
	capability.CarrierLocal:  capability.NameEngineLocal,
	capability.CarrierSocket: capability.NameEngineSocket,
	capability.CarrierStdio:  capability.NameEngineStdio,
	capability.CarrierTLS:    capability.NameEngineTLS,
}

// engineCapability is the capability a device must advertise for each
// engine.
var engineCapability = map[capability.EngineKind]capability.Name{
	capability.EngineDocker: capability.NameDocker,
	capability.EnginePodman: capability.NamePodman,
}

// Open connects to the engine device advertises, for the task named fqcn,
// allowing only ops. The caller must Close the session.
func Open(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, ops ...Op) (*Session, error) {
	return OpenKind(ctx, rc, device, params, fqcn, "", ops...)
}

// OpenKind is Open for an engine-only method, one only Docker or only
// Podman has: kind is the engine it must reach, and a device that
// does not run it is refused rather than reached through another.
func OpenKind(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, kind capability.EngineKind, ops ...Op) (*Session, error) {
	for _, op := range ops {
		if !KnownOp(op) {
			return nil, fmt.Errorf("%s: containerengine: unknown operation %q", fqcn, op)
		}
	}
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: a container method addresses a specific container host, set the task's target", fqcn)
	}
	kind, reach, err := engineFor(device, params, kind)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	if kind != "" {
		params = withEngine(params, kind)
	}
	pinned := reach.EngineID(kind)
	if pinned == "" {
		return nil, fmt.Errorf("%s: device %q has no %s identity on record; run `pleiades onboard %s`", fqcn, device.Name(), kind, device.Name())
	}

	s := &Session{Kind: kind}
	raw, carrier, err := s.connect(ctx, rc, device, params, fqcn, reach, kind)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.Carrier = carrier

	id, err := ReadIdentity(ctx, raw, kind)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("%s: reading the %s engine's identity on %q: %w", fqcn, kind, device.Name(), err)
	}
	if id.ID != pinned {
		s.Close()
		return nil, fmt.Errorf("%s: the %s engine answering for device %q is %s, and onboarding proved %s; run `pleiades onboard %s` if the engine was replaced on purpose",
			fqcn, kind, device.Name(), id.ID, pinned, device.Name())
	}
	s.Version = id.Version

	s.dial = guard(raw, ops)
	api, err := newAPI(s.dial)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	s.API = api
	if s.APIVersion, err = negotiate(ctx, api, kind, id.Version); err != nil {
		s.Close()
		return nil, fmt.Errorf("%s: the %s engine on %q: %w", fqcn, kind, device.Name(), err)
	}
	return s, nil
}

// ChooseEngine returns the engine a task on device uses: the one named by
// the engine parameter, which must be one the device advertises, or the only
// one it runs. A device running both with no parameter is refused, never
// guessed.
func ChooseEngine(device inventory.InventoryItem, params map[string]any) (capability.EngineKind, error) {
	host, ok := device.(capability.ContainerEngineCapable)
	if !ok || !device.HasCapability(capability.NameContainerEngine) {
		return "", fmt.Errorf("device %q advertises no container engine; run `pleiades onboard %s`, which finds one", device.Name(), device.Name())
	}
	var runs []capability.EngineKind
	for _, kind := range host.ContainerEngines() {
		if device.HasCapability(engineCapability[kind]) {
			runs = append(runs, kind)
		}
	}
	if named := sdk.StringParam(params, ParamEngine); named != "" {
		kind := capability.EngineKind(named)
		if !slices.Contains(runs, kind) {
			return "", fmt.Errorf("device %q does not run %q; it runs %s", device.Name(), named, kinds(runs))
		}
		return kind, nil
	}
	switch len(runs) {
	case 0:
		return "", fmt.Errorf("device %q advertises no container engine; run `pleiades onboard %s`, which finds one", device.Name(), device.Name())
	case 1:
		return runs[0], nil
	default:
		return "", fmt.Errorf("device %q runs %s; set %s: to choose one", device.Name(), kinds(runs), ParamEngine)
	}
}

// withEngine returns params with the engine parameter set to kind, leaving
// the caller's map as it was.
func withEngine(params map[string]any, kind capability.EngineKind) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out[ParamEngine] = string(kind)
	return out
}

// kinds names engines for a message.
func kinds(list []capability.EngineKind) string {
	if len(list) == 0 {
		return "none"
	}
	names := make([]string, len(list))
	for i, k := range list {
		names[i] = string(k)
	}
	return strings.Join(names, " and ")
}

// engineFor resolves the engine a task on device reaches: kind for an
// engine-only method, refused when the task names another, or the one the
// engine parameter or the device chooses (ChooseEngine).
func engineFor(device inventory.InventoryItem, params map[string]any, kind capability.EngineKind) (capability.EngineKind, capability.EngineReach, error) {
	if kind != "" {
		named := sdk.StringParam(params, ParamEngine)
		if named != "" && capability.EngineKind(named) != kind {
			return "", nil, fmt.Errorf("this method reaches %s, and the task names %s", kind, named)
		}
		params = withEngine(params, kind)
	}
	chosen, err := ChooseEngine(device, params)
	if err != nil {
		return "", nil, err
	}
	reach, ok := device.(capability.EngineReach)
	if !ok {
		return "", nil, fmt.Errorf("device %q does not say how its engine is reached", device.Name())
	}
	return chosen, reach, nil
}

// usableCarriers lists, in capability.Carriers' order, the carriers to
// kind's engine on device that onboarding proved, the device advertises,
// and the record holds what they need (a socket path, a TLS address). A
// session takes the first; Route names it.
func usableCarriers(device inventory.InventoryItem, reach capability.EngineReach, kind capability.EngineKind) []capability.Carrier {
	proven := reach.EngineCarriers(kind)
	socket := reach.EngineSocket(kind)
	var out []capability.Carrier
	for _, carrier := range capability.Carriers() {
		if !slices.Contains(proven, carrier) || !device.HasCapability(reachCapability[carrier]) {
			continue
		}
		switch carrier {
		case capability.CarrierLocal, capability.CarrierSocket:
			if socket == "" {
				continue
			}
		case capability.CarrierTLS:
			tlsDevice, ok := device.(capability.EngineTLSCapable)
			if !ok || tlsDevice.EngineTLSAddress() == "" {
				continue
			}
		}
		out = append(out, carrier)
	}
	return out
}

// Route returns the engine and carrier a session for a task on device would
// use, decided from the record alone, through the same functions Open uses,
// so a plan names what a run does. kind is the engine an engine-only method
// reaches, or empty.
func Route(device inventory.InventoryItem, params map[string]any, kind capability.EngineKind) (capability.EngineKind, capability.Carrier, error) {
	chosen, reach, err := engineFor(device, params, kind)
	if err != nil {
		return "", "", err
	}
	carriers := usableCarriers(device, reach, chosen)
	if len(carriers) == 0 {
		return "", "", noCarrier(device, chosen)
	}
	return chosen, carriers[0], nil
}

// noCarrier is the refusal for a device with no usable way to its engine.
func noCarrier(device inventory.InventoryItem, kind capability.EngineKind) error {
	return fmt.Errorf("device %q advertises no way to reach its %s engine; run `pleiades onboard %s`, which finds one", device.Name(), kind, device.Name())
}

// connect opens the first usable carrier (usableCarriers).
func (s *Session) connect(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, reach capability.EngineReach, kind capability.EngineKind) (Dialer, capability.Carrier, error) {
	carriers := usableCarriers(device, reach, kind)
	if len(carriers) == 0 {
		return nil, "", fmt.Errorf("%s: %w", fqcn, noCarrier(device, kind))
	}
	socket := reach.EngineSocket(kind)
	switch carrier := carriers[0]; carrier {
	case capability.CarrierLocal:
		if !localCarrier.Load() {
			return nil, "", refuseLocalCarrier(fqcn, device.Name())
		}
		who, err := LocalUser()
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := sameAccount(fqcn, device.Name(), kind, reach.EngineLoginUser(), who); err != nil {
			return nil, "", err
		}
		return Local(socket), carrier, nil
	case capability.CarrierSocket:
		conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
		if err != nil {
			return nil, "", err
		}
		s.release = append(s.release, func() { _ = conn.Close() })
		if err := s.checkLogin(ctx, conn, fqcn, device.Name(), kind, reach); err != nil {
			return nil, "", err
		}
		return SSHSocket(conn, socket), carrier, nil
	case capability.CarrierStdio:
		conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
		if err != nil {
			return nil, "", err
		}
		s.release = append(s.release, func() { _ = conn.Close() })
		if err := s.checkLogin(ctx, conn, fqcn, device.Name(), kind, reach); err != nil {
			return nil, "", err
		}
		dial, stop := SSHStdio(conn, kind, socket)
		// The carrier's service stops before the login it runs on closes:
		// release runs newest first.
		s.release = append(s.release, stop)
		return dial, carrier, nil
	default: // capability.CarrierTLS; usableCarriers proved the address.
		tlsDevice := device.(capability.EngineTLSCapable)
		cfg, err := devicetls.For(device).Config(rc.InjectSecrets())
		if err != nil {
			return nil, "", fmt.Errorf("%s: the TLS settings of device %q: %w", fqcn, device.Name(), err)
		}
		return TLS(tlsDevice.EngineTLSAddress(), cfg), carrier, nil
	}
}

// checkLogin refuses an SSH carrier whose login is not the account onboarding
// probed the engine as (sameAccount), asking the device the same question
// the probe asked.
func (s *Session) checkLogin(ctx context.Context, conn *remoteexec.Conn, fqcn, device string, kind capability.EngineKind, reach capability.EngineReach) error {
	who, err := remoteUser(ctx, conn)
	if err != nil {
		return fmt.Errorf("%s: device %q: %w", fqcn, device, err)
	}
	return sameAccount(fqcn, device, kind, reach.EngineLoginUser(), who)
}

// Close releases what the session opened, newest first.
func (s *Session) Close() {
	if s.API != nil {
		_ = s.API.Close()
	}
	for i := len(s.release) - 1; i >= 0; i-- {
		s.release[i]()
	}
	s.release = nil
}

// Dial returns the session's guarded dialer, for a caller that speaks to an
// endpoint Docker's client has no method for (Podman's own API): the guard
// still decides what it may send.
func (s *Session) Dial() Dialer { return s.dial }

// newAPI builds Docker's client on dial. The host is a placeholder Unix
// socket so the client configures no proxy; every connection, ordinary or
// hijacked, then comes from dial, and keep-alive is off so each carries one
// request the guard decided.
func newAPI(dial Dialer) (*client.Client, error) {
	transport := &http.Transport{
		DisableKeepAlives: true,
		// HTTP/1 only, and the reason is the guard, not the protocol. The
		// client clones the transport it is given, and cloning one left to
		// its defaults configures HTTP/2, which gives the clone a TLS
		// configuration. The client then dials a hijacked request (exec,
		// attach) itself, to the placeholder host below, instead of through
		// dial, so the guard never saw it. Found 2026-10-14 when exec moved
		// onto this client; TestSession_HijackedRequestsPassTheGuard holds it.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	api, err := client.New(
		client.WithHTTPClient(&http.Client{Transport: transport}),
		client.WithHost("unix:///pleiades-engine.sock"),
		client.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }),
		// Stated, not inferred: the client infers https from a transport
		// with any TLS configuration, and cloning an http.Transport gives it
		// one for HTTP/2. TLS, when a device asks for it, is the TLS
		// carrier's, below this client.
		client.WithScheme("http"),
	)
	if err != nil {
		return nil, fmt.Errorf("containerengine: building the engine client: %w", err)
	}
	return api, nil
}
