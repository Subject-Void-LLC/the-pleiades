package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunForgeNewFilter(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring expected in the error, "" means no error
	}{
		{
			name:    "missing positional",
			args:    []string{},
			wantErr: "missing positional argument",
		},
		{
			name:    "missing return",
			args:    []string{"CIDRToNetmask", "--cel-name", "cidrToNetmask", "--category", "network", "--summary", "x.", "--param", "cidr:string"},
			wantErr: "--return is required",
		},
		{
			name: "lowercase GoName rejected",
			args: []string{
				"cidrToNetmask", "--cel-name", "cidrToNetmask", "--category", "network",
				"--summary", "x.", "--param", "cidr:string", "--return", "string",
			},
			wantErr: "invalid GoName",
		},
		{
			name: "unknown param type with no explicit CELType rejected",
			args: []string{
				"Foo", "--cel-name", "foo", "--category", "network",
				"--summary", "x.", "--param", "counts:[]int", "--return", "string",
			},
			wantErr: "not well-known",
		},
		{
			name: "well-known []string param needs no explicit CELType",
			args: []string{
				"Supernet", "--cel-name", "supernet", "--category", "network",
				"--summary", "x.", "--param", "cidrs:[]string", "--return", "string",
			},
		},
		{
			name: "success, single param",
			args: []string{
				"CIDRToNetmask", "--cel-name", "cidrToNetmask", "--category", "network",
				"--summary", "converts a CIDR prefix length to its dotted-decimal netmask.",
				"--param", "cidr:string", "--return", "string",
			},
		},
		{
			name: "success, two params and an explicit return CELType",
			args: []string{
				"SubnetSplit", "--cel-name", "subnetSplit", "--category", "network",
				"--summary", "splits a CIDR block into subnets.",
				"--param", "cidr:string", "--param", "newPrefix:int",
				"--return", "[]string:cel.ListType(cel.StringType)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append(append([]string{}, tt.args...), "--dir", dir)
			err := runForgeNewFilter(args)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("runForgeNewFilter(%v) error = %v, want substring %q", args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runForgeNewFilter(%v): unexpected error: %v", args, err)
			}
		})
	}
}

func TestRunForgeNewFilter_WritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"CIDRToNetmask", "--cel-name", "cidrToNetmask", "--category", "network",
		"--summary", "converts a CIDR prefix length to its dotted-decimal netmask.",
		"--param", "cidr:string", "--return", "string", "--dir", dir,
	}
	if err := runForgeNewFilter(args); err != nil {
		t.Fatalf("runForgeNewFilter: unexpected error: %v", err)
	}

	base := filepath.Join(dir, "pkg", "filters")
	for _, name := range []string{"cidr_to_netmask.go", "cidr_to_netmask_test.go"} {
		full := filepath.Join(base, name)
		content, statErr := os.ReadFile(full) // #nosec G304 -- test-controlled temp dir path
		if statErr != nil {
			t.Fatalf("expected %s to exist: %v", full, statErr)
		}
		if len(content) == 0 {
			t.Errorf("%s is empty", full)
		}
	}
}

func TestRunForgeNewFilter_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"CIDRToNetmask", "--cel-name", "cidrToNetmask", "--category", "network",
		"--summary", "x.", "--param", "cidr:string", "--return", "string", "--dir", dir,
	}

	if err := runForgeNewFilter(args); err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	err := runForgeNewFilter(args)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second run error = %v, want a refusing-to-overwrite error", err)
	}
}

func TestRunForgeNewFilter_SkipExisting(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"CIDRToNetmask", "--cel-name", "cidrToNetmask", "--category", "network",
		"--summary", "x.", "--param", "cidr:string", "--return", "string", "--dir", dir,
	}

	if err := runForgeNewFilter(args); err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	if err := runForgeNewFilter(append(append([]string{}, args...), "--skip-existing")); err != nil {
		t.Fatalf("second run with --skip-existing: unexpected error: %v", err)
	}
}
