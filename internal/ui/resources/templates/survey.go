// This file is the survey's write path: adding a question to a template,
// editing one in place, removing one, and moving one through the order.
//
// Until it existed the survey was the largest read-only object in the
// product. The model was complete -- seven question types, per-type
// validation, encrypted answers, AWX-compatible names -- and the only way
// to author one was the JSON API or the database, so the feature was
// reachable by people who could write a PUT body and by nobody else.
//
// All four controls name one endpoint, because they are one API operation:
// set-survey replaces the whole survey however it was changed, and "may
// this caller set this template's survey" has one answer for all of them.
// The store validates the result of every one of them, so a control cannot
// write a survey the API would have refused.
//
// The order is authored, which is why moving a question is a control rather
// than a consequence of editing one. A question that only makes sense after
// another has been answered has to render after it, and the stored
// display_order is what the launch form reads.
package templates

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// surveyTitle is the section heading and the page-index slug every one of
// these controls redirects back to.
const surveyTitle = "Survey"

// The survey controls' URL segments. They share one namespace with the
// template's own record actions, which Register enforces.
const (
	addQuestionName    = "add-question"
	editQuestionName   = "edit-question"
	removeQuestionName = "remove-question"
	moveQuestionUp     = "move-question-up"
	moveQuestionDown   = "move-question-down"
)

// questionTypeOptions offers the question types a survey may ask.
//
// Resolved from launch.QuestionTypes rather than written out here, so the
// form cannot offer a type the resolver does not implement and cannot omit
// one it does. A select built from a second hand-maintained list is a list
// that goes stale silently: the control simply stops offering something,
// and nothing fails.
func questionTypeOptions(context.Context) ([]view.Option, error) {
	types := launch.QuestionTypes()
	out := make([]view.Option, 0, len(types))
	for _, t := range types {
		out = append(out, view.Option{Label: string(t), Value: string(t)})
	}
	return out, nil
}

// questionFormFields are the controls a question's form offers, and one
// slice serves both the add form and the edit form.
//
// The variable is Immutable, which is what makes that work: an add form
// renders it because a new question has to write somewhere, and an edit
// form withholds it because the variable is the name the automation reads.
// Renaming one in place would leave every runbook and playbook referencing
// it reading an unset variable, while looking like a spelling correction,
// and would strand the answer stored in every saved configuration keyed by
// the old name. On an edit the URL is what says which question, and it is
// the only thing that does.
func questionFormFields(execAllowed bool) []view.Field {
	fields := []view.Field{
		{
			Name: "variable", Label: "VARIABLE", Kind: view.KindText, Required: true, InForm: true,
			Immutable:    true,
			Autocomplete: "off",
			Help:         "The extra-variable name the answer is written to. This is what the runbook or playbook reads.",
		},
		{
			Name: "label", Label: "QUESTION", Kind: view.KindText, Required: true, InForm: true,
			Help: "What the launch form asks. Shown above the control.",
		},
		{
			Name: "type", Label: "TYPE", Kind: view.KindSelect, InForm: true,
			Options: questionTypeOptions,
			Help:    "What the answer is. A password answer is encrypted at rest and read back as a marker, never its value.",
		},
		{
			Name: "required", Label: "REQUIRED", Kind: view.KindBool, InForm: true,
			Help: "Whether a launch that leaves it blank is refused.",
		},
		{
			Name: "help", Label: "HELP", Kind: view.KindLongText, InForm: true,
			Help: "The line under the question on the launch form.",
		},
		{
			Name: "default", Label: "DEFAULT", Kind: view.KindText, InForm: true,
			Help: "Used when an answer is absent and the question is not required. A password question may not carry one: " +
				"it would be a credential sitting in the template, readable by anybody who may edit it.",
		},
		{
			Name: "choices", Label: "CHOICES", Kind: view.KindTags, InForm: true,
			Help: "The permitted values, for a multiplechoice or multiselect question. Ignored by every other type.",
		},
		{
			Name: "min", Label: "MINIMUM", Kind: view.KindNumber, InForm: true,
			Help: "Bounds a numeric answer, or the length of a text one. On a file question this is a byte count. Leave both blank for unbounded.",
		},
		{
			Name: "max", Label: "MAXIMUM", Kind: view.KindNumber, InForm: true,
			Help: "See minimum.",
		},
	}

	// Offered only where it could do something. A deployment that has not
	// set PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT refuses program
	// content whatever a question says, so drawing the checkbox there would
	// be offering a choice whose only outcome is that nothing changes --
	// and the person ticking it usually cannot change the other half.
	//
	// Hiding the control is not the enforcement. A submission carrying the
	// value still reaches Validate, which refuses it on any question that
	// is not a file, and a launch still consults both gates: a form that
	// only hides a control is a form a hand-posted body walks straight
	// past.
	if execAllowed {
		fields = append(fields, view.Field{
			Name: "allow_program_content", Label: "ACCEPTS PROGRAM CONTENT", Kind: view.KindBool, InForm: true,
			Help: "File questions only. On, an answer opening with an interpreter line is accepted rather than " +
				"refused. It does not make an answer safe: an automation that pipes any text answer to a shell " +
				"runs it whether or not this is on.",
		})
	}
	return fields
}

