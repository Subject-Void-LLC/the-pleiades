// The container engine probe every type that can run one shares: which
// engines answer, where, as whom, through which carriers, and with what
// identity.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// engineLoginScript is the one command the remote engine probe runs: which
// user the login is, and which engine CLIs it can run. It is a constant: no
// inventory value reaches it. Only lines after its marker count, so a banner
// a login script prints first is ignored.
const engineLoginScript = `printf 'pleiades-probe=1\n'
printf 'user=%s\n' "$(id -un)"
printf 'uid=%s\n' "$(id -u)"
for c in docker podman; do
  if command -v "$c" >/dev/null 2>&1; then printf 'has=%s\n' "$c"; fi
done
exit 0`

// engineLogin is what engineLoginScript reported.
type engineLogin struct {
	user string
	uid  string
	has  []string
}

// parseEngineLogin reads engineLoginScript's output. A uid that is not a
// number is dropped rather than put in a path.
func parseEngineLogin(out string) engineLogin {
	var l engineLogin
	seen := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "pleiades-probe=1" {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		switch k, v, _ := strings.Cut(line, "="); k {
		case "user":
			l.user = v
		case "uid":
			if _, err := strconv.ParseUint(v, 10, 32); err == nil {
				l.uid = v
			}
		case "has":
			if v == "docker" || v == "podman" {
				l.has = append(l.has, v)
			}
		}
	}
	return l
}

// engineFound is one engine a probe reached.
type engineFound struct {
	kind     capability.EngineKind
	socket   string
	id       containerengine.Identity
	carriers []capability.Carrier
	rootless bool
}

// attempt is one carrier tried against one socket.
type attempt struct {
	carrier capability.Carrier
	dial    containerengine.Dialer
	release func()
}

// probeRemoteEngines finds the engines reachable through conn's login.
// tried says what each failed attempt answered, for a probe that found
// nothing.
func probeRemoteEngines(ctx context.Context, conn *remoteexec.Conn) (found []engineFound, login engineLogin, tried []string, err error) {
	res, err := conn.Run(ctx, engineLoginScript)
	if err != nil {
		return nil, engineLogin{}, nil, err
	}
	login = parseEngineLogin(res.Stdout)

	for _, kind := range capability.Engines() {
		hasCLI := slices.Contains(login.has, string(kind))
		var candidates []string
		switch kind {
		case capability.EngineDocker:
			candidates = append(candidates, rootfulDockerSocket)
			if login.uid != "" {
				candidates = append(candidates, path.Join("/run/user", login.uid, "docker.sock"))
			}
		case capability.EnginePodman:
			candidates = append(candidates, "/run/podman/podman.sock")
			if login.uid != "" {
				candidates = append(candidates, path.Join("/run/user", login.uid, "podman", "podman.sock"))
			}
		}
		engine, notes := probeSockets(ctx, kind, candidates, func(socket string) []attempt {
			out := []attempt{{carrier: capability.CarrierSocket, dial: containerengine.SSHSocket(conn, capability.SocketAddress(socket)), release: func() {}}}
			if hasCLI {
				dial, stop := containerengine.SSHStdio(conn, kind, capability.SocketAddress(socket))
				out = append(out, attempt{carrier: capability.CarrierStdio, dial: dial, release: stop})
			}
			return out
		})
		tried = append(tried, notes...)
		// Podman with no API socket listening is still reached: a session
		// starts a service of its own.
		if engine == nil && kind == capability.EnginePodman && hasCLI {
			dial, stop := containerengine.SSHStdio(conn, kind, "")
			id, err := containerengine.ReadIdentity(ctx, dial, kind)
			stop()
			if err == nil {
				engine = &engineFound{kind: kind, id: id, carriers: []capability.Carrier{capability.CarrierStdio}, rootless: login.uid != "0"}
			} else {
				tried = append(tried, fmt.Sprintf("podman through a service started for the session: %v", err))
			}
		}
		if engine != nil {
			found = append(found, *engine)
		}
	}
	return found, login, tried, nil
}

// probeSockets tries every attempt against each candidate socket in turn and
// returns the first socket where any carrier reached the engine, with every
// carrier that did.
func probeSockets(ctx context.Context, kind capability.EngineKind, candidates []string, attempts func(socket string) []attempt) (*engineFound, []string) {
	var notes []string
	for _, socket := range candidates {
		var found *engineFound
		for _, a := range attempts(socket) {
			id, err := containerengine.ReadIdentity(ctx, a.dial, kind)
			a.release()
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s at %s over %s: %v", kind, socket, a.carrier, err))
				continue
			}
			if found == nil {
				found = &engineFound{kind: kind, socket: socket, id: id, rootless: strings.HasPrefix(socket, "/run/user/")}
			}
			if id.ID != found.id.ID {
				// Two carriers to one socket answering as two engines is
				// not a state to record; name it and keep the first.
				notes = append(notes, fmt.Sprintf("%s at %s answered as %s over %s and %s over %s", kind, socket, found.id.ID, found.carriers, id.ID, a.carrier))
				continue
			}
			found.carriers = append(found.carriers, a.carrier)
		}
		if found != nil {
			return found, notes
		}
	}
	return nil, notes
}

