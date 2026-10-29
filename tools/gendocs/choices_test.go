// Tests that the generated module reference prints every closed set a
// parameter declares (Phase 131, R2), from the real registry.
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestWriteParameters_PrintsChoices: a parameter's row ends with its choices,
// each as code, marked when its method ignores case, a fragment's set
// included; a parameter with none gets no such note.
func TestWriteParameters_PrintsChoices(t *testing.T) {
	for _, tc := range []struct {
		fqcn  string
		want  []string
		never string
	}{
		{"net.ovs.bridge", []string{"**Choices:** `present`, `absent`.", "**Choices:** `system`, `netdev`."}, ""},
		{"net.netconf.config", []string{"**Choices:** `never`, `always`, `if_supported` (case ignored)."}, ""},
		{"container.run", []string{"**Choices:** `missing`, `never`."}, ""},
		{"net.catalyst.device_facts", nil, "**Choices:**"},
	} {
		t.Run(tc.fqcn, func(t *testing.T) {
			desc, ok := collection.Lookup(tc.fqcn)
			if !ok {
				t.Fatalf("%s is not registered", tc.fqcn)
			}
			var b strings.Builder
			writeParameters(&b, desc.Manifest.Doc)
			page := b.String()
			for _, w := range tc.want {
				if !strings.Contains(page, w) {
					t.Errorf("%s's parameters lack %q:\n%s", tc.fqcn, w, page)
				}
			}
			if tc.never != "" && strings.Contains(page, tc.never) {
				t.Errorf("%s declares no set, but its parameters print one:\n%s", tc.fqcn, page)
			}
		})
	}
}
