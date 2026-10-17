// Fuzzing for the report reader.
package osvreport_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/osvreport"
)

// FuzzParse holds Parse to its contract on any input: it returns or
// refuses without panicking, and every finding names a package and an id
// and carries a rating it can name.
func FuzzParse(f *testing.F) {
	if real, err := os.ReadFile("testdata/report-2.6.0.json"); err == nil {
		f.Add(real)
	}
	f.Add([]byte(`{"results":[]}`))
	f.Add([]byte(`{"results":[{"packages":[{"package":{"name":"x"},"groups":[{"ids":["A"],"max_severity":"7"}],"vulnerabilities":[{"id":"A","affected":[{"package":{"name":"x"},"ranges":[{"events":[{"fixed":"2"}]}]}]}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		findings, err := osvreport.Parse(data)
		if err != nil {
			return
		}
		for _, x := range findings {
			if x.Package == "" || x.ID == "" || x.Rating.String() == "" {
				t.Fatalf("a finding Parse returned is incomplete: %+v", x)
			}
		}
	})
}
