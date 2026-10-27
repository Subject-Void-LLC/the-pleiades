// Package clidialog reads the parameters a network CLI method takes for a
// command that asks questions (prompts, ask, expect_disconnect) into
// pkg/netcli's DialogOptions, wiring ask to a model through pkg/aitask. It
// is shared by net.cli.command and net.ios.command, which a Collection's
// rule (pkg/ only) keeps from sharing an internal/ helper.
//
// Everything is read, and ask's provider resolved, before a method opens
// its device's session, so a parameter that does not read never leaves a
// command half answered.
package clidialog

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// The parameters.
const (
	ParamPrompts          = "prompts"
	ParamAsk              = "ask"
	ParamExpectDisconnect = "expect_disconnect"
)

// The keys of an entry in prompts, and of ask beside a chooser's own.
const (
	keyMatch   = "match"
	keyAnswer  = "answer"
	keyRefuse  = "refuse"
	keyChoices = "choices"
	keyMax     = "max"
	keyShape   = "shape"
	keyID      = "id"
	keyMeans   = "means"
)

// MaxAsk is the most questions ask may let a model answer in one command.
const MaxAsk = 10

// choiceID is what a choice's id may be: what a model answers with, and
// what a review rule names.
var choiceID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// now is the clock a chooser's records are read against.
var now = time.Now

// Uses reports whether params ask for a dialog at all, so a method keeps
// its plain command path when they do not.
func Uses(params map[string]any) bool {
	for _, k := range []string{ParamPrompts, ParamAsk, ParamExpectDisconnect} {
		if v, ok := params[k]; ok && v != nil {
			return true
		}
	}
	return false
}

// Read reads params' dialog parameters for the method fqcn. ask's
// provider is resolved through the run's project, so ask runs on the
// Crawl tier only, and is refused before any contact elsewhere.
func Read(ctx context.Context, rc sdk.RunbookContext, fqcn string, params map[string]any) (netcli.DialogOptions, error) {
	var opts netcli.DialogOptions
	var err error
	if opts.ExpectDisconnect, err = sdk.BoolParamOr(params, ParamExpectDisconnect, false); err != nil {
		return opts, err
	}
	if opts.Prompts, err = readPrompts(rc, params[ParamPrompts]); err != nil {
		return opts, err
	}
	if raw, ok := params[ParamAsk]; ok && raw != nil {
		if opts.Ask, err = readAsk(ctx, rc, fqcn, raw); err != nil {
			return opts, fmt.Errorf("%s: %w", ParamAsk, err)
		}
	}
	return opts, nil
}

// ReadConfig reads the dialog parameters a method applying configuration
// lines takes, prompts and ask, as Read does. expect_disconnect is refused:
// a configuration line that ends the session has failed.
func ReadConfig(ctx context.Context, rc sdk.RunbookContext, fqcn string, params map[string]any) (netcli.DialogOptions, error) {
	if _, set := params[ParamExpectDisconnect]; set {
		return netcli.DialogOptions{}, fmt.Errorf("%s does not apply to configuration lines: a line that ends the session has failed", ParamExpectDisconnect)
	}
	return Read(ctx, rc, fqcn, params)
}

