// Tests for Route, the carrier choice sessions and plans share.
package containerengine

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// engineHost is a device record as onboarding leaves one: the engines it
// runs, what it advertises, and per engine a socket and proven carriers.
// The methods a route never calls stay on the nil embedded interface.
type engineHost struct {
	inventory.InventoryItem
	caps     []capability.Name
	engines  []capability.EngineKind
	socket   capability.SocketAddress
	carriers []capability.Carrier
	tlsAddr  string
}

func (h *engineHost) Name() string                                                { return "box" }
func (h *engineHost) HasCapability(n capability.Name) bool                        { return slices.Contains(h.caps, n) }
func (h *engineHost) ContainerEngines() []capability.EngineKind                   { return h.engines }
func (h *engineHost) EngineSocket(capability.EngineKind) capability.SocketAddress { return h.socket }
func (h *engineHost) EngineID(capability.EngineKind) capability.EngineID          { return "id" }
func (h *engineHost) EngineCarriers(capability.EngineKind) []capability.Carrier   { return h.carriers }
func (h *engineHost) EngineLoginUser() string                                     { return "deploy" }
func (h *engineHost) EngineTLSAddress() string                                    { return h.tlsAddr }

// TestRoute_TakesTheFirstUsableCarrierInOrder proves the order (local,
// socket, stdio, TLS) and that a carrier is usable only when onboarding
// proved it, the device advertises it, and the record holds what it needs.
func TestRoute_TakesTheFirstUsableCarrierInOrder(t *testing.T) {
	all := []capability.Name{capability.NameContainerEngine, capability.NameDocker,
		capability.NameEngineLocal, capability.NameEngineSocket, capability.NameEngineStdio, capability.NameEngineTLS}
	for _, tc := range []struct {
		name     string
		host     engineHost
		want     capability.Carrier
		wantFail string
	}{
		{"socket before stdio", engineHost{caps: all, socket: "/s", carriers: []capability.Carrier{"stdio", "socket"}}, capability.CarrierSocket, ""},
		{"stdio when forwarding is denied", engineHost{caps: all, socket: "/s", carriers: []capability.Carrier{"stdio"}}, capability.CarrierStdio, ""},
		{"no socket path skips the socket carriers", engineHost{caps: all, carriers: []capability.Carrier{"local", "socket", "stdio"}}, capability.CarrierStdio, ""},
		{"proven but not advertised is skipped", engineHost{caps: all[:2], socket: "/s", carriers: []capability.Carrier{"socket"}}, "", "no way to reach"},
		{"TLS needs an address", engineHost{caps: all, carriers: []capability.Carrier{"tls"}}, "", "no way to reach"},
		{"TLS with an address", engineHost{caps: all, carriers: []capability.Carrier{"tls"}, tlsAddr: "box:2376"}, capability.CarrierTLS, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.host
			h.engines = []capability.EngineKind{capability.EngineDocker}
			kind, carrier, err := Route(&h, nil, capability.EngineDocker)
			if tc.wantFail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
					t.Fatalf("Route = %s %s, %v; want a refusal %q", kind, carrier, err, tc.wantFail)
				}
				return
			}
			if err != nil || kind != capability.EngineDocker || carrier != tc.want {
				t.Fatalf("Route = %s %s, %v; want docker %s", kind, carrier, err, tc.want)
			}
			if carrier.Transport() != "engine_"+string(tc.want) {
				t.Errorf("Transport() = %q", carrier.Transport())
			}
		})
	}
}

// TestRoute_RefusesAnotherEngine proves an engine-only method is not routed
// to an engine the task names otherwise.
func TestRoute_RefusesAnotherEngine(t *testing.T) {
	h := engineHost{caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket},
		engines: []capability.EngineKind{capability.EngineDocker}, socket: "/s", carriers: []capability.Carrier{"socket"}}
	if _, _, err := Route(&h, map[string]any{ParamEngine: "podman"}, capability.EngineDocker); err == nil {
		t.Fatal("a Docker-only method was routed for a task naming podman")
	}
}
