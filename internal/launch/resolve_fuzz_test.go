package launch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// FuzzPromptResolution is Phase 21's own fuzz item, and it targets the one
// invariant the whole prompting matrix exists to hold:
//
//	no value a caller supplies for a field the template did not open can
//	ever reach the resolved run.
//
// Everything else about Resolve is a convenience. This is the security
// property: the fields being gated are how many devices are worked on at
// once, how long a task may run, and which subset of an inventory is
// touched, so an override that slipped through a lock would change the
// blast radius of somebody else's saved change.
//
// The fuzzer drives the matrix rather than the parser: which fields are
// open, which layer a value arrives in, and what the value is. A string
// fuzzer over one input would have explored the value space and none of the
// combinations, and the combinations are where a gate goes missing.
func FuzzPromptResolution(f *testing.F) {
	// The runbook kind's own fields, in a fixed order so a bitmask means
	// the same thing across runs.
	fields := []string{"limit", "verbosity", "forks", "timeout", "extra_vars", "labels"}

	// Seeds: no fields open, all open, and a few mixtures, each against
	// each layer.
	for _, mask := range []uint8{0, 0b111111, 0b000001, 0b101010, 0b010101} {
		for _, layer := range []uint8{0, 1, 2} {
			f.Add(mask, layer, "edge-*", 3)
		}
	}
	// Values that have caused trouble at other boundaries here.
	f.Add(uint8(0b000111), uint8(2), "", 0)
	f.Add(uint8(0b111000), uint8(1), "../../etc/passwd", -1)
	f.Add(uint8(0b010010), uint8(0), "*", 1<<30)

	f.Fuzz(func(t *testing.T, promptMask uint8, layerPick uint8, text string, number int) {
		tmpl := launch.Template{
			Name:           "fuzzed",
			KindName:       "runbook",
			Definition:     "fuzzed-runbook",
			InventoryID:    1,
			OrganizationID: 1,
			Defaults: launch.Fields{
				"limit":      "template-limit",
				"verbosity":  1,
				"forks":      5,
				"timeout":    600,
				"extra_vars": map[string]any{"from": "template"},
				"labels":     []string{"template"},
			},
		}

		open := map[string]bool{}
		for i, name := range fields {
			if promptMask&(1<<uint(i)) != 0 {
				tmpl.Prompts = append(tmpl.Prompts, name)
				open[name] = true
			}
		}

		// One supplied value per field, every one of them different from
		// the template's own, so "the override was applied" and "the
		// template's value survived" are always distinguishable.
		supplied := launch.Fields{
			"limit":      text,
			"verbosity":  number,
			"forks":      number,
			"timeout":    number,
			"extra_vars": map[string]any{"from": "caller", "text": text},
			"labels":     []string{text},
		}

		cfg := launch.Config{}
		switch layerPick % 3 {
		case 0:
			cfg.Saved = supplied
		case 1:
			cfg.Overrides = supplied
		default:
			cfg.Saved, cfg.Overrides = supplied, supplied
		}

		resolved, ignored, err := tmpl.Resolve(context.Background(), cfg)
		if err != nil {
			// The only failures Resolve is allowed is an unregistered kind
			// or a bad survey answer, and this template has neither.
			t.Fatalf("Resolve failed on a well-formed template: %v", err)
		}

		// The invariant. Every locked field must still hold exactly what
		// the template said, whatever was supplied and from wherever.
		for _, name := range fields {
			if open[name] {
				continue
			}
			switch name {
			case "limit":
				if got := resolved.Fields.String(name); got != "template-limit" {
					t.Fatalf("locked %q resolved to %q, want the template's value", name, got)
				}
			case "verbosity":
				if got := resolved.Fields.Int(name); got != 1 {
					t.Fatalf("locked %q resolved to %d, want the template's 1", name, got)
				}
			case "forks":
				if got := resolved.Fields.Int(name); got != 5 {
					t.Fatalf("locked %q resolved to %d, want the template's 5", name, got)
				}
			case "timeout":
				if got := resolved.Fields.Int(name); got != 600 {
					t.Fatalf("locked %q resolved to %d, want the template's 600", name, got)
				}
			case "extra_vars":
				if got := resolved.ExtraVars["from"]; got != "template" {
					t.Fatalf("locked %q took the caller's value %v", name, got)
				}
				if _, leaked := resolved.ExtraVars["text"]; leaked {
					t.Fatalf("locked %q let the caller add a variable", name)
				}
			case "labels":
				got := resolved.Fields.List(name)
				if len(got) != 1 || got[0] != "template" {
					t.Fatalf("locked %q resolved to %v, want the template's labels", name, got)
				}
			}
		}

		// And nothing was dropped in silence: whatever did not apply was
		// reported, so a caller can tell what ran from what they asked for.
		reported := map[string]bool{}
		for _, ig := range ignored {
			reported[ig.Name] = true
			if ig.Name == "" || ig.Reason == "" || ig.Layer == "" {
				t.Fatalf("an ignored field was reported with a hole in it: %+v", ig)
			}
		}
		suppliedInSomeLayer := cfg.Saved != nil || cfg.Overrides != nil
		for _, name := range fields {
			if open[name] || !suppliedInSomeLayer {
				continue
			}
			if !reported[name] {
				t.Fatalf("locked %q was dropped without being reported", name)
			}
		}
	})
}

// FuzzSurveyAnswers targets the other half: an answer that violates its
// question's schema must be refused rather than reaching extra variables.
//
// A survey is the one path by which a launching operator writes variables
// the template author did not write, so what bounds it is the schema alone.
func FuzzSurveyAnswers(f *testing.F) {
	f.Add("17.6", 10, uint8(0))
	f.Add("", 0, uint8(1))
	f.Add("18.0", -1, uint8(2))
	f.Add("17.3", 1<<30, uint8(3))

	f.Fuzz(func(t *testing.T, choice string, number int, shape uint8) {
		tmpl := launch.Template{
			Name: "surveyed", KindName: "runbook", Definition: "surveyed-runbook",
			InventoryID: 1, OrganizationID: 1,
			Survey: launch.Survey{
				Enabled: true,
				Questions: []launch.Question{
					{Variable: "version", Type: launch.QuestionChoice, Required: true,
						Choices: []string{"17.3", "17.6"}},
					{Variable: "batch", Type: launch.QuestionInteger, Min: 1, Max: 50},
				},
			},
		}
		if err := tmpl.Validate(); err != nil {
			t.Fatalf("the fuzz template does not validate: %v", err)
		}

		answers := map[string]any{}
		if shape&1 == 0 {
			answers["version"] = choice
		}
		if shape&2 == 0 {
			answers["batch"] = number
		}

		resolved, _, err := tmpl.Resolve(context.Background(), launch.Config{Answers: answers})
		if err != nil {
			if !errors.Is(err, launch.ErrSurveyAnswer) {
				t.Fatalf("Resolve failed with %v, want a survey answer error or success", err)
			}
			return
		}

		// If it succeeded, everything that reached the variables satisfies
		// its own schema. A value outside the choices, or outside the
		// bounds, must not be here.
		if v, ok := resolved.ExtraVars["version"]; ok {
			if v != "17.3" && v != "17.6" {
				t.Fatalf("an answer outside its choices reached extra variables: %v", v)
			}
		} else {
			t.Fatal("a required answer was accepted as absent")
		}
		if v, ok := resolved.ExtraVars["batch"]; ok {
			n, isInt := v.(int)
			if !isInt || n < 1 || n > 50 {
				t.Fatalf("an out-of-range answer reached extra variables: %v", v)
			}
		}
	})
}
