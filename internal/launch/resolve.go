package launch

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// Config is what a caller supplies at launch: a sparse bundle of values,
// in the two layers a launch can carry them.
//
// Sparse in both layers, and that is load bearing. An absent field inherits
// what the layer beneath it decided; a field present with an empty value
// sets it to empty. Collapsing those two would mean a saved configuration
// that omits `limit` silently clearing the template's.
type Config struct {
	// Saved is a stored launch configuration: the one a schedule holds, a
	// workflow node holds, or a relaunch reuses. Applied over the
	// template's defaults.
	Saved Fields

	// Overrides are what this particular launch supplied. Applied last, so
	// an operator standing at the form beats a configuration saved months
	// ago, which is the order AWX resolves the same layers in.
	Overrides Fields

	// Answers are this launch's survey answers, merged into extra
	// variables between the saved configuration and the launch overrides.
	Answers map[string]any

	// FilePolicy is the deployment's half of the file-answer rules, and it
	// is STAMPED BY THE DISPATCHER rather than supplied by whoever built
	// this Config. A caller who could set it could grant themselves the
	// deployment's consent, which is the one thing the two-gate design
	// exists to make impossible, so every launch path overwrites whatever
	// is here immediately before resolving.
	//
	// It lives on Config because Config is the only value that reaches
	// Template.Resolve without changing that method's signature, and
	// because its zero value is the refusing one: a Config nobody stamped
	// admits no program content.
	FilePolicy FilePolicy
}

// extraVarsField is the one field whose layers merge rather than replace.
//
// Two callers setting two different variables both mean it, so a launch
// supplying {"version": "2"} over a template supplying {"region": "eu"}
// must run with both. Every other field replaces: a launch supplying a
// limit means that limit instead of the template's, not both.
const extraVarsField = "extra_vars"

