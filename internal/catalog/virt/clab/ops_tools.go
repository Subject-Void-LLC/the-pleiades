// The host-side link methods: vEth pairs and VxLAN tunnels on the server's host.
package clab

import (
	"context"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vethCreate creates a vEth pair.
func vethCreate(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	ea, eb := a.str("a_endpoint"), a.str("b_endpoint")
	mtu, hasMTU := a.integer("mtu")
	if a.err != nil {
		return false, a.err
	}
	body := map[string]any{"aEndpoint": ea, "bEndpoint": eb}
	if hasMTU {
		body["mtu"] = mtu
	}
	return true, s.call(ctx, http.MethodPost, "/api/v1/tools/veth", nil, body, nil)
}

// vxlanCreate creates a VxLAN tunnel; its undo removes the interfaces with
// containerlab's prefix, which is partial when others carry it.
func vxlanCreate(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	remote, link, dev := a.str("remote"), a.ident("link"), a.ident("dev")
	vni, hasVNI := a.integer("vni")
	port, hasPort := a.integer("port")
	mtu, hasMTU := a.integer("mtu")
	if a.err != nil {
		return false, a.err
	}
	body := map[string]any{"remote": remote, "link": link}
	if dev != "" {
		body["dev"] = dev
	}
	for key, v := range map[string]struct {
		n  int
		ok bool
	}{"id": {vni, hasVNI}, "port": {port, hasPort}, "mtu": {mtu, hasMTU}} {
		if v.ok {
			body[key] = v.n
		}
	}
	if err := s.undo(sdk.Inverse{FQCN: "virt.clab.vxlan.delete", Params: map[string]any{"prefix": "vx-"}, Partial: true, KeepOnFailure: true,
		Description: "Removes the VxLAN interfaces named vx-*, which includes the one this run made and any other made with that prefix."}); err != nil {
		return false, err
	}
	return true, s.call(ctx, http.MethodPost, "/api/v1/tools/vxlan", nil, body, nil)
}

// vxlanDelete removes the VxLAN interfaces with a prefix.
func vxlanDelete(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	prefix := a.str("prefix")
	if a.err != nil {
		return false, a.err
	}
	return true, s.call(ctx, http.MethodDelete, "/api/v1/tools/vxlan", query("prefix", prefix), nil, nil)
}