// readPrompts reads prompts: a list of {match, answer} or {match, refuse:
// true}. An answer holding a secret reference is resolved from rc's named
// secrets (sdk.ResolveSecretRefs, which adds the value to the run's masking
// set) and marked Secret, so it may answer a password question and its
// exchange records netcli.SecretAnswer, never the value.
func readPrompts(rc sdk.RunbookContext, raw any) ([]netcli.Prompt, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is a list of {%s, %s} or {%s, %s: true}", ParamPrompts, keyMatch, keyAnswer, keyMatch, keyRefuse)
	}
	out := make([]netcli.Prompt, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", ParamPrompts, i)
		}
		if err := onlyKeys(m, keyMatch, keyAnswer, keyRefuse); err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", ParamPrompts, i, err)
		}
		// Only an answer is resolved: a reference in match would be compiled
		// as a pattern, never filled in.
		if text, _ := m[keyMatch].(string); secretref.Has(text) {
			return nil, fmt.Errorf("%s[%d]: a secret reference belongs in %s, never in %s", ParamPrompts, i, keyAnswer, keyMatch)
		}
		re, err := pattern(m, keyMatch, true)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", ParamPrompts, i, err)
		}
		refuse, err := sdk.BoolParamOr(m, keyRefuse, false)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", ParamPrompts, i, err)
		}
		answer, hasAnswer := m[keyAnswer].(string)
		if _, set := m[keyAnswer]; set && !hasAnswer {
			return nil, fmt.Errorf("%s[%d]: %s is text (\"\" is Enter alone)", ParamPrompts, i, keyAnswer)
		}
		if refuse == hasAnswer {
			return nil, fmt.Errorf("%s[%d]: give %s (\"\" is Enter alone) or %s: true, not both or neither", ParamPrompts, i, keyAnswer, keyRefuse)
		}
		secret := secretref.Has(answer)
		if secret {
			if answer, _, err = sdk.ResolveSecretRefs(rc, answer); err != nil {
				return nil, fmt.Errorf("%s[%d]: %s: %w", ParamPrompts, i, keyAnswer, err)
			}
		}
		out = append(out, netcli.Prompt{Match: re, Answer: answer, Refuse: refuse, Secret: secret})
	}
	return out, nil
}

// readAsk reads ask: the choices a model may pick from, its limit and the
// shape of a question, and the rest as a chooser's parameters.
func readAsk(ctx context.Context, rc sdk.RunbookContext, fqcn string, raw any) (*netcli.Ask, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("is a mapping: %s, the model's provider and options", keyChoices)
	}
	choices, err := readChoices(m[keyChoices])
	if err != nil {
		return nil, err
	}
	limit, set, err := sdk.IntParam(m, keyMax)
	if err != nil {
		return nil, err
	}
	if set && (limit < 1 || limit > MaxAsk) {
		return nil, fmt.Errorf("%s is 1 to %d", keyMax, MaxAsk)
	}
	shape, err := pattern(m, keyShape, false)
	if err != nil {
		return nil, err
	}
	call := make(map[string]any, len(m))
	for k, v := range m {
		if k != keyChoices && k != keyMax && k != keyShape {
			call[k] = v
		}
	}
	chooser, err := aitask.NewChooser(ctx, rc, fqcn, call, now)
	if err != nil {
		return nil, err
	}
	options := make([]aitask.Option, len(choices))
	for i, c := range choices {
		options[i] = aitask.Option{ID: c.ID, Means: c.Means}
	}
	choose := func(ctx context.Context, question string, _ []netcli.Choice) (netcli.Decision, error) {
		pick, err := chooser.Choose(ctx, rc, question, options)
		if err != nil {
			return netcli.Decision{}, err
		}
		return netcli.Decision{ID: pick.ID, Reason: pick.Reason, Detail: pick.Detail}, nil
	}
	return &netcli.Ask{Choices: choices, Choose: choose, Max: limit, Shape: shape}, nil
}

// readChoices reads ask's choices: a list of {id, answer, means}, each
// required, every id distinct.
func readChoices(raw any) ([]netcli.Choice, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("%s is a non-empty list of {%s, %s, %s}", keyChoices, keyID, keyAnswer, keyMeans)
	}
	out := make([]netcli.Choice, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", keyChoices, i)
		}
		if err := onlyKeys(m, keyID, keyAnswer, keyMeans); err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", keyChoices, i, err)
		}
		id, _ := m[keyID].(string)
		answer, hasAnswer := m[keyAnswer].(string)
		means, _ := m[keyMeans].(string)
		switch {
		case !choiceID.MatchString(id) || id == netcli.ChoiceRefuse || id == netcli.ChoiceWait:
			return nil, fmt.Errorf("%s[%d]: %s is lowercase letters, digits and _, up to 40, and not %s or %s", keyChoices, i, keyID, netcli.ChoiceRefuse, netcli.ChoiceWait)
		case !hasAnswer:
			return nil, fmt.Errorf("%s[%d]: %s is text (\"\" is Enter alone)", keyChoices, i, keyAnswer)
		case means == "":
			return nil, fmt.Errorf("%s[%d]: %s says what the answer does, for the model to read", keyChoices, i, keyMeans)
		case slices.ContainsFunc(out, func(c netcli.Choice) bool { return c.ID == id }):
			return nil, fmt.Errorf("%s[%d]: %s %s is listed twice", keyChoices, i, keyID, id)
		}
		out = append(out, netcli.Choice{ID: id, Answer: answer, Means: means})
	}
	return out, nil
}

