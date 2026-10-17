// Fuzzing for the SBOM reader.
package sbomcheck_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sbomcheck"
)

// FuzzParse holds Parse, Validate and SetSubjectSupplier to their
// contract on any input: they return or refuse without panicking, and a
// document an edit writes parses again.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"syft-1.52.0-dir.spdx.json", "syft-1.52.0-image.spdx.json"} {
		if data, err := os.ReadFile("testdata/" + name); err == nil {
			f.Add(data)
		}
	}
	f.Add([]byte(`{"spdxVersion":"SPDX-2.3","relationships":[{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"r"}],"packages":[{"SPDXID":"r"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := sbomcheck.Parse(data)
		if err != nil {
			return
		}
		_ = d.Validate()
		out, err := d.SetSubjectSupplier("Organization: x", "pleiades")
		if err != nil {
			return
		}
		if _, err := sbomcheck.Parse(out); err != nil {
			t.Fatalf("an edited document does not parse: %v", err)
		}
	})
}
