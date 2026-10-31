// The remaining virt.clab methods: the server itself, packet-capture links,
// generated topologies, the server's temporary SSH sessions and Edgeshark.
package clab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// serverRead is a server route that only reads, its answer decoded by pick
// into results.
func serverRead(path string, anonymous bool, pick func(raw map[string]any) map[string]any) handler {
	return func(ctx context.Context, s *session, _ *args, _ bool) (bool, error) {
		var raw map[string]any
		if err := s.call(ctx, http.MethodGet, path, nil, nil, &raw); err != nil {
			return false, err
		}
		return false, s.stats(pick(raw))
	}
}

// text returns raw[key] as text, "" when it is not text.
func text(raw map[string]any, key string) string {
	s, _ := raw[key].(string)
	return s
}

// capturePacketflix builds capture links for interfaces.
func capturePacketflix(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, remote := a.ident("lab"), a.str("remote_hostname")
	targets := a.dicts("targets")
	if a.err != nil {
		return false, a.err
	}
	if len(targets) == 0 {
		return false, fmt.Errorf("%s: targets names nothing to capture", s.fqcn)
	}
	wire := make([]map[string]any, 0, len(targets))
	for i, t := range targets {
		c, _ := t["container_name"].(string)
		ifc, _ := t["interface_name"].(string)
		if !nameRE.MatchString(c) || (ifc != "" && !nameRE.MatchString(ifc)) {
			return false, fmt.Errorf("%s: targets[%d] needs a container_name and an interface_name that are names", s.fqcn, i)
		}
		w := map[string]any{"containerName": c, "interfaceName": ifc}
		if ns, ok := t["node_namespace"].(bool); ok {
			w["nodeNamespace"] = ns
		}
		wire = append(wire, w)
	}
	body := map[string]any{"targets": wire}
	if remote != "" {
		body["remoteHostname"] = remote
	}
	var out struct {
		Captures []struct {
			ContainerName  string   `json:"containerName"`
			InterfaceNames []string `json:"interfaceNames"`
			URI            string   `json:"packetflixUri"`
		} `json:"captures"`
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "capture", "packetflix"), nil, body, &out); err != nil {
		return false, err
	}
	caps := make([]any, 0, len(out.Captures))
	for _, c := range out.Captures {
		caps = append(caps, map[string]any{"container_name": c.ContainerName, "interface_names": c.InterfaceNames, "packetflix_uri": c.URI})
	}
	return false, s.stat("captures", caps)
}

// topologyGenerate asks the server for a topology from tiers. It never
// deploys.
func topologyGenerate(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	name := a.ident("name")
	tiers := a.dicts("tiers")
	body := map[string]any{"name": name, "deploy": false}
	for param, key := range map[string]string{"default_kind": "defaultKind", "management_network": "managementNetwork", "ipv4_subnet": "ipv4Subnet", "node_prefix": "nodePrefix", "group_prefix": "groupPrefix"} {
		if v := a.str(param); v != "" {
			body[key] = v
		}
	}
	if images := a.dict("images"); images != nil {
		body["images"] = images
	}
	if a.err != nil {
		return false, a.err
	}
	if len(tiers) == 0 {
		return false, fmt.Errorf("%s: tiers names no tier", s.fqcn)
	}
	body["tiers"] = tiers
	var out struct {
		Message string `json:"message"`
		YAML    string `json:"topologyYaml"`
	}
	if err := s.call(ctx, http.MethodPost, "/api/v1/generate", nil, body, &out); err != nil {
		return false, err
	}
	return false, s.stats(map[string]any{"topology": out.YAML, "message": out.Message})
}

// sshSession is one temporary SSH session on the server.
type sshSession struct {
	Port       int    `json:"port"`
	LabName    string `json:"labName"`
	NodeName   string `json:"nodeName"`
	Username   string `json:"username"`
	Expiration string `json:"expiration"`
	Created    string `json:"created"`
}

// sshSessions lists the sessions.
func (s *session) sshSessions(ctx context.Context, all bool) ([]sshSession, error) {
	q := url.Values{}
	if all {
		q.Set("all", "true")
	}
	var list []sshSession
	err := s.call(ctx, http.MethodGet, "/api/v1/ssh/sessions", q, nil, &list)
	return list, err
}

// sshSessionList lists the temporary SSH sessions.
func sshSessionList(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	all := a.flag("all")
	if a.err != nil {
		return false, a.err
	}
	list, err := s.sshSessions(ctx, all)
	if err != nil {
		return false, err
	}
	out := make([]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{"port": x.Port, "lab_name": x.LabName, "node_name": x.NodeName, "username": x.Username, "expiration": x.Expiration, "created": x.Created})
	}
	return false, s.stat("sessions", out)
}

// sshSessionTerminate closes a session, if it is open.
func sshSessionTerminate(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	port, _ := a.integer("port")
	if a.err != nil {
		return false, a.err
	}
	list, err := s.sshSessions(ctx, true)
	if err != nil {
		return false, err
	}
	open := false
	for _, x := range list {
		open = open || x.Port == port
	}
	if !open {
		return false, nil
	}
	if check {
		return true, nil
	}
	return true, s.call(ctx, http.MethodDelete, "/api/v1/ssh/sessions/"+strconv.Itoa(port), nil, nil, nil)
}

// edgesharkRunning reads whether Edgeshark runs, and the status as the server
// gave it.
func (s *session) edgesharkRunning(ctx context.Context) (bool, map[string]any, error) {
	var out map[string]any
	if err := s.call(ctx, http.MethodGet, "/api/v1/tools/edgeshark/status", nil, nil, &out); err != nil {
		return false, nil, err
	}
	running, _ := out["running"].(bool)
	return running, out, nil
}

// edgesharkStatus reads Edgeshark's status.
func edgesharkStatus(ctx context.Context, s *session, _ *args, _ bool) (bool, error) {
	_, status, err := s.edgesharkRunning(ctx)
	if err != nil {
		return false, err
	}
	return false, s.stat("status", status)
}

// edgesharkChange installs or uninstalls Edgeshark, only when it is not
// already so, and records the other as the undo.
func edgesharkChange(verb string) handler {
	wantRunning := verb == "install"
	undo := map[string]string{"install": "uninstall", "uninstall": "install"}[verb]
	return func(ctx context.Context, s *session, _ *args, check bool) (bool, error) {
		running, status, err := s.edgesharkRunning(ctx)
		if err != nil {
			return false, err
		}
		if running == wantRunning {
			return false, s.stat("status", status)
		}
		if check {
			return true, s.stat("status", status)
		}
		if err := s.undo(sdk.Inverse{FQCN: "virt.clab.edgeshark." + undo, Params: map[string]any{},
			Description: "Edgeshark was " + map[bool]string{true: "running", false: "not running"}[running] + " before: virt.clab.edgeshark." + undo + " puts that back.", KeepOnFailure: true}); err != nil {
			return false, err
		}
		if err := s.call(ctx, http.MethodPost, "/api/v1/tools/edgeshark/"+verb, nil, nil, nil); err != nil {
			return false, err
		}
		after, status, err := s.edgesharkRunning(ctx)
		if err != nil {
			return true, err
		}
		if after != wantRunning {
			return true, fmt.Errorf("%s: after the %s Edgeshark is running: %v", s.fqcn, verb, after)
		}
		return true, s.stat("status", status)
	}
}
