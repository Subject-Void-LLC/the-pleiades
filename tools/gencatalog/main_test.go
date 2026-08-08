package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/pluginscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

func TestNewCollectionArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  collectionscaffold.Config
		want []string
	}{
		{
			name: "bare minimum",
			cfg:  collectionscaffold.Config{Name: "http.request"},
			want: []string{"forge", "new-collection", "http.request"},
		},
		{
			name: "every flag set",
			cfg: collectionscaffold.Config{
				Name:              "pkg.apt.install",
				Capabilities:      []capability.Name{capability.NameApt},
				Transports:        []string{"ssh"},
				RequiresElevation: true,
				EngineVersion:     ">=1.0.0",
			},
			want: []string{
				"forge", "new-collection", "pkg.apt.install",
				"--capabilities", "AptCapable",
				"--transports", "ssh",
				"--requires-elevation",
				"--engine-version", ">=1.0.0",
			},
		},
		{
			name: "multiple capabilities and transports",
			cfg: collectionscaffold.Config{
				Name:         "net.cli.command",
				Capabilities: []capability.Name{capability.NameNetworkCLI, capability.NameLinux},
				Transports:   []string{"ssh", "telnet"},
			},
			want: []string{
				"forge", "new-collection", "net.cli.command",
				"--capabilities", "NetworkCLICapable,LinuxCapable",
				"--transports", "ssh,telnet",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newCollectionArgs(tc.cfg)
			if !equalArgs(got, tc.want) {
				t.Errorf("newCollectionArgs(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

func TestNewDeviceArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  devicescaffold.Config
		want []string
	}{
		{
			name: "no capabilities",
			cfg:  devicescaffold.Config{Vendor: "aws", TypeKey: "aws_account"},
			want: []string{"forge", "new-device", "aws", "--type", "aws_account"},
		},
		{
			name: "with capabilities",
			cfg: devicescaffold.Config{
				Vendor:       "windows",
				TypeKey:      "windows_server",
				Capabilities: []capability.Name{capability.NameWindows, capability.NameWinRM},
			},
			want: []string{
				"forge", "new-device", "windows", "--type", "windows_server",
				"--capabilities", "WindowsCapable,WinRMCapable",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newDeviceArgs(tc.cfg)
			if !equalArgs(got, tc.want) {
				t.Errorf("newDeviceArgs(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

func TestNewPluginArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  pluginscaffold.Config
		want []string
	}{
		{
			name: "minimal",
			cfg:  pluginscaffold.Config{Name: "netbox", Description: "reads NetBox"},
			want: []string{"forge", "new-plugin", "netbox", "--description", "reads NetBox"},
		},
		{
			name: "endpoint and read-only",
			cfg: pluginscaffold.Config{
				Name:        "catalyst_center",
				Description: "reads a Catalyst Center",
				Endpoint:    "https://sandboxdnac.cisco.com",
				ReadOnly:    true,
			},
			want: []string{
				"forge", "new-plugin", "catalyst_center",
				"--description", "reads a Catalyst Center",
				"--endpoint", "https://sandboxdnac.cisco.com",
				"--read-only",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newPluginArgs(tc.cfg)
			if !equalArgs(got, tc.want) {
				t.Errorf("newPluginArgs(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestValidateCatalogEntries_CatchesInvalidEntry(t *testing.T) {
	collections := []collectionscaffold.Config{
		{Name: "not-namespaced"}, // missing a dot: invalid
	}
	devices := []devicescaffold.Config{
		{Vendor: "ok", TypeKey: "ok_thing"},
	}

	plugins := []pluginscaffold.Config{
		{Name: "ok_plugin", Description: "a valid entry, so only the collection above is at fault"},
	}

	err := validateCatalogEntries(collections, devices, plugins)
	if err == nil {
		t.Fatal("expected an error for the un-namespaced collection name")
	}
	if !strings.Contains(err.Error(), "not-namespaced") {
		t.Errorf("expected the error to name the bad entry, got: %v", err)
	}
}

// TestValidateCatalogEntries_CatchesInvalidDeviceAndPlugin exercises the
// device and plugin validation loops independently of the collection loop
// above, so a regression in either is caught even when every collection
// entry is valid.
func TestValidateCatalogEntries_CatchesInvalidDeviceAndPlugin(t *testing.T) {
	collections := []collectionscaffold.Config{
		{Name: "pkg.apt.install"},
	}
	devices := []devicescaffold.Config{
		{Vendor: "bad vendor", TypeKey: "ok_thing"}, // embedded space: invalid
	}
	plugins := []pluginscaffold.Config{
		{Name: "ok_plugin", Description: ""}, // empty description: invalid
	}

	err := validateCatalogEntries(collections, devices, plugins)
	if err == nil {
		t.Fatal("expected an error for the invalid device and plugin entries")
	}
	if !strings.Contains(err.Error(), "bad vendor") {
		t.Errorf("expected the error to name the bad vendor, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ok_plugin") {
		t.Errorf("expected the error to name the bad plugin, got: %v", err)
	}
}

func TestFindModuleRoot_ErrorsWhenNoGoModInAnyParent(t *testing.T) {
	// findModuleRoot walks up from os.Getwd(), so proving the "not found"
	// branch means actually chdir'ing somewhere with no go.mod anywhere
	// above it up to the filesystem root.
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restoring cwd to %q: %v", orig, err)
		}
	})

	if _, err := findModuleRoot(); err == nil {
		t.Fatal("findModuleRoot from a directory with no go.mod above it: expected an error, got nil")
	}
}

func TestBuildPleiadesBinary_ErrorsOnBuildFailure(t *testing.T) {
	// root has no go.mod and no cmd/pleiades package, so the build this
	// function shells out to must fail.
	root := t.TempDir()

	_, _, err := buildPleiadesBinary(root)
	if err == nil {
		t.Fatal("buildPleiadesBinary against a directory with no cmd/pleiades package: expected an error, got nil")
	}
}

func TestRunPleiades_ErrorsForNonexistentBinary(t *testing.T) {
	err := runPleiades(filepath.Join(t.TempDir(), "no-such-binary"), t.TempDir(), "forge", "new-collection", "test.x")
	if err == nil {
		t.Fatal("runPleiades against a nonexistent binary: expected an error, got nil")
	}
}

func TestWriteCatalogBuiltins_ErrorsWhenDestDirMissing(t *testing.T) {
	// root's internal/catalog directory is never created, so the WriteFile
	// this function does must fail.
	root := t.TempDir()

	err := writeCatalogBuiltins(root, []collectionscaffold.Config{{Name: "svc.start"}})
	if err == nil {
		t.Fatal("writeCatalogBuiltins with a missing destination directory: expected an error, got nil")
	}
}

func TestWriteCatalogBuiltins_DedupesAndSorts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}

	collections := []collectionscaffold.Config{
		{Name: "svc.start"},
		{Name: "svc.stop"}, // same package as svc.start: must appear once
		{Name: "pkg.apt.install"},
		{Name: "exec.command"},
	}

	if err := writeCatalogBuiltins(root, collections); err != nil {
		t.Fatalf("writeCatalogBuiltins: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "internal", "catalog", "builtins.go"))
	if err != nil {
		t.Fatalf("reading generated builtins.go: %v", err)
	}
	content := string(data)

	wantImports := []string{
		`"github.com/SubjectVoidLLC/the-pleiades/internal/catalog/exec"`,
		`"github.com/SubjectVoidLLC/the-pleiades/internal/catalog/pkg/apt"`,
		`"github.com/SubjectVoidLLC/the-pleiades/internal/catalog/svc"`,
	}
	for _, want := range wantImports {
		if !strings.Contains(content, want) {
			t.Errorf("expected generated builtins.go to import %s, got:\n%s", want, content)
		}
	}
	if strings.Count(content, `"github.com/SubjectVoidLLC/the-pleiades/internal/catalog/svc"`) != 1 {
		t.Errorf("expected the svc package to be imported exactly once despite two entries sharing it, got:\n%s", content)
	}
	// exec sorts before pkg/apt sorts before svc: assert that order.
	execIdx := strings.Index(content, "catalog/exec")
	pkgIdx := strings.Index(content, "catalog/pkg/apt")
	svcIdx := strings.Index(content, "catalog/svc")
	if !(execIdx < pkgIdx && pkgIdx < svcIdx) {
		t.Errorf("expected sorted import order exec < pkg/apt < svc, got:\n%s", content)
	}
}

// TestGencatalog_DogfoodsRealCLI_EndToEnd proves gencatalog's own
// generation path (buildPleiadesBinary + runPleiades +
// newCollectionArgs/newDeviceArgs) really drives the real built pleiades
// binary, not a stand-in: it generates one synthetic collection and one
// synthetic device into the real repository tree (unique per-process
// names, cleaned up via t.Cleanup, mirroring
// cmd/pleiades/e2e_test.go's TestCLI_ForgeNewCollection_EndToEnd /
// TestCLI_ForgeNewDevice_EndToEnd), then go build/tests the result. This
// is Phase 34's own dogfood requirement applied to gencatalog itself: a
// unit test calling collectionscaffold.Generate directly would prove the
// library works, not gencatalog's own CLI-driving code.
func TestGencatalog_DogfoodsRealCLI_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("findModuleRoot: %v", err)
	}

	binPath, cleanup, err := buildPleiadesBinary(root)
	if err != nil {
		t.Fatalf("buildPleiadesBinary: %v", err)
	}
	defer cleanup()

	suffix := fmt.Sprintf("gencatalogtest%d", os.Getpid())
	collectionCfg := collectionscaffold.Config{
		Name:         "test." + suffix + ".check",
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Transports:   []string{"ssh"},
	}
	deviceCfg := devicescaffold.Config{
		Vendor:       suffix,
		TypeKey:      suffix + "_widget",
		Capabilities: []capability.Name{capability.NameSSHTransport},
	}

	t.Cleanup(func() {
		// Scoped to this test's own suffix directory, never the shared
		// internal/catalog/test parent: cmd/pleiades's forge end-to-end
		// test writes a sibling under the same parent, and removing the
		// parent would delete it mid-build under a parallel run.
		_ = os.RemoveAll(filepath.Join(root, "internal", "catalog", "test", suffix))
		_ = os.RemoveAll(filepath.Join(root, "internal", "inventory", "devices", suffix))
	})

	if err := validateCatalogEntries([]collectionscaffold.Config{collectionCfg}, []devicescaffold.Config{deviceCfg}, nil); err != nil {
		t.Fatalf("validateCatalogEntries: %v", err)
	}
	if err := runPleiades(binPath, root, newCollectionArgs(collectionCfg)...); err != nil {
		t.Fatalf("runPleiades new-collection: %v", err)
	}
	if err := runPleiades(binPath, root, newDeviceArgs(deviceCfg)...); err != nil {
		t.Fatalf("runPleiades new-device: %v", err)
	}

	collectionImport := "github.com/SubjectVoidLLC/the-pleiades/internal/catalog/test/" + suffix
	deviceImport := "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/" + suffix
	for _, pkg := range []string{collectionImport, deviceImport} {
		for _, subcmd := range []string{"build", "test"} {
			cmd := exec.Command("go", subcmd, pkg)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go %s %s failed: %v\n%s", subcmd, pkg, err, out)
			}
		}
	}
}
