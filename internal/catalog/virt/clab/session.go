// Package clab implements the virt.clab Collection methods, which deploy,
// inspect and change containerlab labs through a containerlab API server. This
// file holds what every method shares: the login, the node lookup, and the
// record of what a run stated.
package clab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clabapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// timeouts: how long a method may take. A deploy builds containers, so it gets
// minutes; the rest are single calls.
const (
	shortTimeout = 2 * time.Minute
	longTimeout  = 15 * time.Minute
)

// reauthDelay is how long a refused token waits before the fresh login that
// replaces it: the real server also refuses a token issued in the same moment
// the first one was, so an immediate retry fails the same way.
var reauthDelay = 1500 * time.Millisecond

// session is one method run's way to the API server: a client, and the token
// its login returns, made on the first call and held for this run only.
type session struct {
	fqcn string
	rc   sdk.RunbookContext
	a    *args
	c    *clabapi.Client

	auth       string
	user, pass string
	token      string
	// anonymous is set for a call that needs no login (/health).
	anonymous bool
}

// open prepares a method run's way to the server device without touching the
// network: the device must be the containerlab API server onboarding
// recognised, and the base URL and TLS are the device's own. The login is made
// by the first call, so a parameter that is refused is refused before the
// server hears of the run.
func open(rc sdk.RunbookContext, device inventory.InventoryItem, fqcn string, a *args, anonymous bool) (*session, error) {
	api, ok := device.(capability.ContainerlabAPICapable)
	if device == nil || !ok || !device.HasCapability(capability.NameClabAPI) {
		return nil, fmt.Errorf("%s: the target must be a containerlab API server, a generic_http device that onboarding recognised (it declares %s): run pleiades onboard on it", fqcn, capability.NameClabAPI)
	}
	base, err := httpapi.ValidateBaseURL(api.HTTPBaseURL(), api.HTTPAuth(), api.HTTPAllowPlaintextCredentials())
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	secrets := rc.InjectSecrets()
	settings := devicetls.For(device)
	warnings := settings.Warnings(device.Name())
	if !anonymous && httpapi.SendsPlaintextCredential(base, api.HTTPAuth()) {
		warnings = append(warnings, httpapi.PlaintextWarning(device.Name()))
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	tlsConfig, err := tlsFor(base, settings, secrets)
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	if !anonymous {
		switch api.HTTPAuth() {
		case httpapi.AuthBasic, httpapi.AuthBearer:
		default:
			return nil, fmt.Errorf("%s: the API server needs a login: store its username and password for this device and set http_auth basic (or bearer, with a token)", fqcn)
		}
	}
	return &session{
		fqcn: fqcn, rc: rc, a: a, anonymous: anonymous,
		c:    clabapi.NewClient(base, tlsConfig, secrets[wire.SecretPassword]),
		auth: api.HTTPAuth(), user: secrets[wire.SecretUsername], pass: secrets[wire.SecretPassword],
	}, nil
}

// ensure logs in, once, when the call needs it.
func (s *session) ensure(ctx context.Context) error {
	if s.anonymous || s.token != "" {
		return nil
	}
	if s.auth == httpapi.AuthBearer {
		if s.pass == "" {
			return fmt.Errorf("%s: http_auth bearer needs the token stored as this device's password", s.fqcn)
		}
		s.token = s.pass
		return nil
	}
	token, err := s.c.Login(ctx, s.user, s.pass)
	if err != nil {
		return fmt.Errorf("%s: %w", s.fqcn, err)
	}
	s.token = token
	return nil
}

// call sends one request with the session's token and decodes the answer. A
// parameter already found wrong stops it first, so nothing is sent for a run
// that is going to be refused.
func (s *session) call(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	if s.a != nil && s.a.err != nil {
		return fmt.Errorf("%s: %w", s.fqcn, s.a.err)
	}
	if err := s.ensure(ctx); err != nil {
		return err
	}
	err := s.c.Call(ctx, s.token, method, path, q, jsonBody(body), out)
	// The real server refuses the first token it issues after it starts, and
	// a token can run out in a long run. A 401 means the request was refused
	// before it ran, so a fresh login and one more try repeats nothing. A
	// stored bearer token is not ours to renew, and a call made with no
	// login has nothing to renew.
	if clabapi.Unauthorized(err) && s.auth == httpapi.AuthBasic && !s.anonymous {
		s.token = ""
		select {
		case <-time.After(reauthDelay):
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", s.fqcn, ctx.Err())
		}
		if lerr := s.ensure(ctx); lerr != nil {
			return lerr
		}
		err = s.c.Call(ctx, s.token, method, path, q, jsonBody(body), out)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", s.fqcn, err)
	}
	return nil
}

// stat records one result.
func (s *session) stat(key string, value any) error { return s.rc.SetStat(key, value) }

// stats records each pair, stopping at the first failure.
func (s *session) stats(pairs map[string]any) error {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := s.stat(k, pairs[k]); err != nil {
			return err
		}
	}
	return nil
}

