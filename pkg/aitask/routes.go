// Package aitask: routing tables, which name each use's providers in
// fallback order with the residency and signing policy the use requires.
package aitask

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

// The parameters a routed call takes, beside a call's.
const (
	ParamUse    = "use"
	ParamRoutes = "routes"
)

// maxRouteSteps bounds a use's fallback order.
const maxRouteSteps = 16

// useName is the form of a use's name.
var useName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Step is one provider in a use's fallback order, and the model to ask it
// for.
type Step struct {
	Provider string
	Model    string
}

// Use is one use in a routing table: its residency (required), its
// signing policy, its review rules, and its providers in fallback order.
type Use struct {
	Name      string
	Residency string
	Signing   promptsign.Policy
	// Review are the use's review rules, which a task's can add to and
	// never take from.
	Review []Rule
	Steps  []Step
}

// ParseRoutes reads a routing table: a mapping of use names to
//
//	residency: local | hosted        (required)
//	signing: off | optional | required   (optional when absent)
//	review: [<rule>, ...]           (ParseReview)
//	providers:
//	  - {provider: <ai_provider device>, model: <model>}
//
// Every key is checked: an unknown one, a use with no providers, or one
// listing the same provider and model twice, is refused.
func ParseRoutes(raw any) (map[string]Use, error) {
	table, ok := raw.(map[string]any)
	if !ok || len(table) == 0 {
		return nil, fmt.Errorf("%s is a mapping of uses, each with residency and providers", ParamRoutes)
	}
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	out := map[string]Use{}
	for _, name := range names {
		u, err := parseUse(name, table[name])
		if err != nil {
			return nil, fmt.Errorf("%s: use %s: %w", ParamRoutes, name, err)
		}
		out[name] = u
	}
	return out, nil
}

// parseUse reads one use.
func parseUse(name string, raw any) (Use, error) {
	if !useName.MatchString(name) {
		return Use{}, fmt.Errorf("the name is not lower-case letters, digits, - and _")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Use{}, fmt.Errorf("it is not a mapping")
	}
	for k := range m {
		switch k {
		case "residency", "signing", "review", "providers":
		default:
			return Use{}, fmt.Errorf("%s is not residency, signing, review or providers", k)
		}
	}
	u := Use{Name: name}
	u.Residency, _ = m["residency"].(string)
	if u.Residency != ResidencyLocal && u.Residency != ResidencyHosted {
		return Use{}, fmt.Errorf("residency must be %s or %s", ResidencyLocal, ResidencyHosted)
	}
	signing, _ := m["signing"].(string)
	if _, present := m["signing"]; present && signing == "" {
		return Use{}, fmt.Errorf("signing must be off, optional or required")
	}
	var err error
	if u.Signing, err = promptsign.ParsePolicy(signing); err != nil {
		return Use{}, err
	}
	if u.Review, err = ParseReview(m["review"]); err != nil {
		return Use{}, err
	}
	steps, ok := m["providers"].([]any)
	if !ok || len(steps) == 0 || len(steps) > maxRouteSteps {
		return Use{}, fmt.Errorf("providers is a list of 1 to %d {provider, model}", maxRouteSteps)
	}
	seen := map[Step]bool{}
	for i, s := range steps {
		sm, ok := s.(map[string]any)
		provider, _ := sm["provider"].(string)
		model, _ := sm["model"].(string)
		if !ok || len(sm) != 2 || provider == "" || model == "" {
			return Use{}, fmt.Errorf("providers[%d] must be a mapping of provider and model, both set", i)
		}
		step := Step{Provider: provider, Model: model}
		if seen[step] {
			return Use{}, fmt.Errorf("providers lists %s with %s twice", provider, model)
		}
		seen[step] = true
		u.Steps = append(u.Steps, step)
	}
	return u, nil
}
