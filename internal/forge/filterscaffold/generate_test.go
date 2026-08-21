package filterscaffold_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/filterscaffold"
)

func simpleConfig() filterscaffold.Config {
	return filterscaffold.Config{
		GoName:   "CIDRToNetmask",
		CELName:  "cidrToNetmask",
		Category: "network",
		Summary:  "converts a CIDR prefix length to its dotted-decimal netmask.",
		Params:   []filterscaffold.Param{{Name: "cidr", GoType: "string"}},
		Return:   filterscaffold.Return{GoType: "string"},
	}
}

func TestGenerate(t *testing.T) {
	tests := []struct {
		name       string
		cfg        filterscaffold.Config
		wantErr    string
		wantSource string // substring expected in the impl file
	}{
		{
			name:       "unary string to string",
			cfg:        simpleConfig(),
			wantSource: "func CIDRToNetmask(cidr string) string {",
		},
		{
			name: "binary with acronym-heavy name",
			cfg: filterscaffold.Config{
				GoName:   "IPToInt",
				CELName:  "ipToInt",
				Category: "network",
				Summary:  "converts a dotted-decimal IPv4 address to its 32-bit integer form.",
				Params:   []filterscaffold.Param{{Name: "ip", GoType: "string"}},
				Return:   filterscaffold.Return{GoType: "int"},
			},
			wantSource: "func IPToInt(ip string) int {",
		},
		{
			name: "two params",
			cfg: filterscaffold.Config{
				GoName:   "SubnetSplit",
				CELName:  "subnetSplit",
				Category: "network",
				Summary:  "splits a CIDR block into subnets of the given new prefix length.",
				Params: []filterscaffold.Param{
					{Name: "cidr", GoType: "string"},
					{Name: "newPrefix", GoType: "int"},
				},
				Return: filterscaffold.Return{GoType: "[]string", CELType: "cel.ListType(cel.StringType)"},
			},
			wantSource: "func SubnetSplit(cidr string, newPrefix int) []string {",
		},
		{
			name: "empty GoName",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.GoName = ""
				return c
			}(),
			wantErr: "invalid GoName",
		},
		{
			name: "lowercase GoName rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.GoName = "cidrToNetmask"
				return c
			}(),
			wantErr: "invalid GoName",
		},
		{
			name: "GoName with dot rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.GoName = "CIDR.ToNetmask"
				return c
			}(),
			wantErr: "invalid GoName",
		},
		{
			name: "CELName with underscore rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.CELName = "cidr_to_netmask"
				return c
			}(),
			wantErr: "invalid CELName",
		},
		{
			name: "uppercase CELName rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.CELName = "CidrToNetmask"
				return c
			}(),
			wantErr: "invalid CELName",
		},
		{
			name: "empty Category rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.Category = ""
				return c
			}(),
			wantErr: "invalid Category",
		},
		{
			name: "path traversal in Category rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.Category = "../../etc"
				return c
			}(),
			wantErr: "invalid Category",
		},
		{
			name: "empty Summary rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.Summary = "   "
				return c
			}(),
			wantErr: "no Summary",
		},
		{
			name: "empty Return.GoType rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.Return.GoType = ""
				return c
			}(),
			wantErr: "no Return.GoType",
		},
		{
			name: "unknown param GoType with no explicit CELType rejected",
			cfg: filterscaffold.Config{
				GoName:   "Foo",
				CELName:  "foo",
				Category: "network",
				Summary:  "x.",
				Params:   []filterscaffold.Param{{Name: "counts", GoType: "[]int"}},
				Return:   filterscaffold.Return{GoType: "string"},
			},
			wantErr: "not well-known",
		},
		{
			name: "unknown param GoType with explicit CELType accepted",
			cfg: filterscaffold.Config{
				GoName:   "Foo",
				CELName:  "foo",
				Category: "network",
				Summary:  "x.",
				Params:   []filterscaffold.Param{{Name: "counts", GoType: "[]int", CELType: "cel.ListType(cel.IntType)"}},
				Return:   filterscaffold.Return{GoType: "string"},
			},
			wantSource: "func Foo(counts []int) string {",
		},
		{
			name: "well-known []string param needs no explicit CELType",
			cfg: filterscaffold.Config{
				GoName:   "Supernet",
				CELName:  "supernet",
				Category: "network",
				Summary:  "computes the smallest CIDR block containing every given CIDR.",
				Params:   []filterscaffold.Param{{Name: "cidrs", GoType: "[]string"}},
				Return:   filterscaffold.Return{GoType: "string"},
			},
			wantSource: "func Supernet(cidrs []string) string {",
		},
		{
			name: "well-known map[string]any param and return need no explicit CELType",
			cfg: filterscaffold.Config{
				GoName:   "DeepMerge",
				CELName:  "deepMerge",
				Category: "structured",
				Summary:  "recursively merges b into a.",
				Params: []filterscaffold.Param{
					{Name: "a", GoType: "map[string]any"},
					{Name: "b", GoType: "map[string]any"},
				},
				Return: filterscaffold.Return{GoType: "map[string]any"},
			},
			wantSource: "func DeepMerge(a map[string]any, b map[string]any) map[string]any {",
		},
		{
			name: "well-known []any and []map[string]any need no explicit CELType",
			cfg: filterscaffold.Config{
				GoName:   "Pluck",
				CELName:  "pluck",
				Category: "structured",
				Summary:  "extracts one key's value across a list of maps.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]map[string]any"},
					{Name: "key", GoType: "string"},
				},
				Return: filterscaffold.Return{GoType: "[]any"},
			},
			wantSource: "func Pluck(list []map[string]any, key string) []any {",
		},
		{
			name: "well-known []byte param and return need no explicit CELType",
			cfg: filterscaffold.Config{
				GoName:   "GzipCompress",
				CELName:  "gzipCompress",
				Category: "fileio",
				Summary:  "compresses content with gzip, returning the raw compressed bytes.",
				Params:   []filterscaffold.Param{{Name: "content", GoType: "string"}},
				Return:   filterscaffold.Return{GoType: "[]byte"},
			},
			wantSource: "func GzipCompress(content string) []byte {",
		},
		{
			name: "zero-param filter",
			cfg: filterscaffold.Config{
				GoName:   "GenerateUUIDv4",
				CELName:  "generateUUIDv4",
				Category: "structured",
				Summary:  "generates a random version-4 UUID.",
				Params:   nil,
				Return:   filterscaffold.Return{GoType: "string"},
			},
			wantSource: "func GenerateUUIDv4() string {",
		},
		{
			name: "well-known any param needs no explicit CELType",
			cfg: filterscaffold.Config{
				GoName:   "ListContains",
				CELName:  "listContains",
				Category: "structured",
				Summary:  "reports whether list contains value.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]any"},
					{Name: "value", GoType: "any"},
				},
				Return: filterscaffold.Return{GoType: "bool"},
			},
			wantSource: "func ListContains(list []any, value any) bool {",
		},
		{
			name: "three params",
			cfg: filterscaffold.Config{
				GoName:   "FilterListByKV",
				CELName:  "filterListByKV",
				Category: "structured",
				Summary:  "keeps only the maps in list whose key equals value.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]map[string]any"},
					{Name: "key", GoType: "string"},
					{Name: "value", GoType: "any"},
				},
				Return: filterscaffold.Return{GoType: "[]map[string]any"},
			},
			wantSource: "func FilterListByKV(list []map[string]any, key string, value any) []map[string]any {",
		},
		{
			name: "duplicate param names rejected",
			cfg: filterscaffold.Config{
				GoName:   "Foo",
				CELName:  "foo",
				Category: "network",
				Summary:  "x.",
				Params: []filterscaffold.Param{
					{Name: "a", GoType: "string"},
					{Name: "a", GoType: "int"},
				},
				Return: filterscaffold.Return{GoType: "string"},
			},
			wantErr: "duplicate param name",
		},
		{
			name: "invalid param name rejected",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.Params = []filterscaffold.Param{{Name: "Cidr", GoType: "string"}}
				return c
			}(),
			wantErr: "invalid Name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files, err := filterscaffold.Generate(tc.cfg)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Generate(%+v): expected error containing %q, got nil", tc.cfg, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Generate(%+v): error %q does not contain %q", tc.cfg, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Generate(%+v): unexpected error: %v", tc.cfg, err)
			}
			if len(files) != 2 {
				t.Fatalf("Generate(%+v): got %d files, want 2", tc.cfg, len(files))
			}
			for _, f := range files {
				if !strings.HasPrefix(f.Path, "pkg/filters/") {
					t.Errorf("file path %q does not start with pkg/filters/", f.Path)
				}
				fset := token.NewFileSet()
				if _, err := parser.ParseFile(fset, f.Path, f.Content, parser.AllErrors); err != nil {
					t.Errorf("generated %s does not parse as Go: %v\n---\n%s", f.Path, err, f.Content)
				}
			}
			implFile := files[0] // Generate always returns [impl, test] in that order.
			if tc.wantSource != "" && !strings.Contains(string(implFile.Content), tc.wantSource) {
				t.Errorf("impl file %s does not contain %q:\n%s", implFile.Path, tc.wantSource, implFile.Content)
			}
		})
	}
}

