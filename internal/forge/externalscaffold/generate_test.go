// Package externalscaffold_test: tests of what Generate writes.
package externalscaffold_test

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
)

// moduleImportPrefix is this module's import path with a trailing slash,
// the prefix every in-module import in a generated file starts with.
const moduleImportPrefix = "github.com/Subject-Void-LLC/the-pleiades/"

// fileByPath returns the generated file at path, failing the test when
// Generate did not produce one.
func fileByPath(t *testing.T, files []externalscaffold.GeneratedFile, path string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return string(f.Content)
		}
	}
	t.Fatalf("Generate produced no %s; got %v", path, pathsOf(files))
	return ""
}

// pathsOf lists the paths of files, for failure messages.
func pathsOf(files []externalscaffold.GeneratedFile) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

// TestGenerate covers name validation and the shape of what an accepted
// name produces: the four files, at the paths the program directory
// expects, with the method's name and its fixed contract in them.
func TestGenerate(t *testing.T) {
	tests := []struct {
		name        string
		cfg         externalscaffold.Config
		wantErr     string
		wantPaths   []string
		wantInFiles map[string][]string
	}{
		{
			name:      "three segment name",
			cfg:       externalscaffold.Config{Name: "acme.motd.read"},
			wantPaths: []string{"main.go", "read.go", "read_test.go", "README.md", "go.mod"},
			wantInFiles: map[string][]string{
				"main.go": {"package main", "external.Main(ReadDescriptor())", `"acme.motd.read"`},
				"read.go": {
					"package main",
					`const readFQCN = "acme.motd.read"`,
					"func ReadDescriptor() collection.Descriptor",
					"func Read(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error)",
					"capability.NameSSHTransport",
					"sdk.Connect(ctx, rc, device, params, readFQCN)",
					"conn.Run(ctx, readCommand)",
					`const readCommand = "uname -a"`,
					"rc.SetStat(readStatUname,",
					"Reversible: false,",
					"sdk.RecordInverse",
				},
				"read_test.go": {"func TestReadServe(t *testing.T)", "external.CommandDescribe", "collection.SnapshotForTest()"},
				"README.md": {
					"# acme.motd.read",
					"PLEIADES_COLLECTIONS_DIR",
					"github.com/Subject-Void-LLC/the-pleiades",
					"go build -o acme-motd-read .",
					"pleiades validate",
					"pleiades doc acme.motd.read",
					"group or others",
				},
			},
		},
		{
			name:      "two segment name",
			cfg:       externalscaffold.Config{Name: "acme.probe"},
			wantPaths: []string{"main.go", "probe.go", "probe_test.go", "README.md", "go.mod"},
			wantInFiles: map[string][]string{
				"main.go":  {"external.Main(ProbeDescriptor())"},
				"probe.go": {`const probeFQCN = "acme.probe"`},
			},
		},
		{
			name:      "underscored method segment",
			cfg:       externalscaffold.Config{Name: "acme.svc.daemon_reload"},
			wantPaths: []string{"main.go", "daemon-reload.go", "daemon-reload_test.go", "README.md", "go.mod"},
			wantInFiles: map[string][]string{
				"daemon-reload.go": {"func DaemonReload(", "func DaemonReloadDescriptor()", "const daemonReloadFQCN"},
				"README.md":        {"`daemon-reload.go`"},
			},
		},
		{name: "single segment rejected", cfg: externalscaffold.Config{Name: "read"}, wantErr: "not namespaced"},
		{name: "empty name rejected", cfg: externalscaffold.Config{Name: ""}, wantErr: "not namespaced"},
		{name: "empty segment rejected", cfg: externalscaffold.Config{Name: "acme..read"}, wantErr: "invalid name"},
		{name: "path traversal rejected", cfg: externalscaffold.Config{Name: "../../etc.read"}, wantErr: "invalid name"},
		{name: "slash rejected", cfg: externalscaffold.Config{Name: "acme/motd.read"}, wantErr: "invalid name"},
		{name: "uppercase rejected", cfg: externalscaffold.Config{Name: "Acme.motd.read"}, wantErr: "invalid name"},
		{name: "leading digit rejected", cfg: externalscaffold.Config{Name: "acme.3com.read"}, wantErr: "invalid name"},
		{name: "keyword segment rejected, as new-collection does", cfg: externalscaffold.Config{Name: "acme.type.read"}, wantErr: "invalid name"},
		{name: "method named main rejected", cfg: externalscaffold.Config{Name: "acme.branch.main"}, wantErr: "collide"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := externalscaffold.Generate(tt.cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Generate(%q) error = %v, want one containing %q", tt.cfg.Name, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Generate(%q): %v", tt.cfg.Name, err)
			}

			if got := pathsOf(files); !slices.Equal(got, tt.wantPaths) {
				t.Fatalf("paths = %v, want %v", got, tt.wantPaths)
			}

			for _, f := range files {
				if !strings.HasSuffix(f.Path, ".go") {
					continue
				}
				if _, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, parser.AllErrors); err != nil {
					t.Errorf("%s does not parse: %v\n---\n%s", f.Path, err, f.Content)
				}
			}

			for path, wants := range tt.wantInFiles {
				content := fileByPath(t, files, path)
				for _, want := range wants {
					if !strings.Contains(content, want) {
						t.Errorf("%s does not contain %q:\n%s", path, want, content)
					}
				}
			}
		})
	}
}

