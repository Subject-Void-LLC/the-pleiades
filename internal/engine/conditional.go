package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// StringList is a slice of strings that also accepts a single bare string
// on decode, in both YAML and JSON. This mirrors Ansible's own when: keyword,
// which has always accepted either a scalar string or a list of strings.
type StringList []string

// UnmarshalYAML decodes a YAML scalar node into a one-element StringList, or
// a YAML sequence node into a StringList of the same length. Any other node
// kind (mapping, and so on) is rejected with a clear error, since neither
// shape means anything for a when-style condition list.
func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// A bare string, e.g. `when: stat.ok == true`, becomes one entry.
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		*s = StringList{single}
		return nil
	case yaml.SequenceNode:
		// A YAML list, e.g. `when: [a, b]`, decodes element for element.
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*s = list
		return nil
	default:
		return fmt.Errorf("expected a string or a list of strings, got a YAML node of kind %v", value.Kind)
	}
}

// UnmarshalJSON decodes a JSON string into a one-element StringList, or a
// JSON array of strings into a StringList of the same length. Any other JSON
// shape is rejected with a clear error.
func (s *StringList) UnmarshalJSON(data []byte) error {
	// Try the bare-string shape first, since it is the common case for a
	// single condition.
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = StringList{single}
		return nil
	}

	// Fall back to a JSON array of strings.
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}

	return fmt.Errorf("expected a JSON string or an array of strings, got: %s", string(data))
}

// Conditional is the shared conditional model embedded in Task (via its
// inline Conditional field) as a per-task skip condition, evaluated against
// every task's when, when_or, or when_cel keys. It offers three ways to
// express a condition, from simplest to most powerful:
//
//   - When mirrors Ansible's when: keyword exactly: a bare string is one
//     expression, a list of strings is multiple expressions ANDed together.
//   - WhenOr has the same shape as When, but list items are ORed together
//     instead of ANDed. Ansible has no equivalent keyword; this field exists
//     purely so OR is reachable without needing to know CEL.
//   - WhenCEL is a single raw CEL expression, used verbatim with no wrapping
//     or joining. It is the escape hatch for logic When and WhenOr cannot
//     express: mixed and/or, parentheses, CEL macros, or function calls.
//
// Exactly one of the three may be set at a time; see Compile.
type Conditional struct {
	// When is one expression (bare string) or several expressions ANDed
	// together (a list), matching Ansible's when: keyword.
	When StringList `json:"when,omitempty" yaml:"when,omitempty"`

	// WhenOr is one expression (bare string) or several expressions ORed
	// together (a list). There is no Ansible equivalent.
	WhenOr StringList `json:"when_or,omitempty" yaml:"when_or,omitempty"`

	// WhenCEL is a single raw CEL expression, used verbatim with no
	// wrapping or joining.
	WhenCEL string `json:"when_cel,omitempty" yaml:"when_cel,omitempty"`
}

// ConditionResult is the outcome of evaluating a ConditionProgram: OK
// reports whether the condition holds (the task should run); Reason
// explains why not when it does not, naming the specific when, when_or, or
// when_cel expression that evaluated false. Reason is empty when OK is
// true.
//
// Ordinal and Total carry as numbers what Reason already states in a
// sentence, so a caller that needs to know which condition of how many
// decided a skip never has to parse English back out of Reason. The run
// journal (JournalEntry.SkipOrdinal and SkipTotal, journal.go) is that
// caller, and it needs the split for a specific reason: it stores no free
// text a runbook author wrote, and Reason quotes the author's own
// expression verbatim. evalAnd and evalOr already compute both numbers to
// build the sentence, so these fields are taken from that same
// computation rather than re-derived, and the sentence itself is
// unchanged.
type ConditionResult struct {
	OK     bool
	Reason string

	// Ordinal is which item of the condition list decided this result,
	// counting from 1, and zero when no single item did. evalAnd sets it,
	// because AND semantics short-circuit on the first false item and
	// exactly one item is therefore responsible. evalOr leaves it zero
	// even on a skip: under OR semantics every item had to evaluate false
	// and none of them is the actionable one, which is exactly why
	// evalOr's own Reason names all of them instead of one.
	Ordinal int

	// Total is how many items the condition list held.
	//
	// Both fields are zero when OK is true. A condition that holds has no
	// ordinal to be out of, and leaving the pair zero keeps "not
	// applicable" one readable shape rather than two.
	Total int
}

// conditionItem pairs one raw when/when_or/when_cel expression with its own
// compiled Program, so a false result can be reported against its own
// source text rather than an opaque expression joined from several.
type conditionItem struct {
	expr string
	prg  Program
}

// ConditionProgram is the compiled, evaluable form of a Conditional,
// produced by Conditional.Compile. Unlike a bare Program, evaluating a
// ConditionProgram that comes out false also returns a human-readable
// ConditionResult.Reason naming the expression responsible, which is the
// whole point of this type: Ansible's own when: skip never says why, and
// that is exactly the gap this closes. or selects OR semantics (when_or:
// any item true wins, all items false is the failure); false selects AND
// semantics (when, and the single-item degenerate case when_cel: first
// false item wins).
type ConditionProgram struct {
	keyword string // "when", "when_or", or "when_cel", used in Reason text
	or      bool
	items   []conditionItem
}

