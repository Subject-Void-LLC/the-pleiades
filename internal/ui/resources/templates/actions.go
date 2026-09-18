package templates

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is the two record actions AWX puts on a template and this one
// does too: launch it, and copy it.
//
// The launch form is the interesting one. It renders exactly the fields
// this particular template opened, plus its survey, and nothing else. That
// is not a nicety: a locked field rendered as a control is an affordance
// that does nothing, since the resolver reports it as ignored the moment it
// is submitted, and this repository has shipped controls that silently did
// nothing before and recorded it. The fields a record opens are a property
// of that record, which is why the action resolves them per record rather
// than declaring one static list.

// surveyPrefix namespaces a survey answer's form control, so a question
// with the variable "limit" cannot collide with the launch field of the
// same name. Both legitimately exist on the same form: one sets how the run
// is bounded, the other sets a variable the runbook reads.
const surveyPrefix = "answer_"

// launchAction runs a template, prompting for whatever it lets a launcher
// decide.
func launchAction(store launch.Store, dispatcher *api.Dispatcher, creds credentials) view.RecordAction {
	return view.RecordAction{
		Name:     "launch",
		Label:    "Launch",
		Heading:  "Launch template",
		Endpoint: &apispec.LaunchTemplate,

		// No static Fields. Declaring any would put a control on every
		// template's form whether that template opened it or not.
		FieldsFor: func(ctx context.Context, id string) ([]view.Field, error) {
			tmpl, ok := load(ctx, store, id)
			if !ok {
				// A template that cannot be read prompts for nothing. The
				// submit that follows resolves it again and reports the
				// failure properly; rendering a speculative form here would
				// be guessing at controls.
				return nil, nil
			}
			// The deployment's half of the survey file rule, read from the
			// dispatcher rather than from the environment here, so the
			// form says what this Controller will actually do when the
			// answer is submitted.
			return launchFields(ctx, tmpl, creds, id, dispatcher.AllowsProgramContent())
		},

		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				// The actor comes from the request's identity, never from
				// the submission. A caller who could name the actor could
				// forge the audit trail the field exists to be.
				return "", nil, errors.New("no identity on the request context")
			}

			templateID, err := strconv.Atoi(id)
			if err != nil || templateID < 1 {
				return "", nil, fmt.Errorf("launch: %q is not a template id", id)
			}

			tmpl, ok := load(ctx, store, templateID2String(templateID))
			if !ok {
				return "", view.FieldErrors{"": {"That template no longer exists. Reload the list."}}, nil
			}

			cfg, errs := bindLaunch(tmpl, v)
			if errs.Any() {
				return "", errs, nil
			}

			// The prompted credential inputs, read back off the same
			// controls this form rendered. They are a separate argument
			// rather than part of cfg, and that is the never-persist rule
			// as a property of the types: recordConfig takes a
			// launch.Config, so the function that writes to the database is
			// structurally unable to see these values.
			prompted := bindPromptedCredentials(promptFields(ctx, creds, templateID), v)

			// api.Dispatcher.LaunchTemplate, the same method the JSON API's
			// own handler calls. A view layer that reimplemented
			// resolve-record-persist-publish would have two orderings to
			// keep in agreement, and the one that drifts is always the one
			// with fewer readers.
			jobID, _, err := dispatcher.LaunchTemplate(ctx, identity.Subject, templateID, cfg, prompted)
			switch {
			case errors.Is(err, launch.ErrNotFound):
				return "", view.FieldErrors{"": {"That template no longer exists. Reload the list."}}, nil
			case errors.Is(err, launch.ErrSurveyAnswer):
				// The survey's own message, attached to the form rather
				// than answered with a 500: it names the variable and says
				// what was wrong with the answer, which is the whole point
				// of having validated it.
				return "", view.FieldErrors{"": {fieldMessage(err)}}, nil
			case errors.Is(err, launch.ErrUnknownKind):
				return "", view.FieldErrors{"": {"This controller cannot run that kind of template."}}, nil
			case err != nil:
				return "", nil, err
			}

			// The ignored fields are deliberately not surfaced here, and
			// that is a consequence of this form rather than a shortcut:
			// the controls offered were exactly the ones this template
			// opened, and the submission is narrowed to them before it
			// arrives, so there is nothing left for the resolver to refuse.
			// The JSON API, whose callers choose their own body, reports
			// them in full.

			// Straight to the job that was just started, because the next
			// thing anybody wants is to watch it.
			return "/ui/jobs/" + jobID, nil, nil
		},
	}
}