// addQuestionAction appends one question to a template's survey.
//
// Appends rather than inserts, because the order is authored and the two
// move controls are what author it. An add form carrying a position would
// be asking for a decision the operator can make more legibly afterwards,
// against a list they can see.
//
// It also turns the survey ON when it adds the first question. A survey
// with questions that is disabled asks nothing, and somebody who has just
// written their first question has not asked for that: enabled and empty is
// the state that carries no information, and disabled with questions is the
// state a template author chooses deliberately later. The redirect lands
// back on the section, where the badge says which it now is.
func addQuestionAction(store launch.Store, execAllowed bool) view.RecordAction {
	return view.RecordAction{
		Name:     addQuestionName,
		Label:    "Add question",
		Heading:  "Add a question to this survey",
		Endpoint: &apispec.SetTemplateSurvey,
		Fields:   questionFormFields(execAllowed),
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			redirect, err := withSurvey(ctx, store, id, func(s *launch.Survey) error {
				q, errs := questionFrom(strings.TrimSpace(v.Get("variable")), launch.Question{}, v)
				if errs.Any() {
					return fieldFault{errs}
				}
				s.Questions = append(s.Questions, q)
				s.Enabled = true
				return nil
			})
			if fault := (fieldFault{}); errors.As(err, &fault) {
				return "", fault.errs, nil
			}
			if fault := surveyFault(err); fault.Any() {
				return "", fault, nil
			}
			return redirect, nil, err
		},
	}
}

// editQuestionAction rewrites one question in place, keeping its position.
//
// In place rather than remove-and-append, because the survey's order is the
// order the launch form renders its controls in, and an edit that moved a
// question to the bottom would silently rearrange the form for everybody
// who launches the template.
func editQuestionAction(store launch.Store, execAllowed bool) view.RowAction {
	return view.RowAction{
		Name:     editQuestionName,
		Label:    "Edit",
		Heading:  "Edit this question",
		Endpoint: &apispec.SetTemplateSurvey,
		Fields:   questionFormFields(execAllowed),
		Form:     questionValues(store, execAllowed),
		Submit: func(ctx context.Context, parentID, rowID string, v view.Values) (string, view.FieldErrors, error) {
			redirect, err := withSurvey(ctx, store, parentID, func(s *launch.Survey) error {
				at := indexOfQuestion(*s, rowID)
				if at < 0 {
					return view.Refuse(fmt.Errorf("this template has no question writing to %q, so nothing was changed", rowID))
				}
				// The stored variable, never a submitted one. The form does
				// not offer the control, so there is nothing to read, and
				// taking it from the URL is what makes a rename impossible
				// rather than merely undocumented.
				q, errs := questionFrom(rowID, s.Questions[at], v)
				if errs.Any() {
					return fieldFault{errs}
				}
				s.Questions[at] = q
				return nil
			})
			if fault := (fieldFault{}); errors.As(err, &fault) {
				return "", fault.errs, nil
			}
			if fault := surveyFault(err); fault.Any() {
				return "", fault, nil
			}
			return redirect, nil, err
		},
	}
}

