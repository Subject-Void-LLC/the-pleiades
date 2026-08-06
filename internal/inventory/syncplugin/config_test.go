package syncplugin_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/syncplugin"
)

// TestConfig_Validate covers the constraints that hold for every source,
// which is deliberately all this shared type checks: a plugin needing more
// validates that itself in Connect, because a shared type that grows a
// field per implementation stops being shared.
func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     syncplugin.Config
		wantErr string
	}{
		{
			name: "minimal config with only a name",
			cfg:  syncplugin.Config{Name: "netbox"},
		},
		{
			name: "https endpoint",
			cfg:  syncplugin.Config{Name: "catalyst_center", Endpoint: "https://sandboxdnac.cisco.com"},
		},
		{
			name: "file endpoint",
			cfg:  syncplugin.Config{Name: "static_yaml", Endpoint: "file:///srv/project/hosts.yaml"},
		},
		{
			name:    "missing name",
			cfg:     syncplugin.Config{Endpoint: "https://example.test"},
			wantErr: "name is required",
		},
		{
			name:    "whitespace-only name",
			cfg:     syncplugin.Config{Name: "   "},
			wantErr: "name is required",
		},
		{
			name:    "negative page size",
			cfg:     syncplugin.Config{Name: "netbox", PageSize: -1},
			wantErr: "page size must not be negative",
		},
		{
			// The mistake a hand-edited config actually makes: url.Parse
			// accepts this as a path with no host and no scheme.
			name:    "bare hostname with no scheme",
			cfg:     syncplugin.Config{Name: "catalyst_center", Endpoint: "sandboxdnac.cisco.com"},
			wantErr: "endpoint scheme must be",
		},
		{
			name:    "unsupported scheme",
			cfg:     syncplugin.Config{Name: "catalyst_center", Endpoint: "ftp://example.test/x"},
			wantErr: "endpoint scheme must be",
		},
		{
			name:    "https with no host",
			cfg:     syncplugin.Config{Name: "catalyst_center", Endpoint: "https:///just/a/path"},
			wantErr: "endpoint has no host",
		},
		{
			name:    "file with no path",
			cfg:     syncplugin.Config{Name: "static_yaml", Endpoint: "file://"},
			wantErr: "file endpoint has no path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestConfig_EffectivePageSize proves the default resolves in one place
// rather than at each call site.
func TestConfig_EffectivePageSize(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "unset falls back to the default", in: 0, want: syncplugin.DefaultPageSize},
		{name: "negative falls back to the default", in: -5, want: syncplugin.DefaultPageSize},
		{name: "explicit value is honored", in: 25, want: 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := syncplugin.Config{Name: "x", PageSize: tt.in}
			if got := cfg.EffectivePageSize(); got != tt.want {
				t.Errorf("EffectivePageSize() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestConfig_FilePath proves the file:// accessor handles the empty-host
// triple-slash form and rejects everything that is not a file URL, so no
// plugin has to slice the string itself.
func TestConfig_FilePath(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantPath string
		wantOK   bool
	}{
		{name: "absolute file url", endpoint: "file:///srv/hosts.yaml", wantPath: "/srv/hosts.yaml", wantOK: true},
		{name: "path with a space", endpoint: "file:///srv/my%20hosts.yaml", wantPath: "/srv/my hosts.yaml", wantOK: true},
		{name: "https is not a file", endpoint: "https://example.test", wantOK: false},
		{name: "empty endpoint", endpoint: "", wantOK: false},
		{name: "bare path is not a file url", endpoint: "/srv/hosts.yaml", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, ok := syncplugin.Config{Name: "x", Endpoint: tt.endpoint}.FilePath()
			if ok != tt.wantOK {
				t.Fatalf("FilePath() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && path != tt.wantPath {
				t.Errorf("FilePath() = %q, want %q", path, tt.wantPath)
			}
		})
	}
}
