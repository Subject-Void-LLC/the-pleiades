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
// The first seven are AWX's own type names, deliberately, so a survey
// imported from an AWX job template means the same thing here that it meant
// there. A migration that silently reinterpreted `integer` as `float` would
// change what a playbook received without changing anything a reader could
// see.
//
// QuestionFile is this platform's own and AWX has no name for it, which is
// stated here rather than left for a reader to discover: the sentence above
// used to describe the whole set and would otherwise now be false. The
// direction of the difference is what makes it safe. A survey authored in
// AWX still means exactly what it meant, because every type it can name is
// here; a survey authored here that asks for a file has no AWX equivalent
// and would not survive a round trip out, which is the honest cost of
// having a type they do not.
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

	// QuestionFile carries a file's own text as the answer, so automation
	// can parse or scan something an operator supplies at launch rather
	// than something the template was saved with.
	//
	// The answer IS the content, as a string, and that is the whole of the
	// contract: no filename, no declared media type, no handle to fetch
	// later. On the wire it is indistinguishable from a textarea answer,
	// which is what lets every existing consumer of an extra variable read
	// it unchanged. What the type adds over a textarea is the bound, the
	// proof that the content is text, the program-content rule, and
	// secrecy by default.
	QuestionFile QuestionType = "file"
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
	QuestionFile,
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
//
// A file answer is secret, and the asymmetry is what decides it rather than
// a claim that every file is a credential. Most are not. But the files
// people actually paste into a launch form are private keys, kubeconfigs
// and service-account documents, and the two mistakes are not comparable:
// treating a hostname list as secret costs the ability to replay one stored
// answer, while treating a private key as public is not recoverable.
//
// The cost is narrower than it first reads. What SecretVariables refuses is
// replaying a STORED answer -- a relaunch or a schedule whose saved
// configuration actually answers this variable. A template carrying a file
// question can still be scheduled; what it cannot do is run unattended on a
// file somebody uploaded once, which is the behaviour to want anyway.
func (q QuestionType) Secret() bool {
	switch q {
	case QuestionPassword, QuestionFile:
		return true
	default:
		return false
	}
}

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

	// AllowProgramContent is the template author's half of the decision to
	// accept a file answer that opens with an interpreter line. It is
	// meaningless on every other type and Validate refuses it there, so a
	// flag cannot sit on a question where it does nothing and read as
	// though it does something.
	//
	// Per question rather than per template, because a template with three
	// file questions of which one ingests an install script should not
	// thereby widen the other two. It is still the template author's
	// decision in the sense that matters: they are the principal who sets
	// it, the template is the change-control surface, and template:write
	// is the scope.
	//
	// It permits nothing on its own. See FilePolicy for the deployment's
	// half and internal/launch/fileanswer.go for what the pair does and
	// does not buy.
	AllowProgramContent bool
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
		case QuestionFile:
			if q.Default != "" {
				// The same rule the password arm states, for the same
				// reason and one more. A default file body is a document
				// nobody uploaded sitting in the template record, copied
				// into every duplicate; and a file answer is secret, so it
				// would be a secret stored in the one place on a template
				// that is not encrypted.
				return fmt.Errorf("%w: question %q carries a file and must not carry a default",
					ErrInvalidSurvey, variable)
			}
			if q.Max > MaxFileAnswerBytes {
				// A bound the platform would not honour is worse than no
				// bound: it tells a template author a limit is in force
				// that is not.
				return fmt.Errorf("%w: question %q sets a maximum of %d bytes, above the %d a file answer may be",
					ErrInvalidSurvey, variable, q.Max, MaxFileAnswerBytes)
			}
		}

		if q.AllowProgramContent && q.Type != QuestionFile {
			// Refused rather than ignored. A flag that is stored, shown in
			// a form and consulted by nothing is the shape this repository
			// has shipped before: the control looks like a decision and is
			// not one.
			return fmt.Errorf("%w: question %q is a %s and cannot accept program content, which only a file question can",
				ErrInvalidSurvey, variable, q.Type)
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
func (s Survey) Resolve(answers map[string]any, pol FilePolicy) (map[string]any, error) {
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
				value, err := q.coerce(q.Default, pol)
				if err != nil {
					return nil, err
				}
				out[q.Variable] = value
			}
			continue
		}

		value, err := q.check(raw, pol)
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
		// Judged under a policy that PERMITS program content, deliberately.
		// Storing a launch configuration is not running one, and the
		// deployment's gate is a property of the Controller that will
		// eventually run it rather than of the one storing it. Every other
		// rule -- the size bound, the text proof -- still applies here, so
		// a configuration cannot be stored carrying something no launch
		// could ever accept; only the decision that genuinely belongs to
		// launch time is deferred to it.
		if _, err := q.check(raw, FilePolicy{AllowProgramContent: true}); err != nil {
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
func (q Question) check(raw any, pol FilePolicy) (any, error) {
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

	case QuestionFile:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q must be a text file", ErrSurveyAnswer, q.Variable)
		}
		if err := checkFileAnswer(q, s, pol); err != nil {
			return nil, err
		}
		// The content, unchanged. Not trimmed, because whitespace at
		// either end of a file is part of the file: a trailing newline is
		// what makes a PEM parse and a leading one is what makes a
		// signature verify.
		return s, nil

	default:
		return nil, fmt.Errorf("%w: %q has unknown type %q", ErrSurveyAnswer, q.Variable, q.Type)
	}
}

// coerce converts a question's declared default into its own type. A
// default that cannot be coerced is a template that was saved wrong, which
// Validate is what should have caught.
func (q Question) coerce(value string, pol FilePolicy) (any, error) {
	return q.check(anyString(value, q.Type), pol)
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
