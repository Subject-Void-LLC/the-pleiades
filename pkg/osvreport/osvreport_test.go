// Tests for reading OSV-Scanner's report.
package osvreport_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/osvreport"
)

// TestParse_ARealReport reads the report OSV-Scanner 2.6.0 itself wrote
// for a lockfile holding left-pad 1.3.0, scanned against a local database
// with one advisory: critical, fixed in 1.3.1.
func TestParse_ARealReport(t *testing.T) {
	data, err := os.ReadFile("testdata/report-2.6.0.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := osvreport.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	f := findings[0]
	if f.Package != "left-pad" || f.Version != "1.3.0" || f.Ecosystem != "npm" || f.ID != "GHSA-test-0001" ||
		f.Score != "9.8" || f.Rating != osvreport.Critical || strings.Join(f.Fixed, ",") != "1.3.1" ||
		f.Source != "/src/package-lock.json" || f.Summary != "a test advisory" {
		t.Errorf("finding = %+v", f)
	}
	if names := strings.Join(f.Names(), ","); names != "GHSA-test-0001,CVE-2026-99999,GHSA-test-0001" {
		t.Errorf("names = %s", names)
	}
}

// TestParse_BinaryPackages: an operating system's binary packages built
// from one source package are told apart by their own names.
func TestParse_BinaryPackages(t *testing.T) {
	report := `{"results":[{"source":{"path":"/lib/apk/db/installed"},"packages":[
{"package":{"name":"busybox","os_package_name":"ssl_client","version":"1.36.1-r31","ecosystem":"Alpine:v3.20"},"groups":[{"ids":["A-1"],"max_severity":"9.8"}]},
{"package":{"name":"busybox","os_package_name":"busybox","version":"1.36.1-r31","ecosystem":"Alpine:v3.20"},"groups":[{"ids":["A-1"],"max_severity":"9.8"}]}]}]}`
	findings, err := osvreport.Parse([]byte(report))
	if err != nil || len(findings) != 2 {
		t.Fatalf("Parse = %+v, %v", findings, err)
	}
	if got := findings[0].Label(); got != "Alpine:v3.20 busybox (ssl_client)@1.36.1-r31" {
		t.Errorf("a binary package's label = %s", got)
	}
	if got := findings[1].Label(); got != "Alpine:v3.20 busybox@1.36.1-r31" {
		t.Errorf("the source package's label = %s", got)
	}
}

// TestParse_GroupsFixesAndRefusals: a group's fixed versions are the ones
// its advisories name for the package's own ecosystem; an unscored group
// is unknown; a report that is not OSV-Scanner's, or that it cannot read
// whole, is refused.
func TestParse_GroupsFixesAndRefusals(t *testing.T) {
	report := `{"results":[{"source":{"path":"/src/go.mod"},"packages":[{"package":{"name":"x","version":"1.0.0","ecosystem":"Go"},
"groups":[{"ids":["GO-1"],"aliases":["CVE-1"],"max_severity":""},{"ids":["GO-2"],"max_severity":"5.3"}],
"vulnerabilities":[
 {"id":"GO-1","affected":[{"package":{"ecosystem":"Go","name":"x"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"1.0.2"},{"introduced":"1.1.0"},{"fixed":"1.1.3"}]}]},
                          {"package":{"ecosystem":"npm","name":"x"},"ranges":[{"events":[{"fixed":"9.9.9"}]}]}]},
 {"id":"GO-2","affected":[{"package":{"ecosystem":"Go","name":"x"},"ranges":[{"events":[{"introduced":"0"}]}]}]}]}]}]}`
	findings, err := osvreport.Parse([]byte(report))
	if err != nil || len(findings) != 2 {
		t.Fatalf("Parse = %+v, %v", findings, err)
	}
	if f := findings[0]; f.Rating != osvreport.Unknown || strings.Join(f.Fixed, ",") != "1.0.2,1.1.3" {
		t.Errorf("GO-1 = %+v", f)
	}
	if f := findings[1]; f.Rating != osvreport.Medium || len(f.Fixed) != 0 {
		t.Errorf("GO-2 = %+v", f)
	}
	for src, want := range map[string]string{
		`{"experimental_config":{}}`: "has no results",
		`not json`:                   "is not OSV-Scanner's JSON",
		`{"results":[{"packages":[{"package":{"name":""}}]}]}`:                          "a package with no name",
		`{"results":[{"packages":[{"package":{"name":"x"},"groups":[{"ids":[]}]}]}]}`:   "no id",
		`{"results":[{"packages":[{"package":{"name":"x"},"groups":[{"ids":[""]}]}]}]}`: "no id",
		`{"results":[]} {"results":[]}`:                                                 "more than one JSON document",
	} {
		if _, err := osvreport.Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, want)
		}
	}
	if findings, err := osvreport.Parse([]byte(`{"results":[]}`)); err != nil || len(findings) != 0 {
		t.Errorf("an empty report = %v, %v", findings, err)
	}
}

// TestRatings maps scores at each boundary.
func TestRatings(t *testing.T) {
	for score, want := range map[string]osvreport.Rating{
		"9.0": osvreport.Critical, "8.9": osvreport.High, "7.0": osvreport.High, "6.9": osvreport.Medium,
		"4.0": osvreport.Medium, "3.9": osvreport.Low, "0.1": osvreport.Low, "0": osvreport.Unknown,
		"": osvreport.Unknown, "high": osvreport.Unknown, "11": osvreport.Unknown, "NaN": osvreport.Unknown,
	} {
		if got := osvreport.RatingOf(score); got != want {
			t.Errorf("RatingOf(%q) = %s, want %s", score, got, want)
		}
	}
	for _, name := range []string{"low", "medium", "high", "critical"} {
		if r, err := osvreport.ParseRating(name); err != nil || r.String() != name {
			t.Errorf("ParseRating(%s) = %s, %v", name, r, err)
		}
	}
	if _, err := osvreport.ParseRating("severe"); err == nil {
		t.Error("ParseRating accepted severe")
	}
}

// TestParse_ARefusalBoundsWhatTheReportSays: a refusal names the report's
// path and package bounded and escaped, however long they are and
// whatever they hold.
func TestParse_ARefusalBoundsWhatTheReportSays(t *testing.T) {
	long := "/src/\x1b[2J" + strings.Repeat("p", 100000)
	for name, report := range map[string]string{
		"a package with no name": `{"results":[{"source":{"path":"` + jsonEscape(long) + `"},"packages":[{"package":{"name":""}}]}]}`,
		"a group with no id":     `{"results":[{"source":{"path":"/src/x"},"packages":[{"package":{"name":"` + jsonEscape(long) + `"},"groups":[{"ids":[]}]}]}]}`,
	} {
		_, err := osvreport.Parse([]byte(report))
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
		if len(err.Error()) > 1024 || strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("%s: a refusal of %d bytes, escape held: %v", name, len(err.Error()), strings.ContainsRune(err.Error(), 0x1b))
		}
	}
}

// jsonEscape is s as the inside of a JSON string.
func jsonEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\x1b", `\u001b`).Replace(s)
}
