// Command gendocs's JSON Schema of the pr-check report, the contract a
// dashboard or an agent reads instead of pr-check's console output.
package main

import (
	"path/filepath"
	"reflect"

	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// prReportModelFiles are the Go files whose doc comments describe the
// report and the records it holds.
var prReportModelFiles = []string{
	"tools/internal/prreport/report.go",
	"tools/internal/flakegate/run.go",
	"tools/internal/flakegate/report.go",
	"tools/internal/oscheck/report.go",
}

// generatePRReportSchema writes the JSON Schema of the report make
// pr-check leaves in .coverage/pr/records/report.json.
func generatePRReportSchema(outDir string) error {
	schema, err := prReportSchema(".")
	if err != nil {
		return err
	}
	return writeDocsSchema(outDir, "pr-check-report.json", schema)
}

// prReportSchema builds the report's schema from prreport.Report, its
// descriptions read from prReportModelFiles under root. It is open where
// the contract is: an object may carry a property it does not declare, and
// a field's known values are examples, not an enum, since a newer producer
// of the same major version may add either.
func prReportSchema(root string) (map[string]any, error) {
	files := make([]string, 0, len(prReportModelFiles))
	for _, file := range prReportModelFiles {
		files = append(files, filepath.Join(root, file))
	}
	docs, err := jsonschema.FilesDocs(files...)
	if err != nil {
		return nil, err
	}
	s := &jsonschema.Builder{Docs: docs, Defs: map[string]any{}, Source: "the pr-check report's model files", Open: true, AbsentNotNull: true,
		Known: map[string][]string{
			"Report.Kind":        {prreport.ReportKind},
			"Report.Verdict":     prreport.KnownReportVerdicts,
			"Problem.Code":       prreport.KnownProblemCodes,
			"Step.Kind":          {prreport.StepKind},
			"Step.Verdict":       prreport.KnownStepVerdicts,
			"Run.Kind":           {flakegate.RunKind},
			"Run.Verdict":        prreport.KnownRunVerdicts,
			"RunFailure.Kind":    prreport.KnownFailureKinds,
			"RunFailure.Outcome": prreport.KnownOutcomes,
			"LegRecord.Kind":     {oscheck.LegKind},
			"LegRecord.Verdict":  prreport.KnownRunVerdicts,
			"LegRecord.Step":     prreport.KnownLegSteps,
		}}
	return s.Root(reflect.TypeFor[prreport.Report](), "Pleiades pr-check report")
}
