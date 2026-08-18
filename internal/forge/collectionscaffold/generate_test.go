package collectionscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name       string
		cfg        collectionscaffold.Config
		wantErr    bool
		wantSource []string
	}{
		{
			name: "three segment name",
			cfg: collectionscaffold.Config{
				Name:              "pkg.apt.install",
				Capabilities:      []capability.Name{capability.NameApt},
				Transports:        []string{"ssh"},
				RequiresElevation: true,
				EngineVersion:     ">=1.0.0",
			},
			wantSource: []string{
				"package apt",
				`Name: "pkg.apt.install"`,
				`capability.Name("AptCapable")`,
				`"ssh"`,
				"RequiresElevation: true",
				`EngineVersion:   ">=1.0.0"`,
				"Status:          collection.StatusDeclared",
				"func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error)",
				`return collection.Result{}, fmt.Errorf("pkg.apt.install: not implemented")`,
			},
		},
		{
			name: "two segment name (bare domain, PLAN.md Section 2 allows this)",
			cfg:  collectionscaffold.Config{Name: "exec.command"},
			wantSource: []string{
				"package exec",
				`Name: "exec.command"`,
				"func Command(",
				"RequiresElevation: false",
			},
		},
		{
			name:    "single segment rejected (not namespaced)",
			cfg:     collectionscaffold.Config{Name: "install"},
			wantErr: true,
		},
		{
			name:    "empty name rejected",
			cfg:     collectionscaffold.Config{Name: ""},
			wantErr: true,
		},
		{
			name:    "path traversal segment rejected",
			cfg:     collectionscaffold.Config{Name: "pkg..install"},
			wantErr: true,
		},
		{
			name:    "empty transport rejected",
			cfg:     collectionscaffold.Config{Name: "pkg.apt.install", Transports: []string{""}},
			wantErr: true,
		},
		{
			name:    "unknown capability rejected",
			cfg:     collectionscaffold.Config{Name: "pkg.apt.install", Capabilities: []capability.Name{"NotARealCapability"}},
			wantErr: true,
		},
		{
			name:    "keyword segment rejected",
			cfg:     collectionscaffold.Config{Name: "pkg.type.install"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := collectionscaffold.Generate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Generate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(files) != 2 {
				t.Fatalf("Generate() returned %d files, want 2", len(files))
			}

			var sourceFile, testFile *collectionscaffold.GeneratedFile
			for i := range files {
				if strings.HasSuffix(files[i].Path, "_test.go") {
					testFile = &files[i]
				} else {
					sourceFile = &files[i]
				}
			}
			if sourceFile == nil || testFile == nil {
				t.Fatalf("Generate() did not return exactly one source and one test file: %+v", files)
			}

			if !strings.HasPrefix(sourceFile.Path, "internal/catalog/") {
				t.Errorf("source path = %q, want prefix %q", sourceFile.Path, "internal/catalog/")
			}

			for _, f := range files {
				fset := token.NewFileSet()
				if _, err := parser.ParseFile(fset, f.Path, f.Content, parser.AllErrors); err != nil {
					t.Errorf("%s: generated source does not parse: %v\n---\n%s", f.Path, err, f.Content)
				}
			}

			for _, want := range tt.wantSource {
				if !strings.Contains(string(sourceFile.Content), want) {
					t.Errorf("generated source missing %q\n---\n%s", want, sourceFile.Content)
				}
			}
		})
	}
}

func TestGenerate_NestedPackagePath(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{Name: "pkg.apt.install"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := "internal/catalog/pkg/apt/install.go"
	if files[0].Path != want {
		t.Errorf("source path = %q, want %q", files[0].Path, want)
	}
}

func TestGenerate_ZeroValueManifestFields(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{Name: "wait.port"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, files[0].Path, files[0].Content, parser.AllErrors); err != nil {
		t.Fatalf("generated source with zero-value manifest fields does not parse: %v\n---\n%s", err, files[0].Content)
	}
}

// TestGenerate_DefaultsTheEngineVersion covers the field that used to be
// emitted empty.
//
// An empty constraint is not a permissive one, it is a field that says
// nothing, and every hand-written manifest in this catalog declares
// ">=1.0.0". A generated one that declared nothing left a reader unable
// to tell "no opinion" from "forgot".
func TestGenerate_DefaultsTheEngineVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "unset", version: "", want: `EngineVersion:   ">=1.0.0"`},
		{name: "whitespace only", version: "   ", want: `EngineVersion:   ">=1.0.0"`},
		{name: "explicit is left alone", version: ">=2.4.0", want: `EngineVersion:   ">=2.4.0"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := collectionscaffold.Generate(collectionscaffold.Config{Name: "probe.engine", EngineVersion: tt.version})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !strings.Contains(string(files[0].Content), tt.want) {
				t.Errorf("generated manifest does not contain %q:\n%s", tt.want, files[0].Content)
			}
		})
	}
}

// TestGenerate_PromptsForReversibility proves the generated file asks the
// question collection.Register will otherwise enforce with a panic.
//
// Register refuses an implemented method that declares itself not
// reversible with no reason. A stub is exempt, so nothing forces the
// author to think about it until they flip Status, at which point the
// binary fails at start and they go read pkg/collection to find out why.
// Putting the question where the answer goes moves that discovery to
// authoring time.
func TestGenerate_PromptsForReversibility(t *testing.T) {
	files, err := collectionscaffold.Generate(collectionscaffold.Config{Name: "probe.reversibility"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := string(files[0].Content)

	for _, want := range []string{
		"Reversibility",
		"StatusImplemented",
		"sdk.RecordInverse",
		"observe or reconstruct",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the generated stub does not mention %q, so an author has no prompt to answer it:\n%s", want, got)
		}
	}

	// Commented out rather than emitted live: an uncommented
	// Reversibility{} on a declared stub would be a real answer to a
	// question nobody has considered, and a false one is worse than an
	// absent one.
	if strings.Contains(got, "\n\t\t\tReversibility: collection.Reversibility{") {
		t.Error("Reversibility is emitted as live code; it must stay commented until an author answers it")
	}
}
