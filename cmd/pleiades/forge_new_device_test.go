package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunForgeNewDevice(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring expected in the error, "" means no error
	}{
		{
			name:    "missing vendor",
			args:    []string{"--type", "acme_widget"},
			wantErr: "missing positional argument",
		},
		{
			name:    "missing type",
			args:    []string{"acme"},
			wantErr: "--type is required",
		},
		{
			name:    "malformed type key rejected",
			args:    []string{"acme", "--type", "../etc/passwd"},
			wantErr: "invalid type key",
		},
		{
			name:    "unknown capability rejected",
			args:    []string{"acme", "--type", "acme_widget", "--capabilities", "NotARealCapability"},
			wantErr: "unknown capability",
		},
		{
			name: "success",
			args: []string{"acme", "--type", "acme_widget", "--capabilities", "SSHTransportCapable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append(append([]string{}, tt.args...), "--dir", dir)
			err := runForgeNewDevice(args)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("runForgeNewDevice(%v) error = %v, want substring %q", args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runForgeNewDevice(%v): unexpected error: %v", args, err)
			}

			sourcePath := filepath.Join(dir, "internal", "inventory", "devices", "acme", "widget.go")
			if _, statErr := os.Stat(sourcePath); statErr != nil {
				t.Errorf("expected %s to exist: %v", sourcePath, statErr)
			}
			testPath := filepath.Join(dir, "internal", "inventory", "devices", "acme", "widget_test.go")
			if _, statErr := os.Stat(testPath); statErr != nil {
				t.Errorf("expected %s to exist: %v", testPath, statErr)
			}
		})
	}
}

func TestRunForgeNewDevice_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{"acme", "--type", "acme_widget", "--dir", dir}

	if err := runForgeNewDevice(args); err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	err := runForgeNewDevice(args)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second run error = %v, want a refusing-to-overwrite error", err)
	}
}