// undo records the instruction that reverses what a run is about to change or
// has changed.
func (s *session) undo(inv sdk.Inverse) error { return sdk.RecordInverse(s.rc, inv) }

// absent reports whether err is the server saying the lab is not there.
func absent(err error) bool {
	for e := err; e != nil; e = unwrap(e) {
		if clabapi.NotFound(e) {
			return true
		}
	}
	return false
}

// unwrap is errors.Unwrap without the import, for absent's loop.
func unwrap(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// nodeInfo is one node of a lab as the server's inspect reports it.
type nodeInfo struct {
	Name        string `json:"name"`
	NodeName    string `json:"nodeName"`
	Kind        string `json:"kind"`
	Image       string `json:"image"`
	State       string `json:"state"`
	Status      string `json:"status"`
	IPv4        string `json:"ipv4_address"`
	IPv6        string `json:"ipv6_address"`
	ContainerID string `json:"container_id"`
	Ports       []struct {
		HostIP   string `json:"host_ip"`
		HostPort int    `json:"host_port"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"ports"`
}

// asMap is the node as a result: the stable names a later task reads.
func (n nodeInfo) asMap() map[string]any {
	ports := make([]any, 0, len(n.Ports))
	for _, p := range n.Ports {
		ports = append(ports, map[string]any{"host_ip": p.HostIP, "host_port": p.HostPort, "port": p.Port, "protocol": p.Protocol})
	}
	return map[string]any{"ports": ports, "name": n.Name, "node": n.NodeName, "kind": n.Kind, "image": n.Image, "state": n.State,
		"status": n.Status, "ipv4_address": n.IPv4, "ipv6_address": n.IPv6, "container_id": n.ContainerID}
}

// nodeMaps turns nodes into results, ordered by node name.
func nodeMaps(nodes []nodeInfo) []any {
	sorted := append([]nodeInfo(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NodeName < sorted[j].NodeName })
	out := make([]any, 0, len(sorted))
	for _, n := range sorted {
		out = append(out, n.asMap())
	}
	return out
}

// inspect returns a lab's nodes, and whether the lab is deployed.
func (s *session) inspect(ctx context.Context, lab string) ([]nodeInfo, bool, error) {
	var nodes []nodeInfo
	err := s.call(ctx, http.MethodGet, "/api/v1/labs/"+url.PathEscape(lab), nil, nil, &nodes)
	switch {
	case absent(err):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return nodes, true, nil
}

// node finds one node of a deployed lab by its name in the lab or by its
// container's name, and refuses with the names there are.
func (s *session) node(ctx context.Context, lab, name string) (nodeInfo, error) {
	nodes, exists, err := s.inspect(ctx, lab)
	if err != nil {
		return nodeInfo{}, err
	}
	if !exists {
		return nodeInfo{}, fmt.Errorf("%s: lab %q is not deployed", s.fqcn, lab)
	}
	var names []string
	for _, n := range nodes {
		if n.NodeName == name || n.Name == name {
			return n, nil
		}
		names = append(names, n.NodeName)
	}
	sort.Strings(names)
	return nodeInfo{}, fmt.Errorf("%s: lab %q has no node %q (its nodes: %s)", s.fqcn, lab, name, strings.Join(names, ", "))
}
