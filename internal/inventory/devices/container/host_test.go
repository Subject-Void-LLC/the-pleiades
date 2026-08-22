package container_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestNewHost_ConstructsFromRecord proves the constructor hydrates a real
// Record without error and that basic identity fields round-trip.
func TestNewHost_ConstructsFromRecord(t *testing.T) {
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if got := string(item.ID()); got != "t1" {
		t.Errorf("ID() = %q, want %q", got, "t1")
	}
	if got := item.Name(); got != "t1" {
		t.Errorf("Name() = %q, want %q", got, "t1")
	}
}

// TestNewHost_BaselineCapabilities is the regression proof this device
// type exists for: unlike a freshly scaffolded, not-yet-hand-completed
// type, Host structurally implements both capabilities its baseline
// declares, so HasCapability -- the same check
// engine.checkMethodCapabilities runs before dispatching
// container.docker.run/stop/remove -- returns true for both, not just
// Capabilities() at the data layer.
func TestNewHost_BaselineCapabilities(t *testing.T) {
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("expected the vendor baseline to include SSHTransportCapable")
	}
	if !item.HasCapability(capability.NameDocker) {
		t.Error("expected the vendor baseline to include DockerCapable")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected no capability beyond the vendor baseline with no classification data")
	}
}

// TestNewHost_UnionsClassificationCapabilities mirrors
// linux.TestNewServer_UnionsClassificationCapabilities: classification-
// derived Capabilities are unioned into the declared set at the data
// layer, but HasCapability for a capability Host does not structurally
// implement correctly stays false -- neither side is trusted alone.
func TestNewHost_UnionsClassificationCapabilities(t *testing.T) {
	rec := record.Record{
		ID:           "t1",
		Name:         "t1",
		Type:         "container_host",
		Capabilities: []capability.Name{capability.NameApt},
	}
	item, err := container.NewHost(rec)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	declared := item.Capabilities()
	found := false
	for _, c := range declared {
		if c == capability.NameApt {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected classification Capabilities() to be unioned into the declared set")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected HasCapability(AptCapable) to stay false: Host does not structurally implement AptCapable")
	}
}

// TestHost_SSHHost proves the "host" property round-trips, and that an
// absent one reads back empty rather than panicking -- the same contract
// linux.Server.SSHHost documents.
func TestHost_SSHHost(t *testing.T) {
	withHost, err := container.NewHost(record.Record{
		ID: "t1", Name: "t1", Type: "container_host",
		Properties: map[string]any{"host": "10.0.0.5"},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	h, ok := withHost.(*container.Host)
	if !ok {
		t.Fatalf("NewHost returned %T, want *container.Host", withHost)
	}
	if got := h.SSHHost(); got != "10.0.0.5" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.5")
	}

	noHost, err := container.NewHost(record.Record{ID: "t2", Name: "t2", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if got := noHost.(*container.Host).SSHHost(); got != "" {
		t.Errorf("SSHHost() with no host property = %q, want empty string", got)
	}
}

// TestHost_SSHPort proves the "port" property round-trips and that an
// absent or zero value defaults to 22, matching linux.Server.SSHPort's
// own documented default.
func TestHost_SSHPort(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]any
		want       int
	}{
		{name: "explicit port", properties: map[string]any{"port": 2222}, want: 2222},
		{name: "absent defaults to 22", properties: nil, want: 22},
		{name: "explicit zero defaults to 22", properties: map[string]any{"port": 0}, want: 22},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := container.NewHost(record.Record{
				ID: "t1", Name: "t1", Type: "container_host", Properties: tt.properties,
			})
			if err != nil {
				t.Fatalf("NewHost: %v", err)
			}
			if got := item.(*container.Host).SSHPort(); got != tt.want {
				t.Errorf("SSHPort() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestHost_DockerEndpoint proves the "docker_endpoint" property
// round-trips and that an absent or empty value defaults to the
// conventional POSIX socket path, so a container host needs no
// configuration at all for the common case.
func TestHost_DockerEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]any
		want       capability.SocketAddress
	}{
		{
			name:       "explicit Windows named pipe",
			properties: map[string]any{"docker_endpoint": "npipe:////./pipe/docker_engine"},
			want:       "npipe:////./pipe/docker_engine",
		},
		{
			name:       "explicit POSIX socket path",
			properties: map[string]any{"docker_endpoint": "/run/user/1000/docker.sock"},
			want:       "/run/user/1000/docker.sock",
		},
		{name: "absent defaults to the conventional socket path", properties: nil, want: "/var/run/docker.sock"},
		{name: "explicit empty defaults to the conventional socket path", properties: map[string]any{"docker_endpoint": ""}, want: "/var/run/docker.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := container.NewHost(record.Record{
				ID: "t1", Name: "t1", Type: "container_host", Properties: tt.properties,
			})
			if err != nil {
				t.Fatalf("NewHost: %v", err)
			}
			if got := item.(*container.Host).DockerEndpoint(); got != tt.want {
				t.Errorf("DockerEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
