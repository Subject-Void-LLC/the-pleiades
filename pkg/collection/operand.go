// Whether a task's value is one name a program cannot read as anything else.
//
// A parameter declaring ParamFormatOperand is a name the method places on a
// device's command line after the program's own options. OperandProblem is
// the one rule validation (a literal) and the engine (a rendered value) hold
// such a value to, as ChoiceProblem is for a closed set; the rule itself is
// pkg/operand's, which the method also applies before it sends anything.
package collection

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// OperandProblem returns "" when p is not an operand, or v is one name a
// program can read only as that name, and otherwise a sentence saying why
// not (operand.Problem). A list is held to the rule item by item, and a
// value that is not text (a null, a number) is left to the parameter's
// type and required checks. The value itself is never printed: it may be
// a rendered value a device returned.
func OperandProblem(p Param, v any) string {
	if p.Format != ParamFormatOperand || v == nil {
		return ""
	}
	if items, ok := listOf(v); ok {
		for i, item := range items {
			if text, ok := item.(string); ok {
				if problem := operand.Problem(text); problem != "" {
					return fmt.Sprintf("item %d is refused: %s", i, problem)
				}
			}
		}
		return ""
	}
	if text, ok := v.(string); ok {
		return operand.Problem(text)
	}
	return ""
}

// checkOperand refuses ParamFormatOperand on a parameter that cannot hold
// a name: an operand is text, or a list of text, and nothing else.
func checkOperand(d Descriptor, p Param) error {
	if p.Format != ParamFormatOperand {
		return nil
	}
	switch p.Type {
	case ParamTypeString, ParamTypeList, ParamTypeListOfString:
		return nil
	}
	return fmt.Errorf("collection: %q declares parameter %q of type %s an operand, which only text or a list of text can be", d.Name, p.Name, p.Type)
}