// TestGenerate_IsDeterministic proves two Generate(cfg) calls with the
// identical input produce byte-identical output, the same load-bearing
// property pluginscaffold's own TestGenerate_IsDeterministic protects:
// a filter scaffold consumed by a bulk regeneration tool later would
// otherwise show unexplainable diff churn.
func TestGenerate_IsDeterministic(t *testing.T) {
	cfg := simpleConfig()
	first, err := filterscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := filterscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("file count differs across identical calls: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Path != second[i].Path {
			t.Errorf("path %d differs: %q vs %q", i, first[i].Path, second[i].Path)
		}
		if string(first[i].Content) != string(second[i].Content) {
			t.Errorf("content of %s differs across identical calls", first[i].Path)
		}
	}
}

// TestGenerate_FileNamesDeriveFromCELName proves the generated file
// names come from CELName (snake_cased), not GoName, so an
// acronym-heavy Go name like "CIDRToNetmask" still produces a readable
// path ("cidr_to_netmask.go"), not "c_i_d_r_to_netmask.go" or similar.
func TestGenerate_FileNamesDeriveFromCELName(t *testing.T) {
	files, err := filterscaffold.Generate(simpleConfig())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := "pkg/filters/cidr_to_netmask.go"
	if files[0].Path != want {
		t.Errorf("impl file path = %q, want %q", files[0].Path, want)
	}
	wantTest := "pkg/filters/cidr_to_netmask_test.go"
	if files[1].Path != wantTest {
		t.Errorf("test file path = %q, want %q", files[1].Path, wantTest)
	}
}