// pattern compiles m[key] as RE2, required or not.
func pattern(m map[string]any, key string, required bool) (*regexp.Regexp, error) {
	raw, set := m[key]
	if !set || raw == nil {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	text, ok := raw.(string)
	if !ok || text == "" {
		return nil, fmt.Errorf("%s is a regular expression (RE2)", key)
	}
	re, err := regexp.Compile(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return re, nil
}

// onlyKeys refuses a key outside allowed, so a misspelled one is never
// read as absent.
func onlyKeys(m map[string]any, allowed ...string) error {
	for k := range m {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("%s is not one of %v", k, allowed)
		}
	}
	return nil
}

// Stats are a dialog's result as a method's stats: each question answered
// and how, and whether the device closed the session.
func Stats(res netcli.DialogResult) map[string]any {
	exchanges := make([]any, len(res.Exchanges))
	for i, ex := range res.Exchanges {
		entry := map[string]any{"question": ex.Question, "answer": ex.Answer}
		if ex.Prompt >= 0 {
			entry["prompt"] = ex.Prompt
		} else {
			maps.Copy(entry, ex.Detail)
			entry["choice"], entry["reason"] = ex.Choice, ex.Reason
		}
		exchanges[i] = entry
	}
	return map[string]any{"exchanges": exchanges, "disconnected": res.Disconnected}
}

// Params documents the dialog parameters, as each method's reference lists
// them.
func Params() []collection.Param {
	return []collection.Param{
		{Name: ParamPrompts, Type: collection.ParamTypeListOfDict, Description: "Questions the command may ask, each {match, answer} or {match, refuse: true}. match is RE2 found on the line the device waits on (case-sensitive unless it starts with (?i)); answer is typed and then Enter, \"\" being Enter alone; refuse closes the session, which abandons the command, and fails the task. The first unanswered entry matching is used and each answers once, so a question the device asks again after its answer fails the task; list it twice to answer it twice. Text written in the task is never sent to a question asking for a password, passphrase or secret; answer one with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret), which is resolved just before it is typed, may stand only with literal text, and is recorded in exchanges as (secret).", SecretRefs: true},
		{Name: ParamAsk, Type: collection.ParamTypeDict, Description: "A model for questions no prompts entry matches, on the Crawl tier: choices, a list of {id, answer, means} the model picks from by id (it never types its own answer, and may refuse, which fails the task, or say the line was no question); max, how many it may answer (1 to 10, default 3); shape, the RE2 a waiting line must match to be asked about (default: ending in ?, ] or ]:); and ai.chat's own provider, model, residency, prompt or prompt_file (a built-in prompt when neither), signing, review, usage_sink and call options. Its provider is resolved before the device is reached. A held answer fails the task as held, which abandons the command; decided with pleiades ai review, running again reuses the decision for that same question."},
		{Name: ParamExpectDisconnect, Type: collection.ParamTypeBool, Default: "false", Description: "Count the device closing the session after at least one answer as success, which is how a reload ends. A close before any answer still fails."},
	}
}

// ConfigParams documents the dialog parameters a method applying
// configuration lines takes: Params without expect_disconnect.
func ConfigParams() []collection.Param {
	return slices.DeleteFunc(Params(), func(p collection.Param) bool { return p.Name == ParamExpectDisconnect })
}

// ConfigReturns documents what a configuration dialog returns: each
// question its lines asked.
func ConfigReturns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: "exchanges", Type: "list of dict", Returned: "when prompts or ask is set", Description: "Each question a line asked and what was typed: question and answer, with prompt (the entry's index) or, for the model, choice, reason, provider, model, confidence, usage and review."},
	}
}

// Returns documents the dialog's stats.
func Returns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: "exchanges", Type: "list of dict", Returned: "when prompts, ask or expect_disconnect is set", Description: "Each question the device asked and what was typed: question and answer, with prompt (the entry's index) or, for the model, choice, reason, provider, model, confidence, usage and review."},
		{Name: "disconnected", Type: "bool", Returned: "when prompts, ask or expect_disconnect is set", Description: "Whether the device closed the session after an answer (expect_disconnect)."},
	}
}