// TestGenerate_DeclaresAnHonestContract pins the manifest fields whose
// values are claims about the generated body: that it is implemented,
// that it supports check mode through the very function Invoke uses, and
// that the reason both are honest is written beside them.
//
// The fields are matched with flexible whitespace because gofmt aligns
// them against their neighbors, and the alignment is not the claim.
func TestGenerate_DeclaresAnHonestContract(t *testing.T) {
	files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := fileByPath(t, files, "read.go")

	for _, pattern := range []string{
		`Status:\s+collection\.StatusImplemented,`,
		`SupportsCheck:\s+true,`,
		`Invoke:\s+Read,`,
		`Check:\s+Read,`,
		`Notes:\s+"Read-only: `,
	} {
		if !regexp.MustCompile(pattern).MatchString(source) {
			t.Errorf("read.go does not match %s:\n%s", pattern, source)
		}
	}

	// The comment that makes SupportsCheck true honest, and tells the
	// author what to do when it stops being honest.
	for _, want := range []string{
		"honest ONLY because the body writes",
		"write a separate Check",
		"set SupportsCheck to false",
		"calls sdk.RecordInverse",
		"unchecked",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("read.go does not tell the author %q:\n%s", want, source)
		}
	}

	// A body that records an inverse would be refused by the engine in
	// check mode, and this body is the check.
	if strings.Contains(source, "sdk.RecordInverse(") {
		t.Error("the generated body calls sdk.RecordInverse, which a check must never do")
	}
}

// TestGenerate_ImportsOnlyPkg proves the generated program imports nothing
// from this module outside pkg/. That is the whole premise of an external
// Collection: it builds in someone else's module, where Go's internal/
// visibility rule makes every internal/ import a compile error.
func TestGenerate_ImportsOnlyPkg(t *testing.T) {
	files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s does not parse: %v", f.Path, err)
		}
		for _, imp := range parsed.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquoting %s: %v", f.Path, imp.Path.Value, err)
			}
			if strings.HasPrefix(path, moduleImportPrefix) && !strings.HasPrefix(path, moduleImportPrefix+"pkg/") {
				t.Errorf("%s imports %s, which a program outside this module cannot import", f.Path, path)
			}
		}
	}
}

// TestGenerate_FileNamesCarryNoBuildConstraint proves no accepted method
// name gives a generated file a meaning the go command reads from its
// name. It writes each program to disk and asks go/build, for two
// platforms, which files it would compile: exactly main.go and the method
// file as sources, and exactly the method's test as a test.
//
// The names are the dangerous ones. "read_test" would otherwise put the
// descriptor in a test file the build never compiles, and "read_linux"
// or "read_windows" would build on one platform and not the other.
func TestGenerate_FileNamesCarryNoBuildConstraint(t *testing.T) {
	platforms := []struct{ goos, goarch string }{
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	}

	for _, name := range []string{
		"acme.motd.read",
		"acme.motd.read_test",
		"acme.motd.read_linux",
		"acme.motd.read_windows",
		"acme.motd.read_windows_arm64",
		"acme.motd.read_amd64",
	} {
		t.Run(name, func(t *testing.T) {
			files, err := externalscaffold.Generate(externalscaffold.Config{Name: name})
			if err != nil {
				t.Fatalf("Generate(%q): %v", name, err)
			}
			dir := t.TempDir()
			for _, f := range files {
				if err := os.WriteFile(filepath.Join(dir, f.Path), f.Content, 0o600); err != nil {
					t.Fatalf("writing %s: %v", f.Path, err)
				}
			}
			base := files[1].Path

			for _, p := range platforms {
				ctx := build.Default
				ctx.GOOS, ctx.GOARCH = p.goos, p.goarch
				pkg, err := ctx.ImportDir(dir, 0)
				if err != nil {
					t.Fatalf("%s/%s: ImportDir: %v", p.goos, p.goarch, err)
				}
				wantSources := []string{base, "main.go"}
				slices.Sort(wantSources)
				if !slices.Equal(pkg.GoFiles, wantSources) {
					t.Errorf("%s/%s: GoFiles = %v, want %v", p.goos, p.goarch, pkg.GoFiles, wantSources)
				}
				wantTests := []string{strings.TrimSuffix(base, ".go") + "_test.go"}
				if !slices.Equal(pkg.TestGoFiles, wantTests) {
					t.Errorf("%s/%s: TestGoFiles = %v, want %v", p.goos, p.goarch, pkg.TestGoFiles, wantTests)
				}
				if len(pkg.IgnoredGoFiles) != 0 {
					t.Errorf("%s/%s: IgnoredGoFiles = %v, want none", p.goos, p.goarch, pkg.IgnoredGoFiles)
				}
			}
		})
	}
}