// launchFields is the form one template offers: its opened fields, then its
// survey's questions.
//
// Order matters. The fields decide how the run is bounded and the survey
// asks for values the automation reads, so the launch controls come first
// and the questions follow, which is the order AWX prompts in too.
func launchFields(ctx context.Context, tmpl launch.Template, creds credentials, id string, execAllowed bool) ([]view.Field, error) {
	d, err := tmpl.Descriptor()
	if err != nil {
		// A template whose kind is no longer registered offers no controls.
		// The submit path reports it as something this controller cannot
		// run, which is the honest answer: the failure is the deployment's,
		// not the operator's.
		return nil, nil
	}

	var out []view.Field
	for _, spec := range d.Fields {
		if !tmpl.Promptable(spec.Name) {
			continue
		}
		out = append(out, fieldFor(spec, tmpl.Defaults))
	}

	if tmpl.Survey.Asks() {
		for _, q := range tmpl.Survey.Questions {
			out = append(out, questionField(q, execAllowed))
		}
	}

	// The credential prompts last, after the fields that bound the run and
	// the questions the automation reads. They are the one group whose
	// values are never stored, so they read as the final thing asked for
	// rather than as another saved setting.
	templateID, err := strconv.Atoi(id)
	if err != nil {
		return out, nil
	}
	return append(out, promptFields(ctx, creds, templateID)...), nil
}

// promptFields resolves the prompted credential controls for one template,
// or none when there is no credential surface wired.
//
// One function for both callers, which is what keeps the form and the
// submission in agreement: the controls the form renders are exactly the
// controls the submission is read back through, so a value can neither
// arrive undeclared nor be silently dropped.
func promptFields(ctx context.Context, creds credentials, templateID int) []view.Field {
	if creds == nil {
		return nil
	}
	fields, err := promptedCredentialFields(creds, templateID)(ctx)
	if err != nil {
		return nil
	}
	return fields
}

// fieldFor renders one launch field as a form control, carrying the
// template's saved value as the help text rather than as the value.
//
// Prefilling would make an operator who changes nothing submit the same
// value explicitly, which is indistinguishable on the wire from one who
// meant to override it with what it already was. Leaving it blank means
// absent, which is what "use the template's value" is, and saying what that
// value is keeps the form legible.
func fieldFor(spec launch.FieldSpec, defaults launch.Fields) view.Field {
	f := view.Field{
		Name:         spec.Name,
		Label:        spec.Label,
		Help:         spec.Help,
		Autocomplete: "off",
		InForm:       true,
	}
	if saved := savedValue(spec, defaults); saved != "" {
		f.Help = strings.TrimSuffix(f.Help, " ") + " Leave blank to use this template's saved value, " + saved + "."
	}

	switch spec.Type {
	case launch.TypeInt:
		f.Kind = view.KindNumber
		if spec.Bounded() {
			// Stated rather than enforced by the control: this UI's number
			// input carries no min or max attribute, and a browser-side
			// bound would not be the check that matters anyway. bindLaunch
			// runs the kind's own normalizer, which is where the refusal
			// actually happens.
			f.Help += " Between " + strconv.Itoa(spec.Min) + " and " + strconv.Itoa(spec.Max) + "."
		}
	case launch.TypeStringList:
		f.Kind = view.KindTags
	case launch.TypeMap:
		// Key=value lines, the same shape an operator types into AWX's own
		// extra-variables box, parsed on submit. A textarea rather than a
		// tag field because a variable's value legitimately contains
		// spaces and commas.
		f.Kind = view.KindLongText
		f.Help += " One key=value per line."
	default:
		f.Kind = view.KindText
	}
	return f
}

// savedValue renders a template's own value for a field, for the help text.
func savedValue(spec launch.FieldSpec, defaults launch.Fields) string {
	if !defaults.Has(spec.Name) {
		return ""
	}
	switch spec.Type {
	case launch.TypeInt:
		return strconv.Itoa(defaults.Int(spec.Name))
	case launch.TypeStringList:
		if list := defaults.List(spec.Name); len(list) > 0 {
			return strings.Join(list, ", ")
		}
		return ""
	case launch.TypeMap:
		if m := defaults.Map(spec.Name); len(m) > 0 {
			return strconv.Itoa(len(m)) + " variables"
		}
		return ""
	default:
		return defaults.String(spec.Name)
	}
}

