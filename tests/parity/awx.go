// Package parity measures this platform against real AWX objects.
//
// It exists because being wrong about AWX compatibility was, three times
// running, discovered by a person looking at a screenshot of a production
// Ascender instance while every package's own tests were green. Nothing in
// this repository could notice that the product is incompatible with the
// system it claims to replace, so incompatibility was found by inspection,
// at random, after the cost had already been paid
// (FAILURE_PATTERNS.md #112, #113).
//
// The corpus under testdata/ is the only external artifact in this
// repository that defines correctness rather than describing it. Each file
// is a real response body from a real AWX deployment, committed verbatim.
// A field appearing there is a field a customer actually has.
//
// The suite is deliberately tiered, so it is useful long before it can be
// satisfied:
//
//   - Tier 1, representability (this file and representability_test.go):
//     can our model hold every field without loss? Every field must be
//     classified, and an unclassified one fails the build, so the arrival
//     of a field nobody has triaged is a test failure rather than a
//     silence. The generated report is the backlog.
//   - Tier 2, import: an importer produces our objects from an export.
//     Not built.
//   - Tier 3, launchability: an imported template launches through the
//     real API and reaches the right adapter. Not built.
//
// The report this generates (GAPS.md) is committed and diffed, the same
// ratchet docs-gen-check applies, so a change that quietly drops a field
// somebody had represented fails rather than merely regenerating.
package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Status is what our model can do with one AWX field.
//
// The distinction that matters most is REPRESENTED against CONVERTIBLE,
// and it is drawn strictly: same name and same type, or it is a
// conversion. That strictness is the point. An import that has to rename
// or reshape a field is an import that can lose it, and a report calling
// such a field "represented" is the same class of comfortable inaccuracy
// this package was built to stop.
type Status string

const (
	// Represented means a declared field of ours holds the value directly:
	// same name, same type, no transformation on import.
	Represented Status = "REPRESENTED"

	// Convertible means we can hold it, but the value must be transformed
	// because our name or our type differs. Every one of these is a line
	// of importer code somebody has to write and a place the import can
	// lose meaning.
	Convertible Status = "CONVERTIBLE"

	// Gap means we have nowhere to put it. Each carries the roadmap phase
	// that owns it, so the report is a backlog rather than a complaint.
	Gap Status = "GAP"

	// Unsupported means we have decided not to support it, with the reason
	// recorded. Distinct from Gap on purpose: a reader deciding whether to
	// migrate needs to know which absences are scheduled and which are
	// permanent.
	Unsupported Status = "UNSUPPORTED"

	// Metadata means the field is AWX's REST envelope rather than template
	// data: identifiers, hyperlinks, denormalised summaries. Nothing to
	// represent, and counting it as a gap would inflate the report with
	// things no import would ever carry.
	Metadata Status = "METADATA"
)

// Represents reports whether a status describes something an import can
// carry, in either direction.
func (s Status) Represents() bool { return s == Represented || s == Convertible }

// Field is one AWX field and what we can do with it.
type Field struct {
	// Name is the AWX field name exactly as it appears on the wire.
	Name string

	Status Status

	// Ours names where the value lands: a Go field, an ent column, or a
	// launch.FieldSpec name. Empty for Gap, Unsupported and Metadata.
	Ours string

	// Conversion states exactly what an importer must do, for Convertible.
	// Required for that status: "convertible" with no stated conversion is
	// an assertion nobody can act on.
	Conversion string

	// Phase names the roadmap phase that owns a Gap, or "unowned".
	Phase string

	// Note carries the reason for Unsupported, and any caveat worth
	// keeping beside the others.
	Note string
}

// Export is one AWX response, as fetched.
//
// AWX returns two shapes and the corpus carries both: a list endpoint
// wraps its objects in count/results, while a detail endpoint such as
// survey_spec returns the object bare. Decoding handles either rather
// than asking whoever captured the response to reshape it, because a
// corpus somebody had to edit before committing is a corpus that can be
// edited wrongly.
type Export struct {
	Count   int              `json:"count"`
	Results []map[string]any `json:"results"`
}

// UnmarshalJSON accepts a wrapped list or a bare object.
func (e *Export) UnmarshalJSON(data []byte) error {
	var wrapped struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Results != nil {
		e.Count, e.Results = wrapped.Count, wrapped.Results
		return nil
	}

	var bare map[string]any
	if err := json.Unmarshal(data, &bare); err != nil {
		return err
	}
	e.Count, e.Results = 1, []map[string]any{bare}
	return nil
}