// TestConfig_DerivedNames covers the names Generate and the CLI derive
// from a method name: the Go function, the file base, and the default
// program directory.
func TestConfig_DerivedNames(t *testing.T) {
	tests := []struct {
		name         string
		wantFunction string
		wantFileBase string
		wantDir      string
	}{
		{name: "acme.motd.read", wantFunction: "Read", wantFileBase: "read", wantDir: "acme-motd-read"},
		{name: "acme.probe", wantFunction: "Probe", wantFileBase: "probe", wantDir: "acme-probe"},
		{name: "acme.svc.daemon_reload", wantFunction: "DaemonReload", wantFileBase: "daemon-reload", wantDir: "acme-svc-daemon_reload"},
		{name: "acme.motd.read_test", wantFunction: "ReadTest", wantFileBase: "read-test", wantDir: "acme-motd-read_test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := externalscaffold.Config{Name: tt.name}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%q): %v", tt.name, err)
			}
			if got := cfg.FunctionName(); got != tt.wantFunction {
				t.Errorf("FunctionName() = %q, want %q", got, tt.wantFunction)
			}
			if got := cfg.FileBase(); got != tt.wantFileBase {
				t.Errorf("FileBase() = %q, want %q", got, tt.wantFileBase)
			}
			if got := cfg.DefaultDir(); got != tt.wantDir {
				t.Errorf("DefaultDir() = %q, want %q", got, tt.wantDir)
			}
		})
	}
}

// TestGenerate_UserFacingTextRules holds every generated file to the two
// text rules this repository enforces on anything a user reads: no em
// dash, and no citation of an internal document a user's checkout does
// not contain. tools/docs-lint enforces the second on other user-facing
// surfaces; a generated program lands in someone else's repository, so
// it is checked here, at its source.
func TestGenerate_UserFacingTextRules(t *testing.T) {
	files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	forbidden := []string{"\u2014", ".SPECIFICATION", ".AGENTS", ".IGNORE", "PLAN.md", "PATTERNS.md", "IMPLEMENTATION.md"}
	for _, f := range files {
		for _, bad := range forbidden {
			if strings.Contains(string(f.Content), bad) {
				t.Errorf("%s contains %q", f.Path, bad)
			}
		}
	}
}

// BenchmarkGenerate measures one full render: four templates, three of
// them run through go/format. It is the whole cost of a `forge
// new-external` invocation short of the disk writes.
func BenchmarkGenerate(b *testing.B) {
	cfg := externalscaffold.Config{Name: "acme.motd.read"}
	for b.Loop() {
		if _, err := externalscaffold.Generate(cfg); err != nil {
			b.Fatalf("Generate: %v", err)
		}
	}
}

// TestGenerate_EngineConstraintFollowsTheGeneratingBuild covers the
// scaffold's engine version constraint: a release states itself as the
// minimum (a release candidate, the release it is for), and a development
// build states none, so no program ever carries a guessed constraint a
// later release would refuse.
func TestGenerate_EngineConstraintFollowsTheGeneratingBuild(t *testing.T) {
	for engine, want := range map[string]string{
		"1.4.0":                  `EngineVersion: ">=1.4.0",`,
		"v0.3.0":                 `EngineVersion: ">=0.3.0",`,
		"1.4.0-rc2":              `EngineVersion: ">=1.4.0",`,
		"0.0.0-dev+abc123def456": "",
		"0.0.0-dev":              "",
		"":                       "",
	} {
		files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read", Engine: engine})
		if err != nil {
			t.Fatalf("Generate on %q: %v", engine, err)
		}
		method := string(files[1].Content)
		if files[1].Path != "read.go" {
			t.Fatalf("the second file is %s, want the method file", files[1].Path)
		}
		switch {
		case want == "" && strings.Contains(method, "EngineVersion"):
			t.Errorf("a program generated by %q states an engine version:\n%s", engine, method)
		case want != "" && !strings.Contains(method, want):
			t.Errorf("a program generated by %q does not say %s", engine, want)
		}
	}
}