// removeQuestionAction takes one question out of a survey.
//
// The confirmation says what the removal costs rather than only asking
// whether you are sure. A removed question stops being asked, so every
// future launch runs with that variable unset unless something else
// supplies it, and a runbook that reads it gets whatever its own default
// is. The answers already stored in saved configurations are unaffected --
// they are keyed by variable name and Survey.Resolve drops an answer to a
// question the survey no longer asks -- so adding the question back
// restores them, which is the one part of this that is reversible.
func removeQuestionAction(store launch.Store) view.RowAction {
	return view.RowAction{
		Name:     removeQuestionName,
		Label:    "Remove",
		Endpoint: &apispec.SetTemplateSurvey,
		Confirm: "This question stops being asked, so every future launch runs with that variable unset unless " +
			"something else supplies it. Saved configurations keep the answer they hold, so adding the question " +
			"back restores it.",
		Submit: func(ctx context.Context, parentID, rowID string, _ view.Values) (string, view.FieldErrors, error) {
			redirect, err := withSurvey(ctx, store, parentID, func(s *launch.Survey) error {
				at := indexOfQuestion(*s, rowID)
				if at < 0 {
					// A row id that matches nothing is refused rather than
					// treated as a successful no-op. The two are
					// indistinguishable to whoever pressed the button, and
					// "the question is gone" is the reading they would take
					// from a redirect, which on a stale page is false.
					return view.Refuse(fmt.Errorf("this template has no question writing to %q, so nothing was removed", rowID))
				}
				s.Questions = append(s.Questions[:at], s.Questions[at+1:]...)
				return nil
			})
			return redirect, nil, err
		},
	}
}

// moveQuestionUpAction and moveQuestionDownAction swap a question with its
// neighbour.
//
// Two controls rather than a position field on the edit form, because the
// question an operator is actually asking is "this one belongs above that
// one", which they can see, and not "this one belongs at index 4", which
// they would have to count. Each is withheld at the end it cannot move
// past: offering a "move up" on the first row would be offering a button
// whose only possible outcome is a refusal.
//
// Withholding is about not drawing a dead control and never about safety.
// The list can change between the page rendering and the button being
// pressed, so both Submits check the bound again and refuse.
func moveQuestionUpAction(store launch.Store) view.RowAction {
	return view.RowAction{
		Name:     moveQuestionUp,
		Label:    "Move up",
		Endpoint: &apispec.SetTemplateSurvey,
		Applies:  func(_ view.Row, at view.RowPosition) bool { return !at.First() },
		Submit:   moveSubmit(store, -1),
	}
}

func moveQuestionDownAction(store launch.Store) view.RowAction {
	return view.RowAction{
		Name:     moveQuestionDown,
		Label:    "Move down",
		Endpoint: &apispec.SetTemplateSurvey,
		Applies:  func(_ view.Row, at view.RowPosition) bool { return !at.Last() },
		Submit:   moveSubmit(store, +1),
	}
}

// moveSubmit is the swap both move controls perform, differing only in
// direction.
//
// A swap rather than a remove-and-insert: the two neighbours trade places,
// which is what the button says, and every other question keeps the
// position it had. Written once because a second copy differing by a sign
// is how the two directions come to disagree about the boundary.
func moveSubmit(store launch.Store, delta int) func(context.Context, string, string, view.Values) (string, view.FieldErrors, error) {
	return func(ctx context.Context, parentID, rowID string, _ view.Values) (string, view.FieldErrors, error) {
		redirect, err := withSurvey(ctx, store, parentID, func(s *launch.Survey) error {
			at := indexOfQuestion(*s, rowID)
			if at < 0 {
				return view.Refuse(fmt.Errorf("this template has no question writing to %q, so nothing was moved", rowID))
			}
			to := at + delta
			if to < 0 || to >= len(s.Questions) {
				// The page was drawn before somebody else moved or removed
				// a question. Saying so is more use than a silent success,
				// because the list they are looking at is out of date.
				return view.Refuse(fmt.Errorf("%q is already at the end it was asked to move past; reload the survey", rowID))
			}
			s.Questions[at], s.Questions[to] = s.Questions[to], s.Questions[at]
			return nil
		})
		return redirect, nil, err
	}
}

