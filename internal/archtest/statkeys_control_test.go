// The control for TestEveryRecordedKeyIsDeclared's scan: a package written
// in each shape a real Collection records a key in, so a scan that stopped
// seeing one shape fails here rather than passing the rule by finding
// nothing.
package archtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// controlSource records one key in each shape the scan follows, and one it
// cannot name.
const controlSource = `package control

const statDirect = "direct"

type stat struct {
	key   string
	value any
}

type shape struct{}

func (shape) view() map[string]any { return map[string]any{"from_method": 1} }

func recordStats(rc ctx, stats map[string]any) error {
	for k, v := range stats {
		_ = rc.SetStat(k, v)
	}
	return nil
}

func describe() map[string]any {
	out := map[string]any{"from_producer": 1}
	out["from_index"] = 2
	return out
}

func run(rc ctx, s shape, dynamic string) {
	_ = rc.SetStat(statDirect, 1)
	_ = rc.EmitFact("fact", 1)
	for k, v := range map[string]any{"ranged_map": 1} {
		_ = rc.SetStat(k, v)
	}
	for _, k := range []string{"ranged_slice"} {
		_ = rc.SetStat(k, nil)
	}
	for _, st := range []stat{{"struct_positional", 1}, {key: "struct_named", value: 2}} {
		_ = rc.SetStat(st.key, st.value)
	}
	_ = recordStats(rc, map[string]any{"helper_literal": 1})
	stats := map[string]any{"helper_var": 1}
	stats["helper_index"] = 2
	_ = recordStats(rc, stats)
	_ = recordStats(rc, describe())
	for k, v := range s.view() {
		_ = rc.SetStat(k, v)
	}
	_ = rc.SetStat(dynamic, 1)
}
`

// TestStatKeyScan_Control: every shape's key is found, and the key named at
// run time is counted as dynamic rather than silently dropped.
func TestStatKeyScan_Control(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "control.go"), []byte(controlSource), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := newConstScope(t, dir, "example.invalid/control")
	scan := scope.scanPackage(dir)
	var got []string
	for k := range scan.keys {
		got = append(got, k)
	}
	slices.Sort(got)
	want := []string{"direct", "fact", "from_index", "from_method", "from_producer", "helper_index", "helper_literal", "helper_var",
		"ranged_map", "ranged_slice", "struct_named", "struct_positional"}
	if !slices.Equal(got, want) {
		t.Errorf("scan found %s\nwant %s", strings.Join(got, ", "), strings.Join(want, ", "))
	}
	if len(scan.dynamicAt) != 1 {
		t.Errorf("dynamic record calls = %v, want the one keyed by a parameter", scan.dynamicAt)
	}
}