// compileItems compiles each of exprs as its own separate Program (rather
// than joining them into one expression first), so Eval can later report
// exactly which item was responsible for a false result. keyword and or
// are stored on the returned ConditionProgram for Eval to use.
func compileItems(cel Evaluator, keyword string, or bool, exprs []string) (*ConditionProgram, error) {
	items := make([]conditionItem, len(exprs))
	for i, expr := range exprs {
		prg, err := cel.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile %s expression %d (`%s`): %w", keyword, i+1, expr, err)
		}
		items[i] = conditionItem{expr: expr, prg: prg}
	}
	return &ConditionProgram{keyword: keyword, or: or, items: items}, nil
}

// Eval evaluates cp's items against vars, the same top-level CEL
// activation Program.Eval expects (cel.go), returning whether the
// condition holds and, if not, a Reason naming the responsible expression.
func (cp *ConditionProgram) Eval(vars map[string]interface{}) (ConditionResult, error) {
	if cp.or {
		return cp.evalOr(vars)
	}
	return cp.evalAnd(vars)
}

// evalAnd implements when/when_cel semantics: every item must be true.
// It short-circuits and reports the first false item, mirroring how CEL's
// own && operator would short-circuit if the items were still joined into
// one expression.
func (cp *ConditionProgram) evalAnd(vars map[string]interface{}) (ConditionResult, error) {
	for i, item := range cp.items {
		ok, err := item.prg.Eval(vars)
		if err != nil {
			return ConditionResult{}, fmt.Errorf("failed to evaluate %s expression %d (`%s`): %w", cp.keyword, i+1, item.expr, err)
		}
		if !ok {
			reason := fmt.Sprintf("%s `%s` evaluated false", cp.keyword, item.expr)
			if len(cp.items) > 1 {
				reason = fmt.Sprintf("%s condition %d of %d evaluated false: `%s`", cp.keyword, i+1, len(cp.items), item.expr)
			}
			// i+1 and len(cp.items) are the very two numbers the
			// multi-item sentence above formats, carried across rather
			// than recomputed, so the numbers and the sentence cannot
			// drift apart. They are set on the single-item branch too,
			// where the sentence omits them: a lone when_cel is condition
			// 1 of 1, and a caller reading the numbers should not have to
			// special-case the degenerate list.
			return ConditionResult{OK: false, Reason: reason, Ordinal: i + 1, Total: len(cp.items)}, nil
		}
	}
	return ConditionResult{OK: true}, nil
}

// evalOr implements when_or semantics: any item true is enough. It
// short-circuits on the first true item; if every item is false, the
// Reason names all of them, since each one contributed to the skip, unlike
// evalAnd where only the first false item is the actionable one.
func (cp *ConditionProgram) evalOr(vars map[string]interface{}) (ConditionResult, error) {
	falseExprs := make([]string, 0, len(cp.items))
	for i, item := range cp.items {
		ok, err := item.prg.Eval(vars)
		if err != nil {
			return ConditionResult{}, fmt.Errorf("failed to evaluate %s expression %d (`%s`): %w", cp.keyword, i+1, item.expr, err)
		}
		if ok {
			return ConditionResult{OK: true}, nil
		}
		falseExprs = append(falseExprs, fmt.Sprintf("`%s`", item.expr))
	}
	reason := fmt.Sprintf("%s %s evaluated false", cp.keyword, falseExprs[0])
	if len(cp.items) > 1 {
		reason = fmt.Sprintf("%s: all %d conditions evaluated false: %s", cp.keyword, len(cp.items), strings.Join(falseExprs, ", "))
	}
	// len(cp.items) is the same count the multi-item sentence above
	// formats. Ordinal stays zero here: every item contributed to this
	// skip, so no single one owns it. See ConditionResult.Ordinal.
	return ConditionResult{OK: false, Reason: reason, Total: len(cp.items)}, nil
}

// Compile turns this Conditional into a compiled *ConditionProgram using
// the given Evaluator. An empty StringList counts the same as an absent
// one (matching how Ansible treats an empty when: list), so at most one of
// When, WhenOr, and WhenCEL may have at least one element or a non-empty
// string. If more than one is set, Compile returns a nil *ConditionProgram
// and an error. If none are set, Compile returns (nil, nil), meaning
// unconditional. Otherwise, it compiles whichever one is set: When with AND
// semantics, WhenOr with OR semantics, WhenCEL as a single AND-semantics
// item (there is only ever one, so AND/OR make no difference).
func (c Conditional) Compile(cel Evaluator) (*ConditionProgram, error) {
	whenSet := len(c.When) > 0
	whenOrSet := len(c.WhenOr) > 0
	whenCELSet := c.WhenCEL != ""

	set := 0
	if whenSet {
		set++
	}
	if whenOrSet {
		set++
	}
	if whenCELSet {
		set++
	}

	if set > 1 {
		return nil, fmt.Errorf("only one of when, when_or, or when_cel may be set")
	}
	if set == 0 {
		return nil, nil
	}

	switch {
	case whenSet:
		return compileItems(cel, "when", false, c.When)
	case whenOrSet:
		return compileItems(cel, "when_or", true, c.WhenOr)
	default:
		return compileItems(cel, "when_cel", false, []string{c.WhenCEL})
	}
}
