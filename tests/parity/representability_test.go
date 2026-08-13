package parity_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tests/parity"
)

// updateGaps rewrites the committed report instead of asserting against
// it, the standard golden-file affordance. The report is committed so that
// a change quietly dropping a field somebody had represented fails rather
// than regenerating in silence, which is the same ratchet docs-gen-check
// applies to generated reference pages.
var updateGaps = flag.Bool("update-gaps", false, "rewrite GAPS.md from the current classification")

const reportPath = "GAPS.md"

// corpus loads one object type, deriving the survey questions from the
// survey documents that contain them.
func corpus(t *testing.T, object parity.ObjectType) map[string]parity.Export {
	t.Helper()

	switch object {
	case parity.SurveyQuestions:
		return parity.Explode(corpus(t, parity.SurveySpecs), "spec")
	case parity.JobTemplateSummary:
		return parity.Nested(corpus(t, parity.JobTemplates), "summary_fields")
	case parity.JobTemplateRelated:
		return parity.Nested(corpus(t, parity.JobTemplates), "related")
	}
	exports, err := parity.LoadCorpus(parity.CorpusRoot, object)
	if err != nil {
		t.Fatalf("loading the %s corpus: %v", object, err)
	}
	return exports
}

// classify indexes one object type's table.
func classify(t *testing.T, object parity.ObjectType) parity.Classification {
	t.Helper()

	fields, ok := parity.Tables()[object]
	if !ok {
		t.Fatalf("no classification table is registered for %s", object)
	}
	index, err := parity.Index(fields)
	if err != nil {
		t.Fatalf("indexing the %s classification: %v", object, err)
	}
	return index
}

// TestClassification_IsInternallyCoherent checks every table before it is
// compared to anything: each status carries what that status requires.
//
// It runs first because a table asserting "convertible" with no stated
// conversion, or "gap" owned by no phase, produces a report that reads as
// analysis and contains none.
func TestClassification_IsInternallyCoherent(t *testing.T) {
	for _, object := range parity.ObjectTypes() {
		t.Run(string(object), func(t *testing.T) {
			for _, problem := range classify(t, object).Validate() {
				t.Error(problem)
			}
		})
	}
}

// TestCorpus_EveryFieldIsClassified is the ratchet.
//
// A field present in a real AWX export that our table does not mention is
// a field nobody has decided about, and it fails the build. That is the
// entire mechanism this package exists to provide: the arrival of an
// unhandled AWX field becomes a test failure rather than a silence that
// somebody notices in a screenshot eighteen months later.
func TestCorpus_EveryFieldIsClassified(t *testing.T) {
	for _, object := range parity.ObjectTypes() {
		t.Run(string(object), func(t *testing.T) {
			exports := corpus(t, object)
			index := classify(t, object)

			if missing := index.Unclassified(exports); len(missing) > 0 {
				t.Errorf("the %s corpus carries %d field(s) nothing has classified: %s\n\n"+
					"Add each to the %s table with a status. A field is not handled because\n"+
					"it is obvious; it is handled because somebody wrote down which of\n"+
					"represent, convert, defer or refuse applies to it.",
					object, len(missing), strings.Join(missing, ", "), object)
			}

			// Not a failure: a table may legitimately classify a field
			// from AWX's documentation before an example reaches the
			// corpus. Reported so entries nobody can check against real
			// data stay visible.
			if stale := index.Stale(exports); len(stale) > 0 {
				t.Logf("classified but absent from the %s corpus (%d): %s",
					object, len(stale), strings.Join(stale, ", "))
			}
		})
	}
}

// TestCorpus_LoadsAndIsNotEmpty guards the assertion above from passing
// vacuously. An empty corpus classifies everything perfectly.
func TestCorpus_LoadsAndIsNotEmpty(t *testing.T) {
	for _, object := range parity.ObjectTypes() {
		t.Run(string(object), func(t *testing.T) {
			exports := corpus(t, object)

			objects := 0
			for _, export := range exports {
				objects += len(export.Results)
			}
			if objects == 0 {
				t.Fatalf("the %s corpus contains no objects, so every representability assertion for it is vacuous", object)
			}
			t.Logf("%d object(s), %d distinct field(s)", objects, len(parity.FieldNames(exports)))
		})
	}
}

// TestGapReport_MatchesTheCommittedOne renders the report and compares it
// to the committed copy, so the backlog is a tracked artifact rather than
// something regenerated on demand and never read.
func TestGapReport_MatchesTheCommittedOne(t *testing.T) {
	sets := map[parity.ObjectType]parity.Classification{}
	exports := map[parity.ObjectType]map[string]parity.Export{}
	for _, object := range parity.ObjectTypes() {
		sets[object] = classify(t, object)
		exports[object] = corpus(t, object)
	}
	rendered := parity.RenderReport(sets, exports)

	if *updateGaps {
		if err := os.WriteFile(reportPath, []byte(rendered), 0o600); err != nil {
			t.Fatalf("writing %s: %v", reportPath, err)
		}
		t.Logf("rewrote %s", reportPath)
		return
	}

	committed, err := os.ReadFile(filepath.Clean(reportPath))
	if err != nil {
		t.Fatalf("reading %s (run: go test ./tests/parity/ -update-gaps): %v", reportPath, err)
	}
	if string(committed) != rendered {
		t.Errorf("%s is out of date with the classification.\n"+
			"Regenerate and commit it:\n\n    go test ./tests/parity/ -update-gaps\n", reportPath)
	}
}

// TestRepresentability_ReportsTheHonestFraction fails on nothing and
// exists to put the numbers in the test log, because "how much of a real
// AWX deployment can we hold" is the question the whole roadmap is
// answering and it should be visible without opening a file.
func TestRepresentability_ReportsTheHonestFraction(t *testing.T) {
	var carriedAll, totalAll int

	for _, object := range parity.ObjectTypes() {
		counts := classify(t, object).Counts()

		// Metadata is excluded from both halves: it is AWX's REST
		// envelope, and counting it either way would flatter or punish
		// the number for something no import would ever carry.
		carried := counts[parity.Represented] + counts[parity.Convertible]
		total := carried + counts[parity.Gap] + counts[parity.Unsupported]
		carriedAll += carried
		totalAll += total

		t.Logf("%-16s %2d/%2d carried (%d direct, %d converted), %d gaps, %d unsupported",
			object, carried, total, counts[parity.Represented], counts[parity.Convertible],
			counts[parity.Gap], counts[parity.Unsupported])
	}

	t.Logf("across every object type: %d of %d meaningful fields can be carried", carriedAll, totalAll)
}
