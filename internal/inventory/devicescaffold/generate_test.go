package devicescaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name       string
		cfg        devicescaffold.Config
		wantErr    bool
		wantSource []string // substrings that must appear in the generated source file
	}{
		{
			name: "cisco-shaped, mirrors the real hand-written package",
			cfg: devicescaffold.Config{
				Vendor:       "juniper",
				TypeKey:      "junos_router",
				Capabilities: []capability.Name{capability.NameSSHTransport, capability.NameJunos},
			},
			wantSource: []string{
				"package juniper",
				`record.RegisterType("junos_router", NewRouter)`,
				"type Router struct",
				"var _ inventory.InventoryItem = (*Router)(nil)",
				"func NewRouter(rec record.Record)",
				`capability.Name("SSHTransportCapable")`,
				`capability.Name("JunosCapable")`,
				"func (j *Router) HasCapability(name capability.Name) bool",
			},
		},
		{
			name: "no underscore in type key",
			cfg: devicescaffold.Config{
				Vendor:  "acme",
				TypeKey: "widget",
			},
			wantSource: []string{
				"package acme",
				`record.RegisterType("widget", NewWidget)`,
				"type Widget struct",
				"var _ inventory.InventoryItem = (*Widget)(nil)",
			},
		},
		{
			name: "zero capabilities still produces valid source",
			cfg: devicescaffold.Config{
				Vendor:  "acme",
				TypeKey: "acme_box",
			},
			wantSource: []string{
				"[]capability.Name{",
			},
		},
		{
			name:    "invalid vendor rejected",
			cfg:     devicescaffold.Config{Vendor: "..", TypeKey: "acme_box"},
			wantErr: true,
		},
		{
			name:    "invalid type key rejected",
			cfg:     devicescaffold.Config{Vendor: "acme", TypeKey: "../etc"},
			wantErr: true,
		},
		{
			name:    "type key ending in underscore rejected (empty Kind)",
			cfg:     devicescaffold.Config{Vendor: "acme", TypeKey: "acme_"},
			wantErr: true,
		},
		{
			name:    "unknown capability rejected",
			cfg:     devicescaffold.Config{Vendor: "acme", TypeKey: "acme_box", Capabilities: []capability.Name{"NotARealCapability"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := devicescaffold.Generate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Generate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(files) != 2 {
				t.Fatalf("Generate() returned %d files, want 2", len(files))
			}

			var sourceFile, testFile *devicescaffold.GeneratedFile
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

			wantBase := "internal/inventory/devices/" + tt.cfg.Vendor + "/"
			if !strings.HasPrefix(sourceFile.Path, wantBase) {
				t.Errorf("source path = %q, want prefix %q", sourceFile.Path, wantBase)
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

func TestGenerate_KindWithNoUnderscore(t *testing.T) {
	files, err := devicescaffold.Generate(devicescaffold.Config{Vendor: "acme", TypeKey: "gateway"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if files[0].Path != "internal/inventory/devices/acme/gateway.go" {
		t.Errorf("source path = %q, want %q", files[0].Path, "internal/inventory/devices/acme/gateway.go")
	}
}
