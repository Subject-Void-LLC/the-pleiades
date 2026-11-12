// Whether a task's value is one name a program cannot read as anything else.
//
// A parameter declaring an operand format (ParamFormatOperand and its
// siblings, each mapped to one pkg/operand kind) is a word the method places
// on a device's command line after the program's own options. OperandProblem
// is the one rule validation (a literal), the engine (a rendered value) and
// the rollback planner (a journaled undo) hold such a value to, as
// ChoiceProblem is for a closed set; the rules themselves are pkg/operand's,
// which the method also applies before it sends anything.
package collection

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// operandKinds maps each operand format to the pkg/operand rule it holds a
// value to. A format absent here is not an operand.
var operandKinds = map[ParamFormat]operand.Kind{
	ParamFormatOperand:        operand.Name,
	ParamFormatSystemdUnit:    operand.SystemdUnit,
	ParamFormatAptPackage:     operand.AptPackage,
	ParamFormatRPMPackage:     operand.RPMPackage,
	ParamFormatPackage:        operand.Package,
	ParamFormatPackageVersion: operand.PackageVersion,
	ParamFormatMountField:     operand.MountField,
}

// OperandKind returns the pkg/operand rule format holds a value to, and
// whether format is an operand format at all.
func OperandKind(format ParamFormat) (operand.Kind, bool) {
	kind, ok := operandKinds[format]
	return kind, ok
}

// OperandFormats lists every operand format, for the code generator that
// writes each as a named constant and a test that holds this map to
// pkg/operand's kinds.
func OperandFormats() []ParamFormat {
	return []ParamFormat{
		ParamFormatOperand, ParamFormatSystemdUnit, ParamFormatAptPackage, ParamFormatRPMPackage,
		ParamFormatPackage, ParamFormatPackageVersion, ParamFormatMountField,
	}
}

// OperandProblem returns "" when p is not an operand, or v passes the rule
// p's format holds it to, and otherwise a sentence saying why not (the
// pkg/operand kind's own). A list is held to the rule item by item, and a
// value that is not text (a null, a number) is left to the parameter's
// type and required checks. The value itself is never printed: it may be
// a rendered value a device returned.
func OperandProblem(p Param, v any) string {
	kind, ok := OperandKind(p.Format)
	if !ok || v == nil {
		return ""
	}
	if items, ok := listOf(v); ok {
		for i, item := range items {
			if text, ok := item.(string); ok {
				if problem := kind.Problem(text); problem != "" {
					return fmt.Sprintf("item %d is refused: %s", i, problem)
				}
			}
		}
		return ""
	}
	if text, ok := v.(string); ok {
		return kind.Problem(text)
	}
	return ""
}

// checkOperand refuses an operand format on a parameter that cannot hold a
// name: an operand is text, or a list of text, and nothing else.
func checkOperand(d Descriptor, p Param) error {
	if _, ok := OperandKind(p.Format); !ok {
		return nil
	}
	switch p.Type {
	case ParamTypeString, ParamTypeList, ParamTypeListOfString:
		return nil
	}
	return fmt.Errorf("collection: %q declares parameter %q of type %s an operand, which only text or a list of text can be", d.Name, p.Name, p.Type)
}
