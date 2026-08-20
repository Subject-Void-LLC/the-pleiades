package engine

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// filtersLibraryName is this library's namespaced identifier, checked by
// cel.Lib against cel.Env.HasLibrary so registering filtersLib() twice on
// the same *cel.Env (a future EnvOption reuse, or a second NewCELEvaluator
// call sharing options) is a no-op rather than a duplicate-function
// collision error.
const filtersLibraryName = "pleiades.filters"

// filtersLib returns the cel.EnvOption that registers every function in
// pkg/filters into a CEL environment, under the flat "filters." prefix
// PLAN.md Section 36 reserves for first-party filters (a different
// namespace from a Collection's <namespace>.<method> FQCN; the two never
// cross-reference each other, so no collision is possible even in
// principle). This is the only file in the repository that imports both
// cel-go and pkg/filters: pkg/filters stays pure Go so it is independently
// unit-testable, and this file is the sole translation to cel-go's ref.Val
// types.
func filtersLib() cel.EnvOption {
	return cel.Lib(filtersLibrary{})
}

// filtersLibrary implements cel.SingletonLibrary. It carries no state:
// every filter function is a pure translation from CEL argument values to
// a pkg/filters call and back, so there is nothing to construct per
// environment.
type filtersLibrary struct{}

func (filtersLibrary) LibraryName() string {
	return filtersLibraryName
}

func (filtersLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("filters.safeInt",
			cel.FunctionDocs(
				"parse a value as a base-10 integer, returning fallback if it is present but malformed.",
				"The value may be any type; it is converted to a string before parsing, so this is safe to",
				"call on a dyn field whose runtime type is not known ahead of time (a device may report the",
				"same field as an int on one firmware and a string on the next). For a value that may be",
				"absent entirely, use the environment's own optional-chaining syntax instead:",
				"stat.?retries.orValue(0). filters.safeInt does not solve that problem and should not be",
				"reached for as if it did.",
			),
			cel.Overload("filters_safe_int_dyn_int",
				[]*cel.Type{cel.DynType, cel.IntType}, cel.IntType,
				cel.OverloadExamples(
					`filters.safeInt(stat.retries, 0) > 3 // stat.retries == "7" -> 7 > 3 -> true`,
					`filters.safeInt(stat.retries, 0) // stat.retries == "abc" -> 0 (fallback)`,
				),
				cel.BinaryBinding(safeIntBinding),
			),
		),
		cel.Function("filters.safeFloat",
			cel.FunctionDocs(
				"parse a value as a base-10 floating-point number, returning fallback if it is present but",
				"malformed, or if it parses to NaN or an infinity (refused deliberately: a NaN silently",
				"makes every downstream comparison false, the opposite of what a safe cast should do). See",
				"filters.safeInt's own documentation for the missing-versus-malformed distinction; the same",
				"applies here. The fallback argument must be a double literal (0.0, not 0): CEL does not",
				"promote an int literal to double automatically.",
			),
			cel.Overload("filters_safe_float_dyn_double",
				[]*cel.Type{cel.DynType, cel.DoubleType}, cel.DoubleType,
				cel.OverloadExamples(
					`filters.safeFloat(stat.load, 0.0) > 3.5 // stat.load == "4.2" -> 4.2 > 3.5 -> true`,
					`filters.safeFloat(stat.load, 0.0) // stat.load == "NaN" -> 0.0 (fallback)`,
				),
				cel.BinaryBinding(safeFloatBinding),
			),
		),
		cel.Function("filters.safeBool",
			cel.FunctionDocs(
				"parse a value as a boolean, accepting 1/t/true/yes/on and 0/f/false/no/off case",
				"insensitively (a superset of Go's own strconv.ParseBool, matching the truthy spellings",
				"YAML 1.1 and Ansible both already use and that real device CLI output commonly prints).",
				"Any other value, including an unrecognized word or an empty string, returns fallback:",
				"there is no built-in default for an unrecognized spelling beyond what the caller passes.",
			),
			cel.Overload("filters_safe_bool_dyn_bool",
				[]*cel.Type{cel.DynType, cel.BoolType}, cel.BoolType,
				cel.OverloadExamples(
					`filters.safeBool(stat.enabled, false) // stat.enabled == "yes" -> true`,
					`filters.safeBool(stat.enabled, false) // stat.enabled == "maybe" -> false (fallback)`,
				),
				cel.BinaryBinding(safeBoolBinding),
			),
		),
	}
}

func (filtersLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

// celToString converts an arbitrary CEL value to its string form for the
// dyn-typed first argument every filters.safe* function accepts, so a
// device fact reported as an int on one firmware and a string on the next
// reaches pkg/filters identically. ok is false for a value with no string
// conversion at all (a map or a list; cel-go's own ConvertToType returns a
// types.Err for both), which the caller treats as "use fallback" rather
// than propagating a CEL evaluation error out of what is documented as a
// safe cast.
func celToString(v ref.Val) (string, bool) {
	if s, isString := v.(types.String); isString {
		return string(s), true
	}
	converted := v.ConvertToType(types.StringType)
	if types.IsError(converted) {
		return "", false
	}
	s, isString := converted.(types.String)
	if !isString {
		return "", false
	}
	return string(s), true
}

func safeIntBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Int)
	if !ok {
		return types.NewErr("filters.safeInt: fallback must be an int, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Int(filters.SafeInt(s, int(fb)))
}

func safeFloatBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Double)
	if !ok {
		return types.NewErr("filters.safeFloat: fallback must be a double, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Double(filters.SafeFloat(s, float64(fb)))
}

func safeBoolBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Bool)
	if !ok {
		return types.NewErr("filters.safeBool: fallback must be a bool, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Bool(filters.SafeBool(s, bool(fb)))
}
