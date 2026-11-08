// Tests of the skip check (skips.go): a file gosec could not type-check is
// never read as having no findings.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSkips_ASkipIsNeverAPass: gosec's whole-run warning fails, a
// skipped file no waiver names fails, a waiver gosec did not need fails as
// stale, and only a run whose every skip is named passes.
func TestCheckSkips_ASkipIsNeverAPass(t *testing.T) {
	const root = "/repo"
	fixture := []gosecError{{Line: 3, Column: 2, Error: "could not import example.com/scan/helper"}}
	named := []skipWaiver{{File: "tools/fixture/local.go", Reason: "a deliberately unbuildable fixture"}}
	for _, tc := range []struct {
		name   string
		errs   map[string][]gosecError
		skips  []skipWaiver
		failed bool
		want   string
	}{
		{name: "nothing skipped", errs: nil, failed: false},
		{name: "every skip named", errs: map[string][]gosecError{"/repo/tools/fixture/local.go": fixture}, skips: named, failed: false},
		{name: "a toolchain gosec was not built with", errs: map[string][]gosecError{gosecGlobalKey: {{Error: "This application uses version go1.26 of the source-processing packages but runs version go1.27 of 'go list'."}}}, failed: true, want: "Rebuild it with this toolchain"},
		{name: "a skipped file no waiver names", errs: map[string][]gosecError{"/repo/cmd/controller/admin.go": {{Error: "could not import context"}}}, failed: true, want: "cmd/controller/admin.go: could not import context"},
		{name: "a waiver gosec did not need", errs: nil, skips: named, failed: true, want: "name a file gosec type-checked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := checkSkips(tc.errs, tc.skips, root)
			if v.failed() != tc.failed {
				t.Fatalf("failed = %v, want %v: %+v", v.failed(), tc.failed, v)
			}
			if tc.want != "" && !strings.Contains(v.report(20), tc.want) {
				t.Errorf("the report does not say %q:\n%s", tc.want, v.report(20))
			}
		})
	}
}

// TestCheckSkips_ReportsAThousandSkipsInBrief: the run that found this
// skipped 1,345 packages; the report names the first files and how many more.
func TestCheckSkips_ReportsAThousandSkipsInBrief(t *testing.T) {
	errs := map[string][]gosecError{}
	for i := 0; i < 1345; i++ {
		errs[filepath.Join("/repo", "pkg", strings.Repeat("a", 1+i%7), "f"+string(rune('a'+i%26))+".go")+string(rune('0'+i%10))] = []gosecError{{Error: "cannot decode export data"}}
	}
	report := checkSkips(errs, nil, "/repo").report(20)
	if !strings.Contains(report, "and ") || !strings.Contains(report, "more") || strings.Count(report, "cannot decode") > 20 {
		t.Errorf("the report is not brief:\n%s", report)
	}
}

// TestLoadWaivers_ASkipWithoutAReasonIsRefused: an exemption must say why,
// as a finding's waiver does.
func TestLoadWaivers_ASkipWithoutAReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gosec-waivers.json")
	if err := os.WriteFile(path, []byte(`{"waivers": [], "skips": [{"file": "x.go"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadWaivers(path); err == nil {
		t.Fatal("a skip waiver with no reason was accepted")
	}
	if err := os.WriteFile(path, []byte(`{"waivers": [], "skips": [{"file": "x.go", "reason": "fixture"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, skips, err := loadWaivers(path); err != nil || len(skips) != 1 {
		t.Fatalf("a skip waiver with a reason: %v, %v", skips, err)
	}
}