// questionField renders one survey question as a form control.
//
// A password question renders as a password control, which is the whole
// reason the question carries a type: the value is encrypted at rest, read
// back as a redaction marker, and must not be typed into a box that shows
// it or that a browser offers to remember.
func questionField(q launch.Question, execAllowed bool) view.Field {
	f := view.Field{
		Name:         surveyPrefix + q.Variable,
		Label:        strings.ToUpper(q.Label),
		Help:         q.Help,
		Required:     q.Required,
		Autocomplete: "off",
		InForm:       true,
	}
	if f.Label == "" {
		f.Label = strings.ToUpper(q.Variable)
	}

	switch q.Type {
	case launch.QuestionTextarea:
		f.Kind = view.KindLongText
	case launch.QuestionPassword:
		f.Kind = view.KindPassword
	case launch.QuestionInteger:
		f.Kind = view.KindNumber
		if q.Min != 0 || q.Max != 0 {
			f.Help += " Between " + strconv.Itoa(q.Min) + " and " + strconv.Itoa(q.Max) + "."
		}
	case launch.QuestionFloat:
		// A text control rather than a number one: this platform's numeric
		// control is an integer input, and rendering a float question as
		// one would refuse "1.5" in the browser before the survey ever saw
		// it.
		f.Kind = view.KindText
	case launch.QuestionChoice:
		f.Kind = view.KindSelect
		f.Options = choiceOptions(q.Choices)
	case launch.QuestionMultiSelect:
		f.Kind = view.KindLookup
		f.Options = choiceOptions(q.Choices)
	case launch.QuestionFile:
		// A text area holding the file's own content, and the honest
		// reading of that is that this is a PASTE rather than an upload:
		// every write in this UI is parsed with r.ParseForm, which does not
		// read a multipart body at all, so an <input type="file"> here
		// would post the filename and silently blank every other control on
		// the form. A file picker is a separate piece of work on the form
		// pipeline; the type's rules, its bound and its secrecy are real
		// either way, and what reaches the automation is identical.
		f.Kind = view.KindLongText
		// The question's own bound when it set one, so the control stops
		// where the resolver will. Clamped to the platform's, which
		// Survey.Validate already refuses to let a question exceed.
		f.MaxLen = launch.MaxFileAnswerBytes
		if q.Max > 0 && q.Max < f.MaxLen {
			f.MaxLen = q.Max
		}
		f.Help = strings.TrimSuffix(f.Help, " ") + fileAnswerHelp(q, execAllowed)
	default:
		f.Kind = view.KindText
		f.MaxLen = q.Max
	}
	return f
}

// fileAnswerHelp is what a file question says under its control, and it
// differs by whether the answer will be accepted as a program.
//
// Two sentences rather than one, and the extra only when it is true,
// because that is the moment a disclosure is worth anything: a paragraph in
// a document nobody opens is not a disclosure, and a warning shown on every
// file question regardless of state is one people learn to scroll past.
func fileAnswerHelp(q launch.Question, execAllowed bool) string {
	base := " Paste the file's text. Up to " + strconv.Itoa(launch.MaxFileAnswerBytes/1024) +
		" KB of UTF-8 text; a binary file is refused. The answer is treated as secret, so it is " +
		"encrypted in any configuration you save and is never replayed by a relaunch or a schedule."

	switch {
	case q.AllowProgramContent && execAllowed:
		return base + " This question and this deployment both accept program content, so a file opening " +
			"with an interpreter line is allowed. What you paste is handed to the automation unchanged, and " +
			"if the automation runs it, it runs with the runner's own privileges."
	case q.AllowProgramContent:
		return base + " This question is marked as accepting program content, but this deployment does not " +
			"permit it, so a file opening with an interpreter line is still refused."
	default:
		return base + " A file opening with an interpreter line is refused."
	}
}

func choiceOptions(choices []string) func(context.Context) ([]view.Option, error) {
	return func(context.Context) ([]view.Option, error) {
		out := make([]view.Option, 0, len(choices))
		for _, c := range choices {
			out = append(out, view.Option{Label: c, Value: c})
		}
		return out, nil
	}
}

