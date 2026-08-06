package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunForgeNewPlugin(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring expected in the error, "" means no error
	}{
		{
			name:    "missing name",
			args:    []string{},
			wantErr: "missing positional argument",
		},
		{
			name:    "missing description",
			args:    []string{"catalyst_center"},
			wantErr: "has no description",
		},
		{
			name:    "path traversal segment rejected",
			args:    []string{"../../etc", "--description", "escape attempt"},
			wantErr: "invalid plugin name",
		},
		{
			// A dotted name is legal for a Collection namespace and not for
			// a plugin, which names one upstream system rather than a tree.
			name:    "dotted name rejected",
			args:    []string{"net.catalyst", "--description", "dotted"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "scheme-less endpoint rejected",
			args:    []string{"catalyst_center", "--description", "x", "--endpoint", "sandboxdnac.cisco.com"},
			wantErr: "endpoint scheme must be",
		},
		{
			name: "success",
			args: []string{"catalyst_center", "--description", "reads a Cisco Catalyst Center", "--endpoint", "https://sandboxdnac.cisco.com", "--read-only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append(append([]string{}, tt.args...), "--dir", dir)
			err := runForgeNewPlugin(args)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("runForgeNewPlugin(%v) error = %v, want substring %q", args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runForgeNewPlugin(%v): unexpected error: %v", args, err)
			}

			base := filepath.Join(dir, "internal", "inventory", "plugins", "catalystcenter")
			for _, name := range []string{"catalystcenter.go", "catalystcenter_test.go"} {
				if _, statErr := os.Stat(filepath.Join(base, name)); statErr != nil {
					t.Errorf("expected %s to exist: %v", filepath.Join(base, name), statErr)
				}
			}
		})
	}
}

func TestRunForgeNewPlugin_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{"catalyst_center", "--description", "reads a Cisco Catalyst Center", "--dir", dir}

	if err := runForgeNewPlugin(args); err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	err := runForgeNewPlugin(args)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second run error = %v, want a refusing-to-overwrite error", err)
	}
}