// withSurvey is the read, edit, write every survey control shares.
//
// It reads the stored template so the metadata, defaults and prompts beside
// the survey are carried forward unchanged, which is the same reason the
// narrowed API endpoint reads it: Store.Update writes the whole template,
// so a write built from the form alone would blank everything the form did
// not carry.
func withSurvey(ctx context.Context, store launch.Store, parentID string,
	edit func(*launch.Survey) error) (string, error) {

	id, err := strconv.Atoi(parentID)
	if err != nil || id < 1 {
		return "", launch.ErrNotFound
	}
	tmpl, err := store.Get(ctx, id)
	if err != nil {
		return "", err
	}

	if err := edit(&tmpl.Survey); err != nil {
		return "", err
	}

	if err := store.Update(ctx, tmpl); err != nil {
		return "", err
	}
	return "/ui/" + Name + "/" + parentID + "?tab=" + view.TabSlug(surveyTitle), nil
}

// questionValues prefills the edit form from the stored question.
//
// Read back from the survey rather than from the row the table drew,
// because a Row's cells are display strings and a form value is a
// submission token. REQUIRED renders "yes" in its column where the checkbox
// reads only the literal "true", and HELP, DEFAULT, MINIMUM and MAXIMUM
// appear in no column at all. A prefill built from cells would be wrong in
// one control and blank in four.
func questionValues(store launch.Store, execAllowed bool) func(context.Context, string, string) (map[string]string, error) {
	return func(ctx context.Context, parentID, rowID string) (map[string]string, error) {
		tmpl, ok := load(ctx, store, parentID)
		if !ok {
			return nil, launch.ErrNotFound
		}
		at := indexOfQuestion(tmpl.Survey, rowID)
		if at < 0 {
			// A stale page reaching a question somebody else removed. A
			// refusal rather than an error page, because the operator can
			// act on it: the list they are looking at is out of date.
			return nil, view.Refuse(fmt.Errorf("this template has no question writing to %q any more", rowID))
		}
		q := tmpl.Survey.Questions[at]

		values := map[string]string{
			"label":    q.Label,
			"type":     string(q.Type),
			"required": checkbox(q.Required),
			"help":     q.Help,
			"default":  q.Default,
			"choices":  strings.Join(q.Choices, ", "),
			"min":      bound(q.Min),
			"max":      bound(q.Max),
			// No "variable": it is Immutable, so the edit form does not
			// render it, and NarrowPrefill refuses a value for a control the
			// form does not draw.
		}

		// Prefilled only where the form draws the control, for the same
		// reason the variable is absent above: NarrowPrefill refuses a
		// value for a control the form does not render, so returning this
		// unconditionally would make every edit fail on a deployment that
		// withholds the checkbox.
		if execAllowed {
			values["allow_program_content"] = checkbox(q.AllowProgramContent)
		}
		return values, nil
	}
}

// checkbox encodes a boolean the way a submitted checkbox encodes it. The
// form reads the literal "true" and nothing else, so "false" would render
// as unchecked exactly as "" does and would be a second spelling of one
// state.
func checkbox(on bool) string {
	if on {
		return "true"
	}
	return ""
}

