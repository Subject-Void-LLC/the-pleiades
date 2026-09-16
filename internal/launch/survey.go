package launch

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrSurveyAnswer is returned when an answer violates its question's own
// schema.
//
// A failure rather than an ignored field, unlike a locked launch override,
// and the difference is who decided. A locked field is the template
// author's decision and the launch proceeds without it; a survey answer
// that breaks its schema means the run would proceed with a variable set
// the template author said was not acceptable, and there is no version of
// that run worth performing.
var ErrSurveyAnswer = errors.New("launch: survey answer is not valid for its question")

// ErrInvalidSurvey is returned when a survey could not be answered
// correctly by anybody.
var ErrInvalidSurvey = errors.New("launch: survey is not answerable")

// QuestionType is what a survey question accepts.
//
// AWX's own type names, deliberately, so a survey imported from an AWX job
// template means the same thing here that it meant there. A migration that
// silently reinterpreted `integer` as `float` would change what a playbook
// received without changing anything a reader could see.
type QuestionType string

// The question types.
const (
	QuestionText        QuestionType = "text"
	QuestionTextarea    QuestionType = "textarea"
	QuestionPassword    QuestionType = "password"
	QuestionInteger     QuestionType = "integer"
	QuestionFloat       QuestionType = "float"
	QuestionChoice      QuestionType = "multiplechoice"
	QuestionMultiSelect QuestionType = "multiselect"
)

// questionTypes is every type a survey may ask, in the order a form offers
// them: the free-text ones, then the numeric ones, then the bounded ones.
//
// An ordered slice rather than a set literal, because there are now two
// consumers and they need different things from one declaration. Validate
// needs membership; the authoring form needs a list to render as options,
// in an order that is the same on every page load, which ranging over a map
// is not. Deriving the membership test from the slice is what keeps a type
// from being offered by a form that the resolver would then refuse.
var questionTypes = []QuestionType{
	QuestionText,
	QuestionTextarea,
	QuestionPassword,
	QuestionInteger,
	QuestionFloat,
	QuestionChoice,
	QuestionMultiSelect,
}

// QuestionTypes returns every type a survey may ask, in a stable order.
//
// A copy, because the caller is a form builder and a slice handed out of a
// package is a slice the caller can sort in place. The cost is one small
// allocation per rendered form.
func QuestionTypes() []QuestionType {
	out := make([]QuestionType, len(questionTypes))
	copy(out, questionTypes)
	return out
}

// validQuestionTypes is the membership test a survey is validated against,
// derived from the list above so the two cannot disagree.
var validQuestionTypes = func() map[QuestionType]bool {
	m := make(map[QuestionType]bool, len(questionTypes))
	for _, t := range questionTypes {
		m[t] = true
	}
	return m
}()

// Secret reports whether an answer to this question must never be stored or
// rendered in plaintext.
func (q QuestionType) Secret() bool { return q == QuestionPassword }

// Question is one thing a launching operator is asked.
type Question struct {
	// Variable is the extra-variable name the answer is written to. It is
	// the whole point of the question: a survey exists to fill in values
	// the runbook or playbook reads.
	Variable string

	// Label is what the form asks. Help is the line under it.
	Label string
	Help  string

	Type QuestionType

	// Required refuses a launch that leaves it blank.
	Required bool

	// Default is used when an answer is absent and the question is not
	// required. Stored as a string and converted per type, because that is
	// how it arrives from a form and from an AWX export alike.
	Default string

	// Choices are the permitted values for the two choice types, and are
	// meaningless for the rest.
	Choices []string

	// Min and Max bound a numeric answer, or the length of a text one,
	// matching AWX's own min/max semantics. Both zero means unbounded.
	Min int
	Max int
}

// Survey is an ordered list of questions.
//
// Ordered because the order is authored: a question that only makes sense
// after another one has been answered has to render after it, and a map
// would put them in whatever order the runtime felt like.
type Survey struct {
	// Enabled is separate from having no questions, deliberately. A
	// template author who has written a survey and turned it off has said
	// something different from one who has not written a survey, and
	// deleting the questions to disable it would lose the work.
	Enabled   bool
	Questions []Question
}