// localEngineAllowed says whether this process may probe a container host
// with no address through its own sockets, and grant it EngineLocalCapable:
// exactly when it may reach an engine that way at all
// (containerengine.AllowLocalCarrier, which only the Crawl tier's
// composition root calls). One switch, not two: onboarding's own used to sit
// beside the engine port's, and a process that set one and not the other
// (a test standing in for the CLI) onboarded what it could then not reach.
// A variable so a test can say no.
var localEngineAllowed = containerengine.LocalCarrierAllowed

// localSystemSockets is where each engine's system (rootful) service listens
// on the machine running pleiades, which probeLocalEngines tries before this
// user's rootless ones. A variable so a test points it away from the
// machine's own engines: a unit test must never read the developer's real
// daemon.
var localSystemSockets = map[capability.EngineKind]string{
	capability.EngineDocker: rootfulDockerSocket,
	capability.EnginePodman: "/run/podman/podman.sock",
}

// probeLocalEngines finds the engines this machine runs, through their
// sockets: the system's, then this user's rootless ones.
func probeLocalEngines(ctx context.Context) ([]engineFound, []string, error) {
	if !localEngineAllowed() {
		return nil, nil, errors.New("a container host with no host is the machine that runs pleiades itself; onboard it there with `pleiades onboard`, since this process cannot see that machine's engine")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	var found []engineFound
	var tried []string
	for _, kind := range capability.Engines() {
		var candidates []string
		candidates = append(candidates, localSystemSockets[kind])
		switch kind {
		case capability.EngineDocker:
			if strings.HasPrefix(runtimeDir, "/") {
				candidates = append(candidates, path.Join(runtimeDir, "docker.sock"))
			}
		case capability.EnginePodman:
			if strings.HasPrefix(runtimeDir, "/") {
				candidates = append(candidates, path.Join(runtimeDir, "podman", "podman.sock"))
			}
		}
		engine, notes := probeSockets(ctx, kind, candidates, func(socket string) []attempt {
			return []attempt{{carrier: capability.CarrierLocal, dial: containerengine.Local(capability.SocketAddress(socket)), release: func() {}}}
		})
		tried = append(tried, notes...)
		if engine != nil {
			engine.rootless = runtimeDir != "" && strings.HasPrefix(engine.socket, runtimeDir)
			found = append(found, *engine)
		}
	}
	return found, tried, nil
}

// probeTLSEngine reaches the Docker engine at addr over mutual TLS.
func probeTLSEngine(ctx context.Context, addr string, dial containerengine.Dialer) (engineFound, error) {
	id, err := containerengine.ReadIdentity(ctx, dial, capability.EngineDocker)
	if err != nil {
		return engineFound{}, fmt.Errorf("docker at %s over TLS: %w", addr, err)
	}
	return engineFound{kind: capability.EngineDocker, id: id, carriers: []capability.Carrier{capability.CarrierTLS}}, nil
}

// engineProbed turns what a probe found into capabilities and facts.
func engineProbed(found []engineFound, loginUser string) Probed {
	p := Probed{Facts: map[string]any{}}
	if len(found) == 0 {
		return p
	}
	reach := map[capability.Carrier]capability.Name{
		capability.CarrierLocal:  capability.NameEngineLocal,
		capability.CarrierSocket: capability.NameEngineSocket,
		capability.CarrierStdio:  capability.NameEngineStdio,
		capability.CarrierTLS:    capability.NameEngineTLS,
	}
	caps := []capability.Name{capability.NameContainerEngine}
	for _, e := range found {
		switch e.kind {
		case capability.EngineDocker:
			caps = append(caps, capability.NameDocker)
		case capability.EnginePodman:
			caps = append(caps, capability.NamePodman)
		}
		carriers := make([]string, 0, len(e.carriers))
		for _, c := range e.carriers {
			carriers = append(carriers, string(c))
			if name := reach[c]; !slices.Contains(caps, name) {
				caps = append(caps, name)
			}
		}
		p.Facts[engines.FactKey(e.kind, engines.FieldSocket)] = factText(e.socket)
		p.Facts[engines.FactKey(e.kind, engines.FieldID)] = factText(string(e.id.ID))
		p.Facts[engines.FactKey(e.kind, engines.FieldVersion)] = factText(e.id.Version)
		p.Facts[engines.FactKey(e.kind, engines.FieldCarriers)] = factList(carriers)
		p.Facts[engines.FactKey(e.kind, engines.FieldRootless)] = strconv.FormatBool(e.rootless)
	}
	if loginUser != "" {
		p.Facts[engines.FactLoginUser] = factText(loginUser)
	}
	p.Capabilities = caps
	return p
}

// merge adds b's capabilities and facts to a.
func merge(a, b Probed) Probed {
	if a.Facts == nil {
		a.Facts = map[string]any{}
	}
	for _, c := range b.Capabilities {
		if !slices.Contains(a.Capabilities, c) {
			a.Capabilities = append(a.Capabilities, c)
		}
	}
	for k, v := range b.Facts {
		a.Facts[k] = v
	}
	a.Warnings = append(a.Warnings, b.Warnings...)
	return a
}