// bound renders a min or max for the form, leaving zero blank.
//
// Zero and unbounded are the same state in the model -- Survey.Validate
// reads "both zero" as no bound at all -- so prefilling a literal "0" would
// show the operator a limit that is not being enforced, and saving it back
// would look like they had set one.
func bound(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// questionFrom reads a submitted question form, with the values the form
// did not render supplied by the caller rather than read off the
// submission.
//
// Two values are carried rather than read, for the same reason and with
// different consequences. The VARIABLE is withheld by the edit form because
// renaming one in place would strand every answer keyed by the old name.
// ALLOW_PROGRAM_CONTENT is withheld on a deployment that refuses program
// content, because a control that could change nothing must not be drawn --
// and that is exactly what made reading it off the submission a data loss:
// an absent checkbox reads back as false, so editing a question's HELP TEXT
// on such a deployment silently cleared a flag a template author had set,
// with nothing on the page saying so. The template then looks disarmed and
// stays disarmed after the deployment's consent returns.
//
// carried is the stored question. Everything else is read from the form,
// which is what keeps the add and edit forms meaning the same thing.
func questionFrom(variable string, carried launch.Question, v view.Values) (launch.Question, view.FieldErrors) {
	errs := view.FieldErrors{}

	min, err := boundValue(v.Get("min"))
	if err != nil {
		errs.Add("min", "Minimum must be a whole number.")
	}
	max, err := boundValue(v.Get("max"))
	if err != nil {
		errs.Add("max", "Maximum must be a whole number.")
	}

	return launch.Question{
		Variable: variable,
		Label:    strings.TrimSpace(v.Get("label")),
		Help:     strings.TrimSpace(v.Get("help")),
		Type:     launch.QuestionType(strings.TrimSpace(v.Get("type"))),
		Required: v.Bool("required"),
		// Read from the form only where the form draws it. Where it does
		// not, the stored value is carried forward: an absent checkbox and
		// a cleared one are indistinguishable in a submission, and treating
		// them the same silently disarms a question nobody touched.
		AllowProgramContent: carriedOrSubmitted(carried, v),
		Default:             strings.TrimSpace(v.Get("default")),
		Choices:             v.Tags("choices"),
		Min:                 min,
		Max:                 max,
	}, errs
}

// carriedOrSubmitted decides where a question's program-content flag comes
// from.
//
// The form draws the control only where the deployment permits program
// content at all, so on every other deployment the submission carries
// nothing and the stored value is the only truth there is. Reading the
// absent control would clear it.
//
// Where the control IS drawn, the submission wins, including when it is
// unchecked: that is somebody deliberately turning the flag off, and
// carrying the old value there would make the checkbox the thing that
// cannot be cleared.
func carriedOrSubmitted(carried launch.Question, v view.Values) bool {
	if !v.Declares("allow_program_content") {
		return carried.AllowProgramContent
	}
	return v.Bool("allow_program_content")
}

// boundValue reads a min or max control, treating blank as unbounded.
func boundValue(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	return strconv.Atoi(trimmed)
}

// indexOfQuestion finds the question writing to a variable.
//
// The variable is the row id, which the schema's own unique index makes
// safe: one question per variable per template, so a match is the match.
func indexOfQuestion(s launch.Survey, variable string) int {
	for i, q := range s.Questions {
		if q.Variable == variable {
			return i
		}
	}
	return -1
}

// fieldFault carries per-control errors out through the edit callback,
// which can only return an error.
//
// The alternative was for each control to validate before calling
// withSurvey, which means every control repeating the read of the template
// it is about to edit, and two of them getting the order wrong.
type fieldFault struct{ errs view.FieldErrors }

func (f fieldFault) Error() string { return "the submitted question is not valid" }

// surveyFault turns the store's own survey refusals into messages on the
// control that caused them.
//
// The store is the authority -- it is the same validation the JSON API
// runs -- so its own words are shown rather than a vaguer restatement. Only
// launch.ErrInvalidSurvey reaches here; anything else is a fault rather
// than the operator's doing, and is answered as one.
func surveyFault(err error) view.FieldErrors {
	errs := view.FieldErrors{}
	if !errors.Is(err, launch.ErrInvalidSurvey) {
		return errs
	}
	msg := fieldMessage(err)
	switch {
	case strings.Contains(msg, "no variable"), strings.Contains(msg, "both write to"):
		errs.Add("variable", msg)
	case strings.Contains(msg, "unknown type"):
		errs.Add("type", msg)
	case strings.Contains(msg, "choice between nothing"):
		errs.Add("choices", msg)
	case strings.Contains(msg, "not one of its choices"), strings.Contains(msg, "must not carry a default"):
		errs.Add("default", msg)
	case strings.Contains(msg, "minimum above its maximum"):
		errs.Add("min", msg)
	case strings.Contains(msg, "above the"):
		// A file question's maximum exceeding what the platform honours.
		errs.Add("max", msg)
	case strings.Contains(msg, "cannot accept program content"):
		// Only reachable on a deployment that draws the checkbox, since
		// the field is otherwise absent and reads back false. Mapped
		// anyway: the rule is also reachable from the JSON API, and a
		// refusal landing on the variable would name the wrong control.
		errs.Add("allow_program_content", msg)
	default:
		// A rule with no single control to blame lands on the variable, the
		// field that names the question the rest describe.
		errs.Add("variable", msg)
	}
	return errs
}