// ObjectType is one AWX API resource the corpus covers. Its value is the
// testdata subdirectory name and the AWX endpoint's own plural, so a
// reader can map a corpus file back to the request that produced it.
type ObjectType string

// The object types the corpus covers today. Each needs its own
// classification table, because "can we hold this" is a different question
// per resource, and a single flat table would let a field name shared
// between two resources (organization, timeout, description) be answered
// once and wrongly for the other.
const (
	JobTemplates    ObjectType = "job_templates"
	Projects        ObjectType = "projects"
	ProjectUpdates  ObjectType = "project_updates"
	CredentialTypes ObjectType = "credential_types"
	SurveySpecs     ObjectType = "survey_specs"
	Schedules       ObjectType = "schedules"
	ActivityStream  ObjectType = "activity_stream"

	// SurveyQuestions has no directory of its own: it is derived from
	// survey_specs by Explode, because AWX nests the questions inside the
	// document rather than serving them as a resource.
	SurveyQuestions ObjectType = "survey_questions"

	// JobTemplateSummary is derived from a job template's summary_fields
	// by Nested, and it exists because treating that block as pure
	// envelope was a real hole in this measurement.
	//
	// AWX gives a job template no root-level credentials, labels or
	// instance_groups field. Those relationships appear ONLY as
	// sub-resource URLs under related and as previews under
	// summary_fields, so a classification that dismissed summary_fields
	// as "nothing to represent" silently omitted three whole
	// relationships: every bound credential, every label, and every
	// instance group. The report then said a template was 19 of 37
	// carried without mentioning any of them, which is the same
	// flattering inaccuracy this package exists to prevent.
	JobTemplateSummary ObjectType = "job_template_summary_fields"

	// JobTemplateRelated is derived from a job template's related block.
	// Its keys are the API's own checklist of what a template connects
	// to, which is the signal that was available and unread when
	// summary_fields was dismissed.
	JobTemplateRelated ObjectType = "job_template_related"
)

// ObjectTypes is every type the corpus covers, in the order a reader
// meets them: the template first, then the things it points at.
func ObjectTypes() []ObjectType {
	return []ObjectType{
		JobTemplates, JobTemplateSummary, JobTemplateRelated, Projects, ProjectUpdates,
		CredentialTypes, SurveySpecs, SurveyQuestions, Schedules, ActivityStream,
	}
}

// Tables maps each object type to its classification.
//
// One table per resource rather than one flat table, because "can we hold
// this" is a different question per resource and several field names
// recur across them (organization, timeout, description, status). A single
// table would answer such a name once, and be wrong for every resource
// but the first.
func Tables() map[ObjectType][]Field {
	return map[ObjectType][]Field{
		JobTemplates:       JobTemplateFields,
		JobTemplateSummary: JobTemplateSummaryFields,
		JobTemplateRelated: JobTemplateRelatedFields,
		Projects:           ProjectFields,
		ProjectUpdates:     ProjectUpdateFields,
		CredentialTypes:    CredentialTypeFields,
		SurveySpecs:        SurveySpecFields,
		SurveyQuestions:    SurveyQuestionFields,
		Schedules:          ScheduleFields,
		ActivityStream:     ActivityStreamFields,
	}
}

// Nested lifts an object-valued key into the object position, so its keys
// are classified in their own right.
//
// The counterpart to Explode, for a nested MAP rather than a nested list.
// It exists for summary_fields, where AWX puts relationships that have no
// root-level field of their own.
func Nested(exports map[string]Export, key string) map[string]Export {
	out := make(map[string]Export, len(exports))
	for name, export := range exports {
		var lifted []map[string]any
		for _, object := range export.Results {
			if fields, ok := object[key].(map[string]any); ok {
				lifted = append(lifted, fields)
			}
		}
		out[name] = Export{Count: len(lifted), Results: lifted}
	}
	return out
}

// Explode turns a nested list inside each object into objects of its own.
//
// It exists for survey questions. A survey_spec's top level is three
// fields, and every mapping worth measuring lives inside its spec array,
// so classifying only the top level would report a resource as almost
// fully represented while saying nothing about the part that matters.
func Explode(exports map[string]Export, key string) map[string]Export {
	out := make(map[string]Export, len(exports))
	for name, export := range exports {
		var nested []map[string]any
		for _, object := range export.Results {
			list, ok := object[key].([]any)
			if !ok {
				continue
			}
			for _, item := range list {
				if fields, ok := item.(map[string]any); ok {
					nested = append(nested, fields)
				}
			}
		}
		out[name] = Export{Count: len(nested), Results: nested}
	}
	return out
}

