package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunForgeNewCollection(t *testing.T) {
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
			name:    "not namespaced",
			args:    []string{"install"},
			wantErr: "not namespaced",
		},
		{
			name:    "path traversal segment rejected",
			args:    []string{"pkg..install"},
			wantErr: "invalid name",
		},
		{
			name:    "unknown capability rejected",
			args:    []string{"pkg.apt.install", "--capabilities", "NotARealCapability"},
			wantErr: "unknown capability",
		},
		{
			name: "success",
			args: []string{"pkg.apt.install", "--capabilities", "AptCapable", "--transports", "ssh", "--requires-elevation", "--engine-version", ">=1.0.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append(append([]string{}, tt.args...), "--dir", dir)
			err := runForgeNewCollection(args)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("runForgeNewCollection(%v) error = %v, want substring %q", args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runForgeNewCollection(%v): unexpected error: %v", args, err)
			}

			sourcePath := filepath.Join(dir, "internal", "catalog", "pkg", "apt", "install.go")
			if _, statErr := os.Stat(sourcePath); statErr != nil {
				t.Errorf("expected %s to exist: %v", sourcePath, statErr)
			}
			testPath := filepath.Join(dir, "internal", "catalog", "pkg", "apt", "install_test.go")
			if _, statErr := os.Stat(testPath); statErr != nil {
				t.Errorf("expected %s to exist: %v", testPath, statErr)
			}
		})
	}
}

func TestRunForgeNewCollection_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{"pkg.apt.install", "--dir", dir}

	if err := runForgeNewCollection(args); err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	err := runForgeNewCollection(args)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second run error = %v, want a refusing-to-overwrite error", err)
	}
}