// Resolve folds a caller's configuration over this template's own
// defaults and returns the concrete run, plus every value the caller
// supplied that this template does not permit them to set.
//
// It does not fail on a value the template locked. Silently applying an
// unopened override is a privilege escalation; silently dropping one is a
// lie about what ran. Reporting it is the only remaining option, and it
// is why the second return value is not an error.
//
// The fold is pkg/policy.Resolve, consumed rather than reimplemented, which
// is Phase 21's own checklist item: "Consume the shared hierarchical policy
// resolver for prompt precedence. Do not write a private one." What this
// supplies is the combine function, which is where the interesting part
// lives: it is per-field Override for scalars and Union for extra
// variables, which is exactly the mixed-mode case pkg/policy's own doc
// comment describes rather than a shape it lacks.
//
// It fails only on things no configuration could fix: an unregistered kind,
// a value that is not what its field declares, a survey answer that
// violates its own schema. A field the template locked is not a failure; it
// is reported and the run proceeds with the template's value, because the
// alternatives are applying it (a privilege escalation) or dropping it
// silently (a lie about what ran).
func (t Template) Resolve(ctx context.Context, cfg Config) (Resolved, []IgnoredField, error) {
	if err := ctx.Err(); err != nil {
		return Resolved{}, nil, err
	}

	d, err := t.Descriptor()
	if err != nil {
		return Resolved{}, nil, err
	}

	// The mode is settled first and apart from everything else
	// (resolveMode), and the generic fold below never sees it.
	mode, err := resolveMode(d, []modeLayer{
		{name: LayerTemplate, fields: t.Defaults},
		{name: LayerSaved, fields: cfg.Saved},
		{name: LayerLaunch, fields: cfg.Overrides},
	})
	if err != nil {
		return Resolved{}, nil, err
	}
	t.Defaults = withoutMode(t.Defaults)
	cfg.Saved = withoutMode(cfg.Saved)
	cfg.Overrides = withoutMode(cfg.Overrides)

	var ignored []IgnoredField

	// combine folds one layer over what is accumulated so far. It is a
	// closure over ignored because policy.Resolve's combine returns only
	// the new accumulator: reporting is the caller's business, and a
	// resolver that could report would be a resolver with an opinion about
	// what a refusal means.
	combine := func(layerName string, gated bool) func(acc, next Fields) Fields {
		return func(acc, next Fields) Fields {
			for _, name := range next.Names() {
				spec, accepted := d.Field(name)
				if !accepted {
					ignored = append(ignored, IgnoredField{Name: name, Layer: layerName, Reason: ReasonUnknownField})
					continue
				}
				if gated && !t.Promptable(name) {
					ignored = append(ignored, IgnoredField{Name: name, Layer: layerName, Reason: ReasonLocked})
					continue
				}

				value, err := normalize(spec, next[name])
				if err != nil {
					// Recorded as ignored rather than returned, so one
					// malformed value does not discard the rest of a
					// launch. The message is the field's own, so a caller
					// can see which value was wrong and why.
					ignored = append(ignored, IgnoredField{Name: name, Layer: layerName, Reason: err.Error()})
					continue
				}

				if spec.Type == TypeMap {
					acc[name] = mergeMaps(acc.Map(name), value.(map[string]any))
					continue
				}
				acc[name] = value
			}
			return acc
		}
	}

	// The base is the template's own defaults, which are never gated: they
	// are what the template says, not what a caller is asking to change.
	base := t.Defaults.Clone()
	if base == nil {
		base = Fields{}
	}

	answers, err := t.Survey.Resolve(cfg.Answers, cfg.FilePolicy)
	if err != nil {
		return Resolved{}, nil, err
	}

	// The chain, least specific first. Survey answers sit between the
	// saved configuration and this launch's overrides, matching AWX's own
	// documented precedence: launch variables beat survey answers, which
	// beat what the template was saved with.
	// The survey layer is ungated, and it is the one exception in this
	// fold. A survey question is already the template author's decision to
	// prompt for that value, so requiring extra_vars to also be listed in
	// Prompts would mean every survey needed a second, invisible opt-in,
	// and a template with a survey and no such entry would silently
	// discard every answer it collected.
	//
	// The safety property is unchanged. A survey can only ever write into
	// extra variables, under variable names the template author declared
	// in the survey itself, which is strictly narrower than being able to
	// set an arbitrary launch field.
	layers := []struct {
		layer policy.Layer[Fields]
		gated bool
	}{
		{policy.Layer[Fields]{Name: LayerSaved, Value: cfg.Saved}, true},
		{policy.Layer[Fields]{Name: LayerSurvey, Value: surveyFields(answers)}, false},
		{policy.Layer[Fields]{Name: LayerLaunch, Value: cfg.Overrides}, true},
	}

	result := base
	for _, l := range layers {
		result = policy.Resolve(policy.ModeOverride, result,
			[]policy.Layer[Fields]{l.layer}, combine(l.layer.Name, l.gated)).Value
	}

	sortIgnored(ignored)

	// Recorded on every run of a kind that has a mode, a real one
	// included, so the job says which it was rather than leaving a reader
	// to infer it from an absence.
	if _, accepted := d.Field(ModeField); accepted {
		result[ModeField] = string(mode)
	}

	return Resolved{
		Mode:              mode,
		Kind:              t.KindName,
		Adapter:           d.Adapter,
		Definition:        t.Definition,
		InventoryID:       t.InventoryID,
		OrganizationID:    t.OrganizationID,
		Fields:            result,
		ExtraVars:         result.Map(extraVarsField),
		AllowSimultaneous: t.AllowSimultaneous,
		// Copied through untouched, and deliberately not merged with
		// anything a caller supplied: cfg carries no credential ids and
		// cannot, because which credentials a definition runs with is the
		// template's decision rather than the launching operator's.
		CredentialIDs: append([]int(nil), t.CredentialIDs...),
	}, ignored, nil
}

// surveyFields wraps resolved survey answers as an extra-variables layer.
//
// Answers are variables, not fields of their own: a survey exists to fill
// in values a playbook or runbook reads, and giving them a second namespace
// would mean a template author choosing between two ways to set the same
// thing.
func surveyFields(answers map[string]any) Fields {
	if len(answers) == 0 {
		return nil
	}
	return Fields{extraVarsField: answers}
}

// mergeMaps unions next over acc, next winning on a shared key.
func mergeMaps(acc, next map[string]any) map[string]any {
	out := make(map[string]any, len(acc)+len(next))
	for k, v := range acc {
		out[k] = v
	}
	for k, v := range next {
		out[k] = v
	}
	return out
}
