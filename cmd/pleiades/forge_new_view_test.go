package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunForgeNewView(t *testing.T) {
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
			name:    "missing title",
			args:    []string{"access-reviews", "--summary", "x"},
			wantErr: "no title",
		},
		{
			name:    "missing summary",
			args:    []string{"access-reviews", "--title", "Access Reviews"},
			wantErr: "no summary",
		},
		{
			// A view name becomes a URL segment, so a traversal in it is a
			// routing question rather than a naming preference.
			name:    "path traversal segment rejected",
			args:    []string{"../../etc", "--title", "Escape", "--summary", "attempt"},
			wantErr: "lowercase letter",
		},
		{
			// Legal for a Collection namespace, not for a view: a dot in a
			// URL segment is not what the registry's name pattern allows.
			name:    "dotted name rejected",
			args:    []string{"access.reviews", "--title", "Access Reviews", "--summary", "x"},
			wantErr: "lowercase letters, digits and hyphens",
		},
		{
			name:    "zero nav order rejected",
			args:    []string{"access-reviews", "--title", "Access Reviews", "--summary", "x", "--nav-order", "0"},
			wantErr: "positive nav order",
		},
		{
			name: "success",
			args: []string{"access-reviews", "--title", "Access Reviews", "--summary", "Who approved what, and when."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append(append([]string{}, tt.args...), "--dir", dir)
			err := runForgeNewView(args)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("runForgeNewView(%v) error = %v, want substring %q", args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runForgeNewView(%v): unexpected error: %v", args, err)
			}

			base := filepath.Join(dir, "internal", "ui", "resources", "accessreviews")
			for _, name := range []string{"accessreviews.go", "accessreviews_test.go"} {
				if _, statErr := os.Stat(filepath.Join(base, name)); statErr != nil {
					t.Errorf("expected %s to exist: %v", filepath.Join(base, name), statErr)
				}
			}
		})
	}
}

// TestRunForgeNewView_RefusesToOverwrite: a generator that silently replaced
// a file would destroy work whose only copy was on disk.
func TestRunForgeNewView_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{"access-reviews", "--title", "Access Reviews", "--summary", "x", "--dir", dir}

	if err := runForgeNewView(args); err != nil {
		t.Fatalf("first run: %v", err)
	}
	err := runForgeNewView(args)
	if err == nil {
		t.Fatal("second run succeeded, so the generator overwrote an existing package")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("second run error = %v, want it to say it refused to overwrite", err)
	}
}

// TestRunForgeNewView_NavLabelDefaultsToTheTitle covers the one derived
// value reaching the generated file, since a nav entry with no text has no
// accessible name.
func TestRunForgeNewView_NavLabelDefaultsToTheTitle(t *testing.T) {
	dir := t.TempDir()
	if err := runForgeNewView([]string{
		"access-reviews", "--title", "Access Reviews", "--summary", "x", "--dir", dir,
	}); err != nil {
		t.Fatalf("runForgeNewView: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "internal", "ui", "resources", "accessreviews", "accessreviews.go"))
	if err != nil {
		t.Fatalf("reading the generated view: %v", err)
	}
	if !strings.Contains(string(body), `NavLabel: "ACCESS REVIEWS"`) {
		t.Error("the generated view did not derive its nav label from the title")
	}
}