func TestReminder(t *testing.T) {
	tests := []struct {
		name    string
		cfg     filterscaffold.Config
		wantErr string
		want    []string // substrings that must all appear
	}{
		{
			name: "unary",
			cfg:  simpleConfig(),
			want: []string{
				`cel.Function("filters.cidrToNetmask",`,
				`cel.Overload("filters_cidr_to_netmask_string_string",`,
				`[]*cel.Type{cel.StringType}, cel.StringType,`,
				`cel.UnaryBinding(cidrToNetmaskBinding)`,
				`func cidrToNetmaskBinding(arg0 ref.Val) ref.Val {`,
				`goCidr, ok := celToString(arg0)`,
				`return types.String(filters.CIDRToNetmask(goCidr))`,
			},
		},
		{
			name: "binary",
			cfg: filterscaffold.Config{
				GoName:   "SubnetSplit",
				CELName:  "subnetSplit",
				Category: "network",
				Summary:  "splits a CIDR block into subnets.",
				Params: []filterscaffold.Param{
					{Name: "cidr", GoType: "string"},
					{Name: "newPrefix", GoType: "int"},
				},
				Return: filterscaffold.Return{GoType: "[]string"},
			},
			want: []string{
				`cel.BinaryBinding(subnetSplitBinding)`,
				`func subnetSplitBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {`,
				`goNewPrefix, ok := celToInt(arg1)`,
				`return wrapStringList(filters.SubnetSplit(goCidr, goNewPrefix))`,
			},
		},
		{
			name: "still-unknown type falls back to a TODO conversion",
			cfg: filterscaffold.Config{
				GoName:   "Foo",
				CELName:  "foo",
				Category: "network",
				Summary:  "x.",
				Params:   []filterscaffold.Param{{Name: "counts", GoType: "[]int", CELType: "cel.ListType(cel.IntType)"}},
				Return:   filterscaffold.Return{GoType: "[]int", CELType: "cel.ListType(cel.IntType)"},
			},
			want: []string{
				`// TODO: convert counts (arg0 ref.Val) to Go []int; no known conversion for this type.`,
				`// TODO: wrap the []int result back into a ref.Val`,
			},
		},
		{
			name: "map[string]any param and return",
			cfg: filterscaffold.Config{
				GoName:   "DeepMerge",
				CELName:  "deepMerge",
				Category: "structured",
				Summary:  "recursively merges b into a.",
				Params: []filterscaffold.Param{
					{Name: "a", GoType: "map[string]any"},
					{Name: "b", GoType: "map[string]any"},
				},
				Return: filterscaffold.Return{GoType: "map[string]any"},
			},
			want: []string{
				`[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.MapType(cel.StringType, cel.DynType)}, cel.MapType(cel.StringType, cel.DynType),`,
				`goA, ok := celToMap(arg0)`,
				`goB, ok := celToMap(arg1)`,
				`return wrapMap(filters.DeepMerge(goA, goB))`,
			},
		},
		{
			name: "[]any and []map[string]any",
			cfg: filterscaffold.Config{
				GoName:   "Pluck",
				CELName:  "pluck",
				Category: "structured",
				Summary:  "extracts one key's value across a list of maps.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]map[string]any"},
					{Name: "key", GoType: "string"},
				},
				Return: filterscaffold.Return{GoType: "[]any"},
			},
			want: []string{
				`goList, ok := celToMapList(arg0)`,
				`goKey, ok := celToString(arg1)`,
				`return wrapDynList(filters.Pluck(goList, goKey))`,
			},
		},
		{
			name: "[]byte param and return use celToBytes/wrapBytes",
			cfg: filterscaffold.Config{
				GoName:   "GzipCompress",
				CELName:  "gzipCompress",
				Category: "fileio",
				Summary:  "compresses content with gzip, returning the raw compressed bytes.",
				Params:   []filterscaffold.Param{{Name: "content", GoType: "string"}},
				Return:   filterscaffold.Return{GoType: "[]byte"},
			},
			want: []string{
				`[]*cel.Type{cel.StringType}, cel.BytesType,`,
				`cel.UnaryBinding(gzipCompressBinding)`,
				`goContent, ok := celToString(arg0)`,
				`return wrapBytes(filters.GzipCompress(goContent))`,
			},
		},
		{
			name: "zero-param filter uses cel.FunctionBinding",
			cfg: filterscaffold.Config{
				GoName:   "GenerateUUIDv4",
				CELName:  "generateUUIDv4",
				Category: "structured",
				Summary:  "generates a random version-4 UUID.",
				Return:   filterscaffold.Return{GoType: "string"},
			},
			want: []string{
				`cel.FunctionBinding(generateUUIDv4Binding)`,
				`func generateUUIDv4Binding(_ ...ref.Val) ref.Val {`,
				`return types.String(filters.GenerateUUIDv4())`,
			},
		},
		{
			name: "any param converts via celToAny",
			cfg: filterscaffold.Config{
				GoName:   "ListContains",
				CELName:  "listContains",
				Category: "structured",
				Summary:  "reports whether list contains value.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]any"},
					{Name: "value", GoType: "any"},
				},
				Return: filterscaffold.Return{GoType: "bool"},
			},
			want: []string{
				`[]*cel.Type{cel.ListType(cel.DynType), cel.DynType}, cel.BoolType,`,
				`cel.BinaryBinding(listContainsBinding)`,
				`goList, ok := celToDynList(arg0)`,
				`goValue, ok := celToAny(arg1)`,
				`return types.Bool(filters.ListContains(goList, goValue))`,
			},
		},
		{
			name: "three-or-more params uses a real variadic cel.FunctionBinding",
			cfg: filterscaffold.Config{
				GoName:   "FilterListByKV",
				CELName:  "filterListByKV",
				Category: "structured",
				Summary:  "keeps only the maps in list whose key equals value.",
				Params: []filterscaffold.Param{
					{Name: "list", GoType: "[]map[string]any"},
					{Name: "key", GoType: "string"},
					{Name: "value", GoType: "any"},
				},
				Return: filterscaffold.Return{GoType: "[]map[string]any"},
			},
			want: []string{
				`cel.FunctionBinding(filterListByKVBinding)`,
				`func filterListByKVBinding(args ...ref.Val) ref.Val {`,
				`if len(args) != 3 {`,
				`return types.NewErr("filters.filterListByKV: expected 3 arguments, got %d", len(args))`,
				`goList, ok := celToMapList(args[0])`,
				`goKey, ok := celToString(args[1])`,
				`goValue, ok := celToAny(args[2])`,
				`return wrapMapList(filters.FilterListByKV(goList, goKey, goValue))`,
			},
		},
		{
			name: "invalid config surfaces the same error Generate would",
			cfg: func() filterscaffold.Config {
				c := simpleConfig()
				c.GoName = "lowercase"
				return c
			}(),
			wantErr: "invalid GoName",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := filterscaffold.Reminder(tc.cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Reminder(%+v): got err %v, want containing %q", tc.cfg, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reminder(%+v): unexpected error: %v", tc.cfg, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Reminder(%+v) missing %q in:\n%s", tc.cfg, want, got)
				}
			}
		})
	}
}