// CorpusRoot is where the committed exports live, relative to this
// package.
const CorpusRoot = "testdata"

// LoadCorpus reads one object type's exports.
func LoadCorpus(root string, object ObjectType) (map[string]Export, error) {
	return LoadExports(filepath.Join(root, string(object)))
}

// LoadExports reads every corpus file under dir, returning them keyed by
// file name so a failure names the file a reader can open.
func LoadExports(dir string) (map[string]Export, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the corpus directory: %w", err)
	}

	out := make(map[string]Export, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// #nosec G304 -- path is built from a directory listing of this
		// package's own committed testdata, not from any caller input.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		var export Export
		if err := json.Unmarshal(data, &export); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", entry.Name(), err)
		}
		out[entry.Name()] = export
	}
	if len(out) == 0 {
		// An empty corpus would make every assertion below vacuously
		// true, which is the one outcome this package must never report
		// as success.
		return nil, fmt.Errorf("the corpus at %s contains no exports", dir)
	}
	return out, nil
}

// FieldNames returns every distinct key across every object in an export
// set, sorted. This is the set that must be fully classified.
func FieldNames(exports map[string]Export) []string {
	seen := map[string]bool{}
	for _, export := range exports {
		for _, object := range export.Results {
			for name := range object {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Classification indexes the declared field table by AWX name.
type Classification map[string]Field

// Index builds a Classification, refusing a duplicate entry rather than
// letting the later one win silently.
func Index(fields []Field) (Classification, error) {
	out := make(Classification, len(fields))
	for _, f := range fields {
		if _, exists := out[f.Name]; exists {
			return nil, fmt.Errorf("field %q is classified twice", f.Name)
		}
		out[f.Name] = f
	}
	return out, nil
}

// Validate checks the table is internally coherent, independently of any
// export: every status carries what that status requires.
func (c Classification) Validate() []error {
	var problems []error
	for _, name := range c.Names() {
		f := c[name]
		switch f.Status {
		case Represented:
			if f.Ours == "" {
				problems = append(problems, fmt.Errorf("%q is represented but names nowhere it lands", name))
			}
		case Convertible:
			if f.Ours == "" {
				problems = append(problems, fmt.Errorf("%q is convertible but names nowhere it lands", name))
			}
			if f.Conversion == "" {
				problems = append(problems, fmt.Errorf("%q is convertible but states no conversion, which nobody can act on", name))
			}
		case Gap:
			if f.Phase == "" {
				problems = append(problems, fmt.Errorf("%q is a gap owned by no phase, not even \"unowned\"", name))
			}
		case Unsupported:
			if f.Note == "" {
				problems = append(problems, fmt.Errorf("%q is unsupported with no reason recorded", name))
			}
		case Metadata:
		default:
			problems = append(problems, fmt.Errorf("%q carries unknown status %q", name, f.Status))
		}
	}
	return problems
}

// Names returns every classified field name, sorted.
func (c Classification) Names() []string {
	names := make([]string, 0, len(c))
	for name := range c {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Unclassified returns every field present in the corpus that the table
// does not mention. It is the ratchet: a new AWX field arriving in a new
// export fails the build until somebody decides what it is.
func (c Classification) Unclassified(exports map[string]Export) []string {
	var missing []string
	for _, name := range FieldNames(exports) {
		if _, ok := c[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// Stale returns every classified field that appears in no export. Not a
// failure: the corpus is a sample, and a field classified from AWX's
// documentation before an example arrived is useful. Reported so the table
// does not accumulate entries nobody can check.
func (c Classification) Stale(exports map[string]Export) []string {
	present := map[string]bool{}
	for _, name := range FieldNames(exports) {
		present[name] = true
	}
	var stale []string
	for _, name := range c.Names() {
		if !present[name] {
			stale = append(stale, name)
		}
	}
	return stale
}

// Counts tallies the table by status.
func (c Classification) Counts() map[Status]int {
	counts := map[Status]int{}
	for _, name := range c.Names() {
		counts[c[name].Status]++
	}
	return counts
}