// Asks reports whether this survey will actually prompt for anything.
func (s Survey) Asks() bool { return s.Enabled && len(s.Questions) > 0 }

// SecretVariables lists the variables whose answers must be encrypted at
// rest and redacted on the way out.
//
// Named here rather than decided at each call site, so the storage layer,
// the API projection and the UI cannot disagree about which answers are
// secret. Getting that wrong in one place is enough to write a password
// into a database column in plaintext.
func (s Survey) SecretVariables() []string {
	var out []string
	for _, q := range s.Questions {
		if q.Type.Secret() {
			out = append(out, q.Variable)
		}
	}
	sort.Strings(out)
	return out
}

// Validate refuses a survey nobody could answer correctly.
func (s Survey) Validate() error {
	seen := make(map[string]bool, len(s.Questions))

	for _, q := range s.Questions {
		variable := strings.TrimSpace(q.Variable)
		if variable == "" {
			return fmt.Errorf("%w: a question with no variable writes its answer nowhere", ErrInvalidSurvey)
		}
		if seen[variable] {
			return fmt.Errorf("%w: two questions both write to %q, so one answer would silently win",
				ErrInvalidSurvey, variable)
		}
		seen[variable] = true

		if !validQuestionTypes[q.Type] {
			return fmt.Errorf("%w: question %q has unknown type %q", ErrInvalidSurvey, variable, q.Type)
		}

		switch q.Type {
		case QuestionChoice, QuestionMultiSelect:
			if len(q.Choices) == 0 {
				return fmt.Errorf("%w: question %q offers a choice between nothing", ErrInvalidSurvey, variable)
			}
			if q.Default != "" && !q.offers(q.Default) {
				return fmt.Errorf("%w: question %q defaults to %q, which is not one of its choices",
					ErrInvalidSurvey, variable, q.Default)
			}
		case QuestionPassword:
			if q.Default != "" {
				// A default password is a credential in the template
				// record, readable by anybody who may edit the template
				// and copied into every duplicate of it.
				return fmt.Errorf("%w: question %q is a password and must not carry a default",
					ErrInvalidSurvey, variable)
			}
		}

		if q.Min != 0 || q.Max != 0 {
			if q.Min > q.Max {
				return fmt.Errorf("%w: question %q has a minimum above its maximum, which no answer satisfies",
					ErrInvalidSurvey, variable)
			}
		}
	}

	return nil
}

// offers reports whether value is one of this question's choices.
func (q Question) offers(value string) bool {
	for _, c := range q.Choices {
		if c == value {
			return true
		}
	}
	return false
}

// Resolve validates a launch's answers against this survey and returns the
// variables to merge.
//
// Answers for questions the survey does not ask are dropped, not refused.
// A stale saved configuration or a relaunch of a job whose template has
// since lost a question would otherwise become unlaunchable, and the
// dropped value cannot reach anything: it is not in the merged map, so
// nothing reads it.
func (s Survey) Resolve(answers map[string]any) (map[string]any, error) {
	if !s.Asks() {
		return nil, nil
	}

	out := make(map[string]any, len(s.Questions))
	for _, q := range s.Questions {
		raw, supplied := answers[q.Variable]

		if !supplied || isBlank(raw) {
			if q.Required {
				return nil, fmt.Errorf("%w: %q is required", ErrSurveyAnswer, q.Variable)
			}
			if q.Default != "" {
				value, err := q.coerce(q.Default)
				if err != nil {
					return nil, err
				}
				out[q.Variable] = value
			}
			continue
		}

		value, err := q.check(raw)
		if err != nil {
			return nil, err
		}
		out[q.Variable] = value
	}

	return out, nil
}

