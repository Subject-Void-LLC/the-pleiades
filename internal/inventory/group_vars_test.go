// Tests that a static inventory's group_vars and host_vars are folded into
// each device's vars property in the documented order, refuse what they must,
// and are never written back into hosts.yaml.
package inventory_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// writeTree writes files (path relative to dir to content) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// hostsFile is an inventory with one host, web1, tagged b_tier and a_site,
// and an extra properties block to append.
func hostsFile(extra string) string {
	return "hosts:\n  - id: h1\n    name: web1\n    type: linux_server\n    tags: [b_tier, a_site]\n    properties:\n      host: 10.0.0.1\n" + extra
}

// loadVars loads web1 through the real file repository and returns its vars.
func loadVars(t *testing.T, dir string) (map[string]any, error) {
	t.Helper()
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	item, err := repo.GetByName(context.Background(), "web1")
	if err != nil {
		return nil, err
	}
	vars, _ := item.Properties().Raw()[inventory.VarsProperty].(map[string]any)
	return vars, nil
}

// TestFoldedVars_Order: all, then tags alphabetically, then the host's file,
// then the host's own properties.vars; the later layer wins a clashing key.
func TestFoldedVars_Order(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		inv.DefaultInventoryFilename: hostsFile("      vars: {own: row, level: row}\n"),
		"group_vars/all.yml":         "level: all\nonly_all: 1\nclash: all\n",
		"group_vars/a_site.yaml":     "level: a_site\nclash: a_site\nonly_a: true\n",
		"group_vars/b_tier/one.yml":  "level: b_tier\nclash: b_tier\n",
		"group_vars/b_tier/two.yaml": "two: 2\n",
		"group_vars/b_tier/skip.txt": "level: ignored\n",
		"group_vars/unused.yml":      "level: unused\n",
		"host_vars/web1.yml":         "level: host\nhost_only: x\nown: file\n",
	})
	got, err := loadVars(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"level": "row", "own": "row", // properties.vars beats host_vars
		"only_all": 1, "clash": "b_tier", // b_tier sorts after a_site, so it wins
		"only_a": true, "two": 2, "host_only": "x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("vars\n got %v\nwant %v", got, want)
	}
}

// TestFoldedVars_NoFilesNoProperty: a project with no vars files keeps the
// property absent, so nothing changes for it.
func TestFoldedVars_NoFilesNoProperty(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{inv.DefaultInventoryFilename: hostsFile("")})
	got, err := loadVars(t, dir)
	if err != nil || got != nil {
		t.Fatalf("vars %v, err %v", got, err)
	}
}

// TestFoldedVars_Refusals: each bad file is an error naming the file and the
// key, never a silent drop.
func TestFoldedVars_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"bad name", map[string]string{"group_vars/all.yml": "bad-name: 1\n"}, []string{"all.yml", "bad-name"}},
		{"secret name", map[string]string{"host_vars/web1.yml": "api_password: x\n"}, []string{"web1.yml", "api_password"}},
		{"secret shaped value", map[string]string{"host_vars/web1.yml": "banner: AKIAIOSFODNN7EXAMPLE\n"}, []string{"web1.yml", "banner"}},
		{"nested secret", map[string]string{"host_vars/web1.yml": "cfg:\n  - AKIAIOSFODNN7EXAMPLE\n"}, []string{"cfg"}},
		{"not a mapping", map[string]string{"group_vars/a_site.yml": "- one\n- two\n"}, []string{"a_site.yml"}},
		{"two forms", map[string]string{"host_vars/web1.yml": "a: 1\n", "host_vars/web1.yaml": "a: 2\n"}, []string{"web1.yml", "web1.yaml"}},
		{"inline not a mapping", nil, []string{"must be a mapping"}},
		{"inline bad name", nil, []string{"1x"}},
	}
	inline := map[string]string{"inline not a mapping": "      vars: [1]\n", "inline bad name": "      vars: {1x: y}\n"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{inv.DefaultInventoryFilename: hostsFile(inline[tc.name])}
			for k, v := range tc.files {
				files[k] = v
			}
			writeTree(t, dir, files)
			_, err := loadVars(t, dir)
			if err == nil {
				t.Fatal("loaded without an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

// TestFoldedVars_SaveDoesNotBakeThemIn: saving a host after an unrelated
// change leaves hosts.yaml without the files' variables, and keeps the row's
// own properties.vars.
func TestFoldedVars_SaveDoesNotBakeThemIn(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		inv.DefaultInventoryFilename: hostsFile("      vars: {own: row}\n"),
		"group_vars/all.yml":         "from_group: g\n",
		"host_vars/web1.yml":         "from_host: h\n",
	})
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	ctx := context.Background()
	item, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if err := item.AddInfo("rack", "r4", true); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, inv.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "from_group") || strings.Contains(text, "from_host") {
		t.Errorf("folded variables were written into hosts.yaml:\n%s", text)
	}
	if !strings.Contains(text, "own: row") || !strings.Contains(text, "rack: r4") {
		t.Errorf("the row's own content was lost:\n%s", text)
	}
	again, err := loadVars(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if again["from_group"] != "g" || again["from_host"] != "h" || again["own"] != "row" {
		t.Errorf("vars after save: %v", again)
	}
}

// TestFoldedVars_RefusesALinkAndANestedSecretKey: a vars file that is a
// symbolic link is never followed, and a secret-named key inside a value is
// refused as a top-level one is.
func TestFoldedVars_RefusesALinkAndANestedSecretKey(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		inv.DefaultInventoryFilename: hostsFile(""),
		"group_vars/a_site.yml":      "db:\n  host: x\n  password: plain\n",
	})
	if _, err := loadVars(t, dir); err == nil || !strings.Contains(err.Error(), "named like a secret") {
		t.Errorf("a nested secret key: %v", err)
	}

	link := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yml")
	writeTree(t, filepath.Dir(outside), map[string]string{"outside.yml": "x: 1\n"})
	writeTree(t, link, map[string]string{inv.DefaultInventoryFilename: hostsFile(""), "group_vars/placeholder.txt": ""})
	if err := os.Symlink(outside, filepath.Join(link, "group_vars", "all.yml")); err != nil {
		t.Skip("symbolic links are not available here")
	}
	if _, err := loadVars(t, link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a link was followed: %v", err)
	}

	many := t.TempDir()
	files := map[string]string{inv.DefaultInventoryFilename: hostsFile("")}
	for i := 0; i < 70; i++ {
		files[fmt.Sprintf("group_vars/a_site/f%02d.yml", i)] = fmt.Sprintf("v%d: 1\n", i)
	}
	writeTree(t, many, files)
	if _, err := loadVars(t, many); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("too many files: %v", err)
	}
}
