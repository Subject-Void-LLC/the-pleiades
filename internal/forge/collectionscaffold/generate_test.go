package collectionscaffold_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
				"SupportsCheck: false,",
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
			name:    "unknown transport rejected",
			cfg:     collectionscaffold.Config{Name: "pkg.apt.install", Transports: []string{"shh"}},
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
// nothing, and every manifest in this catalog declares the same value. A
// generated one that declared nothing left a reader unable to tell "no
// opinion" from "forgot".
//
// The expected value is read from DefaultEngineVersion rather than written
// out, because the literal is the release line and moves with it. Writing
// it twice is how the old ">=1.0.0" survived long enough to become a
// constraint no release could have met.
func TestGenerate_DefaultsTheEngineVersion(t *testing.T) {
	defaulted := fmt.Sprintf(`EngineVersion:   %q`, collectionscaffold.DefaultEngineVersion)
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "unset", version: "", want: defaulted},
		{name: "whitespace only", version: "   ", want: defaulted},
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

// TestGenerate_DeclaresNoCheckSupport proves the generated manifest states
// its check support outright, as false, and tells the implementer how to
// change that.
//
// False is the only value a declared stub can carry: collection.Register
// refuses check support on a method that is not implemented. Stating it
// rather than leaving the zero value in place is what gives the comment
// beside it somewhere to live, so the author meets the check contract
// (read, predict, change nothing, never record an inverse) in the file
// they are already editing rather than after a refused registration.
func TestGenerate_DeclaresNoCheckSupport(t *testing.T) {
	tests := []struct {
		name string
		cfg  collectionscaffold.Config
	}{
		{name: "two segment name", cfg: collectionscaffold.Config{Name: "probe.dryrun"}},
		{name: "three segment name with capabilities", cfg: collectionscaffold.Config{
			Name:         "probe.nested.dryrun",
			Capabilities: []capability.Name{capability.NameSSHTransport},
			Transports:   []string{"ssh"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := collectionscaffold.Generate(tt.cfg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			source := string(files[0].Content)

			// A live field, not a comment: the value has to reach the
			// registered manifest, where Register and every serialized
			// manifest read it.
			if !regexp.MustCompile(`\n\t\t\tSupportsCheck:\s+false,\n`).MatchString(source) {
				t.Errorf("the generated manifest does not state SupportsCheck: false as a live field:\n%s", source)
			}
			if regexp.MustCompile(`SupportsCheck:\s+true`).MatchString(source) {
				t.Errorf("a declared stub claims check support, which collection.Register refuses:\n%s", source)
			}
			// The Descriptor must not carry a live Check either, since
			// Register refuses a Check without SupportsCheck.
			if strings.Contains(source, "\n\t\tCheck:") {
				t.Errorf("the generated Descriptor sets Check on a declared stub:\n%s", source)
			}

			for _, want := range []string{
				"SupportsCheck to true",
				"Check takes exactly Invoke's",
				"Result.Changed",
				"changing nothing",
				"sdk.RecordInverse",
				"unchecked",
			} {
				if !strings.Contains(source, want) {
					t.Errorf("the generated stub does not mention %q, so an author has no prompt for check support:\n%s", want, source)
				}
			}

			// The starter test must pin the same answer, so a later edit
			// that turns check support on without a Check is caught by
			// the package's own test rather than only at registration.
			testSource := string(files[1].Content)
			if !strings.Contains(testSource, "d.Manifest.SupportsCheck || d.Check != nil") {
				t.Errorf("the generated starter test does not assert the absence of check support:\n%s", testSource)
			}
		})
	}
}

// TestGenerate_RefusesAReservedParam proves a scaffold never writes a
// method declaring the engine's device selector as its own parameter:
// collection.Register would refuse it at process start, which for a
// generated file means every binary importing it panics.
func TestGenerate_RefusesAReservedParam(t *testing.T) {
	for _, name := range collection.ReservedParams() {
		cfg := collectionscaffold.Config{Name: "probe.reserved", Doc: collection.Doc{
			Summary: "Probe.",
			Params:  []collection.Param{{Name: name, Type: "string", Description: "the datastore"}},
		}}
		files, err := collectionscaffold.Generate(cfg)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("Generate(param %q) = %d files, %v; want it refused naming the parameter", name, len(files), err)
		}
	}
}

// TestGenerate_StatesTheExecutionContext: a scaffolded method is born
// stating where it runs and whether it acts on a device, since
// internal/archtest requires every built-in to (Phase 117a). An empty
// config states target-side with a device required; an optional device
// also gets a DeviceCall stub, without which collection.Register refuses it.
func TestGenerate_StatesTheExecutionContext(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cfg        collectionscaffold.Config
		want       []string
		deviceCall bool
	}{
		{"defaults", collectionscaffold.Config{Name: "demo.target"}, []string{"collection.SiteTarget", "collection.DeviceRequired"}, false},
		{"controller, none", collectionscaffold.Config{Name: "demo.notify", Site: collection.SiteController, Device: collection.DeviceNone}, []string{"collection.SiteController", "collection.DeviceNone"}, false},
		{"controller, optional", collectionscaffold.Config{Name: "demo.api", Site: collection.SiteController, Device: collection.DeviceOptional}, []string{"collection.SiteController", "collection.DeviceOptional"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := collectionscaffold.Generate(tc.cfg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(files[0].Content), want) || !strings.Contains(string(files[1].Content), want) {
					t.Errorf("the generated method or its test does not state %s", want)
				}
			}
			if got := strings.Contains(string(files[0].Content), "DeviceCall:"); got != tc.deviceCall {
				t.Errorf("DeviceCall stub present = %v, want %v", got, tc.deviceCall)
			}
		})
	}
}

// TestGenerate_RefusesAnIncoherentExecutionContext: what collection.Register
// would refuse at process start is refused before a file is written.
func TestGenerate_RefusesAnIncoherentExecutionContext(t *testing.T) {
	for _, cfg := range []collectionscaffold.Config{
		{Name: "demo.a", Site: "local"},
		{Name: "demo.b", Device: "sometimes"},
		{Name: "demo.c", Device: collection.DeviceNone},
		{Name: "demo.d", Site: collection.SiteTarget, Device: collection.DeviceOptional},
	} {
		if _, err := collectionscaffold.Generate(cfg); err == nil {
			t.Errorf("Generate(%+v) accepted an execution context Register would refuse", cfg)
		}
	}
}
