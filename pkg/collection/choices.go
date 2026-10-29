// Whether a task's value is one of the closed set its method declares.
//
// A parameter that takes one of a fixed set of values (a state, a mode, a
// protocol) declares the set as Param.Choices, so pleiades doc prints it
// and a value outside it is refused before the run, naming the set, rather
// than by the method once earlier tasks have changed things, or not at all.
// ChoiceProblem is the one rule both validation (a literal) and the engine
// (a rendered value) hold a value to, as ParamFits is for its type.
package collection

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ChoiceProblem returns "" when v is within p's declared choices, and
// otherwise a sentence saying which value is outside them and naming the
// set. A parameter with no Choices takes any value, and so does a null,
// which is an absent value its method's own required check answers.
//
// A list is held to the set item by item. A number is compared by its
// decimal text, so an int parameter may declare its choices too. A value
// of any other shape (a map, a boolean) is left to ParamFits, which judges
// its type. The value itself is never printed: it may be a rendered value
// a device returned, and the set is what an author needs to fix it.
func ChoiceProblem(p Param, v any) string {
	if len(p.Choices) == 0 || v == nil {
		return ""
	}
	if items, ok := listOf(v); ok {
		for i, item := range items {
			if text, ok := choiceText(item); ok && !p.choiceAllowed(text) {
				return fmt.Sprintf("item %d is not one of %s", i, p.ChoiceList())
			}
		}
		return ""
	}
	if text, ok := choiceText(v); ok && !p.choiceAllowed(text) {
		return "it is not one of " + p.ChoiceList()
	}
	return ""
}

// ChoiceList is p's declared choices as one line, for a message or a page:
// "present, absent", with "(case ignored)" after it when ChoicesIgnoreCase
// is set.
func (p Param) ChoiceList() string {
	list := strings.Join(p.Choices, ", ")
	if p.ChoicesIgnoreCase {
		list += " (case ignored)"
	}
	return list
}

// choiceAllowed reports whether text is one of p's choices, ignoring case
// and surrounding space when the parameter says its method does.
func (p Param) choiceAllowed(text string) bool {
	if !p.ChoicesIgnoreCase {
		return slices.Contains(p.Choices, text)
	}
	text = strings.TrimSpace(text)
	return slices.ContainsFunc(p.Choices, func(c string) bool { return strings.EqualFold(c, text) })
}

// choiceText is v as the text a choice is compared with: a string as it
// is, and a number as its decimal form (a whole float64 without a
// fraction, since a whole number that crossed a JSON boundary arrives as
// one).
// It reports false for any other value, which no choice can name.
func choiceText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		// 'f' with the shortest precision writes 22.0 as 22, the text a
		// choice names, and keeps any fraction a non-whole number has.
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

// checkChoices refuses a declared set no value could satisfy or that
// contradicts the rest of the parameter: a set on a type no choice can
// name (a boolean, a map, a list of maps), an empty or repeated choice, or
// a default outside the set, which would make a task leaving the
// parameter out mean a value a task writing it could not give.
func checkChoices(d Descriptor, p Param) error {
	if len(p.Choices) == 0 {
		if p.ChoicesIgnoreCase {
			return fmt.Errorf("collection: %q declares parameter %q ChoicesIgnoreCase with no choices", d.Name, p.Name)
		}
		return nil
	}
	switch p.Type {
	case ParamTypeBool, ParamTypeDict, ParamTypeListOfDict:
		return fmt.Errorf("collection: %q declares choices on parameter %q of type %s, which no choice can name", d.Name, p.Name, p.Type)
	}
	seen := map[string]bool{}
	for _, c := range p.Choices {
		key := c
		if p.ChoicesIgnoreCase {
			key = strings.ToLower(c)
		}
		switch {
		case strings.TrimSpace(c) == "":
			return fmt.Errorf("collection: %q declares an empty choice for parameter %q", d.Name, p.Name)
		case seen[key]:
			return fmt.Errorf("collection: %q declares the choice %q twice for parameter %q", d.Name, c, p.Name)
		}
		seen[key] = true
	}
	if p.Default != "" && !p.choiceAllowed(p.Default) {
		return fmt.Errorf("collection: %q declares parameter %q defaulted to %q, which is not one of its choices (%s)", d.Name, p.Name, p.Default, p.ChoiceList())
	}
	return nil
}
