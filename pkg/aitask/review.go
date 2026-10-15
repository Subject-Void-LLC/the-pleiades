// Package aitask: review rules (human in the loop). A use or a task names
// thresholds on an answer; one that matches holds the answer for a
// person's decision through the run's sdk.Reviewer before any later task
// can read it.
package aitask

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ParamReview is the parameter, and the routing table's use key, that
// names review rules.
const ParamReview = "review"

// Bounds on review rules.
const (
	maxRules      = 32
	maxRuleRegexp = 1024
)

// The comparisons a field rule makes.
var fieldComparisons = []string{"below", "above", "equals", "in", "not_in"}

// Rule is one review threshold. A rule that cannot be evaluated against an
// answer (a cost with no price, a field in an answer that is not JSON)
// matches: a threshold that cannot be checked is not passed.
type Rule struct {
	kind   string
	number float64
	re     *regexp.Regexp
	path   string
	cmp    string
	values []string // JSON text of each value compared against
	text   string   // the rule as written, for its reason
}

// String is the rule as a review's reason names it.
func (r Rule) String() string { return r.text }

// ParseReview reads review rules: a list of one-key mappings
//
//	cost_over: <number>          output_tokens_over: <whole number>
//	answer_matches: <RE2>        stop_reason: [<reason>, ...]
//	fallback: true               unsigned: true               always: true
//	field: <dotted path> with one of below, above (numbers), equals, in, not_in
//
// refusing an unknown key, a rule with two criteria, and a flag set to
// false (which would read as a threshold and hold nothing).
func ParseReview(raw any) ([]Rule, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 || len(list) > maxRules {
		return nil, fmt.Errorf("%s is a list of 1 to %d rules", ParamReview, maxRules)
	}
	out := make([]Rule, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", ParamReview, i)
		}
		r, err := parseRule(m)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", ParamReview, i, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// parseRule reads one rule.
func parseRule(m map[string]any) (Rule, error) {
	if _, isField := m["field"]; isField {
		return parseFieldRule(m)
	}
	if len(m) != 1 {
		return Rule{}, errors.New("a rule names one criterion")
	}
	for kind, v := range m {
		r := Rule{kind: kind}
		switch kind {
		case "cost_over", "output_tokens_over":
			f, ok := number(v)
			if !ok || f < 0 || (kind == "output_tokens_over" && f != float64(int64(f))) {
				return Rule{}, fmt.Errorf("%s is a non-negative number", kind)
			}
			r.number, r.text = f, fmt.Sprintf("%s %v", kind, f)
		case "answer_matches":
			s, _ := v.(string)
			if s == "" || len(s) > maxRuleRegexp {
				return Rule{}, fmt.Errorf("answer_matches is a regular expression of 1 to %d bytes", maxRuleRegexp)
			}
			re, err := regexp.Compile(s)
			if err != nil {
				return Rule{}, fmt.Errorf("answer_matches: %w", err)
			}
			r.re, r.text = re, "answer_matches "+strconv.Quote(s)
		case "stop_reason":
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return Rule{}, errors.New("stop_reason is a list of the provider's stop reasons")
			}
			for _, x := range list {
				s, _ := x.(string)
				if s == "" {
					return Rule{}, errors.New("stop_reason holds an empty reason")
				}
				r.values = append(r.values, s)
			}
			r.text = "stop_reason in [" + strings.Join(r.values, ", ") + "]"
		case "fallback", "unsigned", "always":
			if b, _ := v.(bool); !b {
				return Rule{}, fmt.Errorf("%s is true or absent: false would hold nothing", kind)
			}
			r.text = kind
		default:
			return Rule{}, fmt.Errorf("%s is not a review criterion (cost_over, output_tokens_over, answer_matches, field, stop_reason, fallback, unsigned, always)", kind)
		}
		return r, nil
	}
	return Rule{}, errors.New("a rule is empty")
}

