package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SurveyQuestion holds the schema definition for one question a launching
// operator is asked before a template runs.
//
// A survey is how a template author lets somebody vary a run without
// letting them vary the run's shape: an answer writes into extra variables
// under a name the author chose, and nowhere else. That containment is the
// whole design, and it is why a survey needs no separate entry in a
// template's prompts list.
type SurveyQuestion struct {
	ent.Schema
}

// Mixin of the SurveyQuestion.
func (SurveyQuestion) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the SurveyQuestion.
func (SurveyQuestion) Fields() []ent.Field {
	return []ent.Field{
		// variable is the extra-variable name the answer is written to. It
		// is the point of the question: a survey exists to fill in values
		// the runbook or playbook reads.
		field.String("variable").NotEmpty(),

		field.String("label").NotEmpty(),
		field.String("help").Optional(),

		// question_type is AWX's own type vocabulary (text, textarea,
		// password, integer, float, multiplechoice, multiselect), so a
		// survey imported from an AWX job template means the same thing
		// here that it meant there. A migration that silently reinterpreted
		// `integer` as `float` would change what a playbook received
		// without changing anything a reader could see. `file` is an eighth
		// and is this platform's own: AWX has no name for it, so a survey
		// asking for one has no equivalent to export back.
		//
		// A plain string rather than an ent enum, for the reason
		// Template.kind gives: the set is enforced in Go, where it can be
		// enforced once, rather than in Go and in two dialects' DDL. The
		// consequence worth knowing is that this column is the only record
		// of the vocabulary at the storage layer and nothing here refuses a
		// value outside it -- internal/launch.Survey.Validate is the single
		// gate, reached through Template.Validate on both Create and
		// Update.
		field.String("question_type").NotEmpty(),

		// allow_program_content is the template author's half of the
		// decision to accept a file answer that opens with an interpreter
		// line. It is meaningless on every other question type and
		// internal/launch refuses it there.
		//
		// It permits nothing on its own: the deployment must also consent,
		// through an environment variable the Controller reads at startup,
		// and that half is deliberately NOT stored here. Storing it would
		// make a template carry its own permission, so copying the template
		// to another deployment would carry the permission with it, and
		// turning the deployment's consent off would not stop templates
		// that already had it. Keeping the deployment's half out of the
		// database is what makes it a live kill switch.
		field.Bool("allow_program_content").Default(false),

		field.Bool("required").Default(false),

		// default_value is used when an answer is absent and the question
		// is not required. Stored as a string and converted per type,
		// because that is how it arrives from a form and from an AWX export
		// alike. internal/launch refuses a default on a password question:
		// a default password is a credential sitting in the template
		// record, readable by anybody who may edit the template and copied
		// into every duplicate of it.
		//
		// Named default_value rather than default because ent generates a
		// FieldDefault constant from it, and a schema field literally named
		// "default" collides with ent's own builder vocabulary.
		field.String("default_value").Optional(),

		// choices are the permitted values for the two choice types, and
		// are meaningless for the rest.
		field.JSON("choices", []string{}).Optional(),

		// min_value and max_value bound a numeric answer or the length of a
		// text one, matching AWX's own min/max semantics. Both zero means
		// unbounded.
		field.Int("min_value").Default(0),
		field.Int("max_value").Default(0),

		// display_order is what a form renders by. Explicit rather than
		// implied by insertion order: the order is authored, since a
		// question that only makes sense after another has been answered
		// has to render after it, and a query with no ORDER BY returns rows
		// in whatever order the storage engine feels like.
		field.Int("display_order").Default(0),
	}
}

// Edges of the SurveyQuestion.
func (SurveyQuestion) Edges() []ent.Edge {
	return []ent.Edge{
		// A question belongs to exactly one template. Required: a question
		// attached to nothing is one nobody can ever be asked.
		edge.From("template", Template.Type).
			Ref("survey_questions").
			Unique().
			Required(),
	}
}

// Indexes of the SurveyQuestion.
func (SurveyQuestion) Indexes() []ent.Index {
	return []ent.Index{
		// Every read is "this template's survey, in order", so the pair is
		// indexed rather than either column alone.
		index.Fields("display_order").Edges("template"),

		// One question per variable per template. Two questions writing to
		// one variable means one answer silently wins, and which one would
		// depend on row order.
		index.Fields("variable").Edges("template").Unique(),
	}
}