// CheckAnswers validates the answers somebody supplied without requiring
// the ones they did not.
//
// It exists for the one caller Resolve cannot serve: storing a launch
// configuration that will be completed later. A saved configuration is
// legitimately partial, because a schedule may carry the overrides while a
// required answer arrives at launch, so validating it through Resolve would
// refuse a configuration that is perfectly launchable and force the
// operator to answer a question nobody is asking yet.
//
// What it does check is every value that IS present, against the same
// per-question rules Resolve applies, so a configuration cannot be stored
// carrying a value that will be refused the moment somebody launches from
// it. Answers to questions this survey does not ask are dropped rather than
// refused, matching Resolve.
func (s Survey) CheckAnswers(answers map[string]any) error {
	if !s.Asks() {
		return nil
	}

	for _, q := range s.Questions {
		raw, supplied := answers[q.Variable]
		if !supplied || isBlank(raw) {
			continue
		}
		if _, err := q.check(raw); err != nil {
			return err
		}
	}
	return nil
}

// isBlank reports whether a supplied answer is empty in the way a form
// submits an untouched control.
func isBlank(raw any) bool {
	switch v := raw.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	default:
		return false
	}
}

// check validates one supplied answer and returns it in its question's own
// type.
func (q Question) check(raw any) (any, error) {
	switch q.Type {
	case QuestionText, QuestionTextarea, QuestionPassword:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q must be text", ErrSurveyAnswer, q.Variable)
		}
		if q.Max > 0 && len(s) > q.Max {
			return nil, fmt.Errorf("%w: %q must be at most %d characters", ErrSurveyAnswer, q.Variable, q.Max)
		}
		if q.Min > 0 && len(s) < q.Min {
			return nil, fmt.Errorf("%w: %q must be at least %d characters", ErrSurveyAnswer, q.Variable, q.Min)
		}
		return s, nil

	case QuestionInteger:
		n, err := toInt(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q must be a whole number", ErrSurveyAnswer, q.Variable)
		}
		if (q.Min != 0 || q.Max != 0) && (n < q.Min || n > q.Max) {
			return nil, fmt.Errorf("%w: %q must be between %d and %d", ErrSurveyAnswer, q.Variable, q.Min, q.Max)
		}
		return n, nil

	case QuestionFloat:
		f, err := toFloat(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q must be a number", ErrSurveyAnswer, q.Variable)
		}
		if (q.Min != 0 || q.Max != 0) && (f < float64(q.Min) || f > float64(q.Max)) {
			return nil, fmt.Errorf("%w: %q must be between %d and %d", ErrSurveyAnswer, q.Variable, q.Min, q.Max)
		}
		return f, nil

	case QuestionChoice:
		s, ok := raw.(string)
		if !ok || !q.offers(s) {
			return nil, fmt.Errorf("%w: %q must be one of %s", ErrSurveyAnswer, q.Variable, strings.Join(q.Choices, ", "))
		}
		return s, nil

	case QuestionMultiSelect:
		chosen, err := toStringList(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q must be a list of choices", ErrSurveyAnswer, q.Variable)
		}
		for _, c := range chosen {
			if !q.offers(c) {
				return nil, fmt.Errorf("%w: %q is not one of the choices for %q", ErrSurveyAnswer, c, q.Variable)
			}
		}
		return chosen, nil

	default:
		return nil, fmt.Errorf("%w: %q has unknown type %q", ErrSurveyAnswer, q.Variable, q.Type)
	}
}

// coerce converts a question's declared default into its own type. A
// default that cannot be coerced is a template that was saved wrong, which
// Validate is what should have caught.
func (q Question) coerce(value string) (any, error) {
	return q.check(anyString(value, q.Type))
}

// anyString shapes a stored string default as the Go value its type
// expects, so one validation path covers both an answer and a default.
func anyString(value string, kind QuestionType) any {
	switch kind {
	case QuestionMultiSelect:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return value
	}
}

func toFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err != nil {
			return 0, err
		}
		return f, nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}
