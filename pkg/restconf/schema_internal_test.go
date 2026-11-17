// Package restconf: SchemaRef against a base URL holding a path, which New
// refuses today and httpapi.Join supports: a location's path from the
// origin's root is sent relative to the base path, never joined under it
// twice, and one outside the base path is refused.
package restconf

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

func TestSchemaRef_ABasePathIsJoinedOnce(t *testing.T) {
	base, err := url.Parse("https://gw.example/r1")
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{base: base}
	for location, want := range map[string]string{
		"https://gw.example/r1/yang/m@2024-01-01.yang": "/r1/yang/m@2024-01-01.yang",
		"/r1/restconf/tailf/modules/m/2024-01-01":      "/r1/restconf/tailf/modules/m/2024-01-01",
		"yang/m.yang":           "/r1/yang/m.yang",
		"https://gw.example/r1": "/r1/",
	} {
		ref, err := c.SchemaRef(location)
		if err != nil {
			t.Errorf("SchemaRef(%q): %v", location, err)
			continue
		}
		joined, err := httpapi.Join(base, ref)
		if err != nil || joined.EscapedPath() != want {
			t.Errorf("SchemaRef(%q) = %q, joined to %v (%v); want the request at %s", location, ref, joined, err, want)
		}
	}
	for _, location := range []string{"https://gw.example/r2/yang/m.yang", "/r10/yang/m.yang", "https://gw.example/"} {
		if ref, err := c.SchemaRef(location); err == nil || !strings.Contains(err.Error(), "outside the device's base path /r1") {
			t.Errorf("SchemaRef(%q) = %q, %v; want a refusal naming the base path", location, ref, err)
		}
	}
	if _, err := New("https://gw.example/r1", nil, "none", false, nil); err == nil {
		t.Error("New took a base URL with a path; SchemaRef's base-path rule is then reached through it, and needs a test there")
	}
}