// TestGenerate_FileNamesForAcronymHeavyNames is the regression test for a
// real defect this generator's own first real use against Phase 51
// surfaced: camelToSnake used to insert an underscore before every
// uppercase letter, mangling an acronym-heavy CEL name letter by letter
// ("classifyIP" became "classify_i_p"). It must treat a run of uppercase
// runes as one acronym instead.
func TestGenerate_FileNamesForAcronymHeavyNames(t *testing.T) {
	tests := []struct {
		goName, celName, wantFile string
	}{
		{"ClassifyIP", "classifyIP", "pkg/filters/classify_ip.go"},
		{"MACOUI", "macOUI", "pkg/filters/mac_oui.go"},
		{"ValidateVLAN", "validateVLAN", "pkg/filters/validate_vlan.go"},
		{"IsCiscoReservedVLAN", "isCiscoReservedVLAN", "pkg/filters/is_cisco_reserved_vlan.go"},
		{"ValidateASN", "validateASN", "pkg/filters/validate_asn.go"},
		{"HostnameToFQDN", "hostnameToFQDN", "pkg/filters/hostname_to_fqdn.go"},
		{"URLDomain", "urlDomain", "pkg/filters/url_domain.go"},
	}
	for _, tc := range tests {
		t.Run(tc.celName, func(t *testing.T) {
			cfg := filterscaffold.Config{
				GoName: tc.goName, CELName: tc.celName, Category: "network",
				Summary: "x.",
				Params:  []filterscaffold.Param{{Name: "arg", GoType: "string"}},
				Return:  filterscaffold.Return{GoType: "string"},
			}
			files, err := filterscaffold.Generate(cfg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if files[0].Path != tc.wantFile {
				t.Errorf("Generate(%s).Path = %q, want %q", tc.celName, files[0].Path, tc.wantFile)
			}
		})
	}
}
