// Tests for pkg/docedit: what an edit keeps, what it refuses.
package docedit

import (
	"strings"
	"testing"
)

func mustPath(t *testing.T, raw any) []Segment {
	t.Helper()
	p, err := ParsePath(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestJSON_EditKeepsTheRest: setting one key leaves every other key in
// its place, numbers and strings as written, and the indent the file used.
func TestJSON_EditKeepsTheRest(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"two spaces": {"{\n  \"z\": 1,\n  \"id\": 12345678901234567890,\n  \"s\": \"caf\\u00e9 <b>\",\n  \"server\": {\n    \"port\": 80\n  }\n}\n",
			"{\n  \"z\": 1,\n  \"id\": 12345678901234567890,\n  \"s\": \"caf\\u00e9 <b>\",\n  \"server\": {\n    \"port\": 8443\n  }\n}\n"},
		"tabs":    {"{\n\t\"server\": {\n\t\t\"port\": 80\n\t}\n}", "{\n\t\"server\": {\n\t\t\"port\": 8443\n\t}\n}"},
		"compact": {`{"server":{"port":80},"a":[1,2]}`, `{"server":{"port":8443},"a":[1,2]}`},
	} {
		t.Run(name, func(t *testing.T) {
			v, f, err := ParseJSON([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			nv, _ := FromAny(8443)
			if err := v.Set(mustPath(t, "server.port"), nv); err != nil {
				t.Fatal(err)
			}
			if got := string(EncodeJSON(v, f)); got != tc.want {
				t.Errorf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// TestJSON_SetDeleteAndPaths: a new nested key is created at the end, an
// index appends, a key holding a dot is reached by a list, and a delete
// reports whether there was anything.
func TestJSON_SetDeleteAndPaths(t *testing.T) {
	v, f, err := ParseJSON([]byte(`{"a":{"b.c":1},"list":[{"n":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	set := func(path any, x any) {
		t.Helper()
		nv, err := FromAny(x)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Set(mustPath(t, path), nv); err != nil {
			t.Fatal(err)
		}
	}
	set([]any{"a", "b.c"}, 2)
	set("new.deep", map[string]any{"y": true, "x": nil})
	set("list.1", "appended")
	set("list.0.n", 5)
	want := `{"a":{"b.c":2},"list":[{"n":5},"appended"],"new":{"deep":{"x":null,"y":true}}}`
	if got := string(EncodeJSON(v, f)); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if ok, err := v.Delete(mustPath(t, "list.1")); !ok || err != nil {
		t.Errorf("delete: %v %v", ok, err)
	}
	if ok, _ := v.Delete(mustPath(t, "absent.key")); ok {
		t.Error("deleted what was not there")
	}
	for _, bad := range []any{"list.5", "list.0.n.x", "list.x"} {
		nv, _ := FromAny(1)
		if err := v.Set(mustPath(t, bad), nv); err == nil {
			t.Errorf("set %v was accepted", bad)
		}
	}
}

// TestJSON_Refuses: what two readers read differently, and what is not a
// document.
func TestJSON_Refuses(t *testing.T) {
	for _, bad := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":1}}`, `{"a":1} x`, "{\"a\":\"\xff\"}", `{"a":01}`, `[1,]`, `{"a"}`, strings.Repeat("[", 70) + strings.Repeat("]", 70), ""} {
		if _, _, err := ParseJSON([]byte(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, bad := range []any{"", "a..b", ".a", []any{}, []any{1.5}, []any{-1}, 3} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("path %v was accepted", bad)
		}
	}
}

// TestYAML_EditKeepsComments: comments and order survive an edit, and the
// replaced value keeps its line comment.
func TestYAML_EditKeepsComments(t *testing.T) {
	in := "# the service\nname: web   # its name\nserver:\n    port: 80   # the port\n    host: 0.0.0.0\nz: last\n"
	d, err := ParseYAML([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := ValueNode(8443)
	if err := d.Set(mustPath(t, "server.port"), n); err != nil {
		t.Fatal(err)
	}
	out, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# the service", "name: web # its name", "    port: 8443 # the port", "    host: 0.0.0.0", "z: last"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("%q is not in\n%s", want, out)
		}
	}
	if strings.Index(string(out), "name:") > strings.Index(string(out), "z:") {
		t.Error("the order changed")
	}
}

// TestYAML_Refuses: an alias on the path, a stream of documents and a
// duplicate key.
func TestYAML_Refuses(t *testing.T) {
	d, err := ParseYAML([]byte("base: &b {port: 80}\nweb: *b\n"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := ValueNode(1)
	if err := d.Set(mustPath(t, "web.port"), n); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Errorf("through an alias: %v", err)
	}
	if err := d.Set(mustPath(t, "web"), n); err == nil {
		t.Error("an alias was replaced")
	}
	if _, err := ParseYAML([]byte("a: 1\n---\nb: 2\n")); err == nil {
		t.Error("two documents were accepted")
	}
	if _, err := ParseYAML([]byte("a: 1\na: 2\n")); err == nil {
		t.Error("a duplicate key was accepted")
	}
}

// TestYAMLText_IgnoresComments: a value and the same value with a comment
// read the same, so setting a commented key to its own value is no change.
func TestYAMLText_IgnoresComments(t *testing.T) {
	d, err := ParseYAML([]byte("a: 4  # kept\n"))
	if err != nil {
		t.Fatal(err)
	}
	old, _, _ := d.Get(mustPath(t, "a"))
	n, _ := ValueNode(4)
	if YAMLText(old) != YAMLText(n) || old.LineComment == "" {
		t.Errorf("%q != %q, or the comment was lost from the document", YAMLText(old), YAMLText(n))
	}
}