// parseFieldRule reads a field rule: a dotted path and one comparison.
func parseFieldRule(m map[string]any) (Rule, error) {
	path, _ := m["field"].(string)
	if path == "" || strings.Contains(path, "..") || strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return Rule{}, errors.New("field is a dotted path of JSON object keys")
	}
	if len(m) != 2 {
		return Rule{}, fmt.Errorf("field takes one of %s", strings.Join(fieldComparisons, ", "))
	}
	r := Rule{kind: "field", path: path}
	for cmp, v := range m {
		if cmp == "field" {
			continue
		}
		r.cmp = cmp
		switch cmp {
		case "below", "above":
			f, ok := number(v)
			if !ok {
				return Rule{}, fmt.Errorf("%s is a number", cmp)
			}
			r.number = f
			r.text = fmt.Sprintf("field %s %s %v", path, cmp, f)
		case "equals":
			b, err := json.Marshal(v)
			if err != nil {
				return Rule{}, err
			}
			r.values = []string{string(b)}
			r.text = fmt.Sprintf("field %s equals %s", path, b)
		case "in", "not_in":
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return Rule{}, fmt.Errorf("%s is a list of values", cmp)
			}
			for _, x := range list {
				b, err := json.Marshal(x)
				if err != nil {
					return Rule{}, err
				}
				r.values = append(r.values, string(b))
			}
			r.text = fmt.Sprintf("field %s %s [%s]", path, cmp, strings.Join(r.values, ", "))
		default:
			return Rule{}, fmt.Errorf("%s is not one of %s", cmp, strings.Join(fieldComparisons, ", "))
		}
	}
	return r, nil
}

// number reads a YAML or JSON number.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, n == n
	}
	return 0, false
}

// Outcome is what a call came to, as the rules read it.
type Outcome struct {
	Answer       string
	StopReason   string
	OutputTokens int
	Cost         float64
	Priced       bool
	// Verified is whether the prompt's signature was verified.
	Verified bool
	// Fallback is whether a provider other than the use's first answered.
	Fallback bool
}

// Match reports why the rule holds o, or "" when it does not.
func (r Rule) Match(o Outcome) string {
	switch r.kind {
	case "cost_over":
		if !o.Priced {
			return r.text + ": the call is not priced, so its cost cannot be checked"
		}
		if o.Cost > r.number {
			return fmt.Sprintf("%s: it cost %v", r.text, o.Cost)
		}
	case "output_tokens_over":
		if float64(o.OutputTokens) > r.number {
			return fmt.Sprintf("%s: it took %d", r.text, o.OutputTokens)
		}
	case "answer_matches":
		if r.re.MatchString(o.Answer) {
			return r.text
		}
	case "stop_reason":
		if slices.Contains(r.values, o.StopReason) {
			return fmt.Sprintf("%s: it stopped for %s", r.text, o.StopReason)
		}
	case "fallback":
		if o.Fallback {
			return "fallback: a provider other than the use's first answered"
		}
	case "unsigned":
		if !o.Verified {
			return "unsigned: the prompt's signature was not verified"
		}
	case "always":
		return "always"
	case "field":
		return r.matchField(o.Answer)
	}
	return ""
}

// matchField evaluates a field rule against the answer read as JSON.
func (r Rule) matchField(answer string) string {
	dec := json.NewDecoder(strings.NewReader(answer))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return r.text + ": the answer is not JSON"
	}
	for _, key := range strings.Split(r.path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return r.text + ": the answer has no " + r.path
		}
		if v, ok = obj[key]; !ok {
			return r.text + ": the answer has no " + r.path
		}
	}
	switch r.cmp {
	case "below", "above":
		n, ok := v.(json.Number)
		f, err := n.Float64()
		if !ok || err != nil {
			return r.text + ": " + r.path + " is not a number"
		}
		if (r.cmp == "below" && f < r.number) || (r.cmp == "above" && f > r.number) {
			return fmt.Sprintf("%s: it is %v", r.text, f)
		}
		return ""
	}
	got := canonicalValue(v)
	in := slices.Contains(r.values, got)
	if (r.cmp == "not_in") != in {
		return fmt.Sprintf("%s: it is %s", r.text, got)
	}
	return ""
}

// canonicalValue writes a decoded JSON value as encoding/json would
// marshal the rule's own value, so 0.9 and 0.90 compare equal.
func canonicalValue(v any) string {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			b, _ := json.Marshal(f)
			return string(b)
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Reasons is every rule's reason to hold o, none when it may be released.
func Reasons(rules []Rule, o Outcome) []string {
	var out []string
	for _, r := range rules {
		if why := r.Match(o); why != "" {
			out = append(out, why)
		}
	}
	return out
}

// keyExcluded are parameters that change how hard a call tries or where
// its record goes, not what it asks, so asking again with another value
// still finds the review.
var keyExcluded = map[string]bool{ParamTimeout: true, ParamRetries: true, ParamUsageSink: true}

// RequestKey is the SHA-256, in hex, of a request as asked: the method,
// where it went, the bundle it sent (by hash, so a new version is a new
// request), and every parameter that decides what was asked.
func RequestKey(method, target, bundleHash string, params map[string]any) (string, error) {
	kept := make(map[string]any, len(params))
	for k, v := range params {
		if !keyExcluded[k] {
			kept[k] = v
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"method": method, "target": target, "bundle": bundleHash, "params": kept}); err != nil {
		return "", fmt.Errorf("the request cannot be keyed for review: %w", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