// bindLaunch reads a submitted launch form into the sparse configuration
// the resolver folds.
//
// Sparse is the load-bearing part: a control left blank must be absent
// rather than present and empty, or an operator who touched nothing would
// clear every value the template was saved with.
func bindLaunch(tmpl launch.Template, v view.Values) (launch.Config, view.FieldErrors) {
	errs := view.FieldErrors{}
	cfg := launch.Config{Overrides: launch.Fields{}, Answers: map[string]any{}}

	d, err := tmpl.Descriptor()
	if err != nil {
		errs.Add("", "This controller cannot run that kind of template.")
		return launch.Config{}, errs
	}

	for _, spec := range d.Fields {
		if !tmpl.Promptable(spec.Name) {
			continue
		}
		raw := strings.TrimSpace(v.Get(spec.Name))
		if raw == "" {
			continue
		}

		var submitted any = raw
		switch spec.Type {
		case launch.TypeStringList:
			submitted = v.Tags(spec.Name)
		case launch.TypeMap:
			vars, err := parseVariables(raw)
			if err != nil {
				errs.Add(spec.Name, err.Error())
				continue
			}
			submitted = vars
		}

		// The kind's own normalizer, not a copy of its rules. A value it
		// refuses is reported beside the control that produced it, which is
		// what makes the resolver's ignored-field report empty by
		// construction for a launch made through this form.
		value, err := d.Normalize(spec.Name, submitted)
		if err != nil {
			errs.Add(spec.Name, fieldMessage(err))
			continue
		}
		cfg.Overrides[spec.Name] = value
	}

	for _, q := range tmpl.Survey.Questions {
		name := surveyPrefix + q.Variable
		if q.Type == launch.QuestionMultiSelect {
			if selected := v.Selected(name); len(selected) > 0 {
				cfg.Answers[q.Variable] = selected
			}
			continue
		}
		if raw := v.Get(name); strings.TrimSpace(raw) != "" {
			// Left as the submitted string: the survey coerces per question
			// type, so parsing here would be a second place deciding what
			// an integer answer is.
			cfg.Answers[q.Variable] = raw
		}
	}

	if len(cfg.Overrides) == 0 {
		cfg.Overrides = nil
	}
	if len(cfg.Answers) == 0 {
		cfg.Answers = nil
	}
	return cfg, errs
}

// parseVariables reads a key=value block into a variable map.
//
// The same shape AWX's own extra-variables box accepts in its simplest
// form, chosen over YAML or JSON because this control is typed into by
// somebody launching one job: a syntax error in a bracketed document is a
// worse failure at that moment than a line that does not contain an equals
// sign.
func parseVariables(raw string) (map[string]any, error) {
	out := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("each line must read name=value: %q does not", line)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}

// fieldMessage renders a domain refusal for a form.
//
// The domain's own sentence, with its package prefix removed: it names the
// variable and says what was wrong, which is what makes it worth showing at
// all, but "launch:" is a Go package name and means nothing to whoever is
// reading the form.
func fieldMessage(err error) string {
	msg := err.Error()
	if _, after, found := strings.Cut(msg, "launch: "); found {
		msg = after
	}
	if msg == "" {
		return "One of the survey answers is not valid for its question."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

// copyAction duplicates a template under a new name.
//
// It exists because varying a template is how people actually work:
// duplicating one to test a change without touching the production copy.
// It is also the supported way to point a saved definition at different
// code, since the kind, the definition and the inventory are not editable.
//
// The saved launch configurations do not travel with the copy. Those are
// one operator's answers, secrets among them, and duplicating them would
// move a stored password onto an object with its own separate grants.
func copyAction(store launch.Store) view.RecordAction {
	return view.RecordAction{
		Name:     "copy",
		Label:    "Copy",
		Heading:  "Copy template",
		Endpoint: &apispec.CopyTemplate,
		Fields: []view.Field{
			{
				Name: "name", Label: "NEW NAME", Kind: view.KindText,
				Required: true, MaxLen: 253, Autocomplete: "off", InForm: true,
				Help: "What the copy is called. Unique within the same organization, so it cannot silently overwrite the original.",
			},
		},
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			source, ok := load(ctx, store, id)
			if !ok {
				return "", view.FieldErrors{"name": {"That template no longer exists. Reload the list."}}, nil
			}

			source.ID = 0
			source.Name = strings.TrimSpace(v.Get("name"))
			// Cleared so the store derives it again from the inventory
			// rather than carrying a tenant across a write it did not
			// check.
			source.OrganizationID = 0

			created, err := store.Create(ctx, source)
			switch {
			case errors.Is(err, launch.ErrExists):
				return "", view.FieldErrors{"name": {"A template with that name already exists in this organization."}}, nil
			case errors.Is(err, launch.ErrInvalidTemplate):
				return "", view.FieldErrors{"name": {fieldMessage(err)}}, nil
			case err != nil:
				return "", nil, err
			}

			return "/ui/templates/" + strconv.Itoa(created.ID), nil, nil
		},
	}
}

// templateID2String keeps load's string-parented signature usable from a
// path that has already parsed the id.
func templateID2String(id int) string { return strconv.Itoa(id) }
