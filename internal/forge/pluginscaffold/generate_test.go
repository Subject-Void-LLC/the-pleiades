package pluginscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/pluginscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// parseGenerated parses content as Go source, failing the test with the
// offending source attached. Every accepted case below goes through this
// rather than string-matching, so a template change that produces
// syntactically invalid Go fails here rather than the next time someone
// runs the generator for real.
func parseGenerated(t *testing.T, path string, content []byte) *token.FileSet {
	t.Helper()

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, path, content, parser.AllErrors); err != nil {
		t.Fatalf("generated %s is not valid Go: %v\n---\n%s", path, err, content)
	}
	return fset
}

// TestGenerate covers the accepted configurations and the shape of what
// comes out.
func TestGenerate(t *testing.T) {
	tests := []struct {
		name         string
		cfg          pluginscaffold.Config
		wantPaths    []string
		wantContains []string
	}{
		{
			name: "multi-word name collapses to one package word",
			cfg: pluginscaffold.Config{
				Name:        "catalyst_center",
				Description: "reads devices from a Cisco Catalyst Center",
				Endpoint:    "https://sandboxdnac.cisco.com",
				ReadOnly:    true,
			},
			wantPaths: []string{
				"internal/inventory/plugins/catalystcenter/catalystcenter.go",
				"internal/inventory/plugins/catalystcenter/catalystcenter_test.go",
			},
			wantContains: []string{
				"package catalystcenter",
				`const Name = "catalyst_center"`,
				"type CatalystCenter struct",
				"ReadOnly: true",
				`Endpoint: "https://sandboxdnac.cisco.com"`,
				"syncplugin.StatusDeclared",
				"RequiresCredentials: false",
				"func(deps syncplugin.Deps) syncplugin.Plugin { return New(deps) }",
				"var _ syncplugin.Plugin = (*CatalystCenter)(nil)",
			},
		},
		{
			// The wiring half: a plugin declaring a credential store and a
			// per-deployment setting must come out of the generator already
			// reading both, since a declaration written later than the code
			// that reads it is a declaration that starts out wrong.
			name: "a plugin that needs credentials and a setting",
			cfg: pluginscaffold.Config{
				Name:                "cloudthing",
				Description:         "reads hosts from a cloud provider",
				RequiresCredentials: true,
				Settings: []syncplugin.SettingSpec{{
					Name:        "region",
					Description: "the provider region to read from",
					Required:    true,
				}},
			},
			wantPaths: []string{
				"internal/inventory/plugins/cloudthing/cloudthing.go",
				"internal/inventory/plugins/cloudthing/cloudthing_test.go",
			},
			wantContains: []string{
				"RequiresCredentials: true",
				"Settings: []syncplugin.SettingSpec{",
				`Name:        "region"`,
				`Description: "the provider region to read from"`,
				"Required:    true",
				"creds credential.Store",
				"creds: deps.Credentials,",
				`cfg.Setting("region")`,
				`"github.com/Subject-Void-LLC/the-pleiades/internal/credential"`,
			},
		},
		{
			name: "single word name",
			cfg: pluginscaffold.Config{
				Name:        "netbox",
				Description: "reads devices from a NetBox instance",
			},
			wantPaths: []string{
				"internal/inventory/plugins/netbox/netbox.go",
				"internal/inventory/plugins/netbox/netbox_test.go",
			},
			wantContains: []string{
				"package netbox",
				"type Netbox struct",
				"ReadOnly: false",
				`Endpoint: ""`,
			},
		},
		{
			name: "file endpoint is accepted",
			cfg: pluginscaffold.Config{
				Name:        "local_file",
				Description: "reads a local document",
				Endpoint:    "file:///srv/hosts.yaml",
			},
			wantPaths: []string{
				"internal/inventory/plugins/localfile/localfile.go",
				"internal/inventory/plugins/localfile/localfile_test.go",
			},
			wantContains: []string{"package localfile"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := pluginscaffold.Generate(tt.cfg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(files) != len(tt.wantPaths) {
				t.Fatalf("Generate produced %d files, want %d", len(files), len(tt.wantPaths))
			}

			for i, want := range tt.wantPaths {
				if files[i].Path != want {
					t.Errorf("file %d path = %q, want %q", i, files[i].Path, want)
				}
				parseGenerated(t, files[i].Path, files[i].Content)
			}

			source := string(files[0].Content)
			for _, want := range tt.wantContains {
				if !strings.Contains(source, want) {
					t.Errorf("generated source does not contain %q\n---\n%s", want, source)
				}
			}
		})
	}
}

// TestGenerate_Rejects covers every input the generator must refuse. These
// are the path-traversal and unbuildable-identifier cases genutil exists
// for, exercised through this generator's own entry point rather than
// assumed to be covered because genutil has its own tests.
func TestGenerate_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		cfg     pluginscaffold.Config
		wantErr string
	}{
		{
			name:    "empty name",
			cfg:     pluginscaffold.Config{Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "path traversal",
			cfg:     pluginscaffold.Config{Name: "../../etc", Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "dotted name is not a namespace here",
			cfg:     pluginscaffold.Config{Name: "net.catalyst", Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "go keyword",
			cfg:     pluginscaffold.Config{Name: "package", Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "leading digit",
			cfg:     pluginscaffold.Config{Name: "9lives", Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "uppercase",
			cfg:     pluginscaffold.Config{Name: "NetBox", Description: "x"},
			wantErr: "invalid plugin name",
		},
		{
			name:    "missing description",
			cfg:     pluginscaffold.Config{Name: "netbox"},
			wantErr: "has no description",
		},
		{
			// A setting nobody can name is not a setting, and the
			// generated descriptor would declare an empty key nothing can
			// ever match a --set value to.
			name: "setting with no name",
			cfg: pluginscaffold.Config{
				Name:        "netbox",
				Description: "x",
				Settings:    []syncplugin.SettingSpec{{Description: "d"}},
			},
			wantErr: "invalid setting name",
		},
		{
			name: "setting name is not a valid segment",
			cfg: pluginscaffold.Config{
				Name:        "netbox",
				Description: "x",
				Settings:    []syncplugin.SettingSpec{{Name: "My-Region", Description: "d"}},
			},
			wantErr: "invalid setting name",
		},
		{
			// A required setting an operator cannot look up the meaning of
			// is a value they have to read source to supply, which is the
			// state that made the AWS region undiscoverable.
			name: "setting with no description",
			cfg: pluginscaffold.Config{
				Name:        "netbox",
				Description: "x",
				Settings:    []syncplugin.SettingSpec{{Name: "region"}},
			},
			wantErr: "no description",
		},
		{
			name:    "whitespace-only description",
			cfg:     pluginscaffold.Config{Name: "netbox", Description: "   "},
			wantErr: "has no description",
		},
		{
			name:    "endpoint with no scheme",
			cfg:     pluginscaffold.Config{Name: "netbox", Description: "x", Endpoint: "netbox.example.com"},
			wantErr: "endpoint scheme must be",
		},
		{
			name:    "endpoint with an unsupported scheme",
			cfg:     pluginscaffold.Config{Name: "netbox", Description: "x", Endpoint: "ftp://netbox.example.com"},
			wantErr: "endpoint scheme must be",
		},
		{
			name:    "https endpoint with no host",
			cfg:     pluginscaffold.Config{Name: "netbox", Description: "x", Endpoint: "https:///path"},
			wantErr: "endpoint has no host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pluginscaffold.Generate(tt.cfg)
			if err == nil {
				t.Fatalf("Generate accepted %+v, want an error containing %q", tt.cfg, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Generate error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestGenerate_IsDeterministic proves two runs of the same config produce
// byte-identical output. tools/gencatalog regenerates the whole catalog on
// every run and the result is expected to leave no diff, so
// nondeterminism here would show up as unexplainable churn there.
func TestGenerate_IsDeterministic(t *testing.T) {
	cfg := pluginscaffold.Config{
		Name:        "catalyst_center",
		Description: "reads devices from a Cisco Catalyst Center",
		Endpoint:    "https://sandboxdnac.cisco.com",
		ReadOnly:    true,
	}

	first, err := pluginscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	second, err := pluginscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}

	for i := range first {
		if first[i].Path != second[i].Path {
			t.Errorf("file %d path differs between runs: %q then %q", i, first[i].Path, second[i].Path)
		}
		if string(first[i].Content) != string(second[i].Content) {
			t.Errorf("file %d content differs between runs", i)
		}
	}
}

// TestConfig_Accessors pins the name derivations, which the composition
// root and tools/gencatalog both depend on to compute import paths.
func TestConfig_Accessors(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantPackage string
		wantType    string
		wantImport  string
	}{
		{
			name:        "multi word",
			in:          "catalyst_center",
			wantPackage: "catalystcenter",
			wantType:    "CatalystCenter",
			wantImport:  "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/catalystcenter",
		},
		{
			name:        "single word",
			in:          "netbox",
			wantPackage: "netbox",
			wantType:    "Netbox",
			wantImport:  "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/netbox",
		},
		{
			name:        "three words",
			in:          "acme_widget_controller",
			wantPackage: "acmewidgetcontroller",
			wantType:    "AcmeWidgetController",
			wantImport:  "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/acmewidgetcontroller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := pluginscaffold.Config{Name: tt.in, Description: "x"}
			if got := cfg.PackageName(); got != tt.wantPackage {
				t.Errorf("PackageName() = %q, want %q", got, tt.wantPackage)
			}
			if got := cfg.TypeName(); got != tt.wantType {
				t.Errorf("TypeName() = %q, want %q", got, tt.wantType)
			}
			if got := cfg.ImportPath(); got != tt.wantImport {
				t.Errorf("ImportPath() = %q, want %q", got, tt.wantImport)
			}
		})
	}
}
