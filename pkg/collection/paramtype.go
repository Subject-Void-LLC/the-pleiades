// Whether a task's value is of the type its method declares for it.
//
// A method reads its params map through pkg/sdk's typed readers, and the
// most used of them, StringParam, reads a value of any other type as
// absent: a task writing stdin: 4 sent nothing, and a template rendering a
// number into a string parameter did the same, with no error anywhere. The
// declared type (Param.Type) was documentation only. ParamFits is the one
// rule both validation (a literal) and the engine (a rendered value) hold a
// value to, so a value of the wrong type is refused naming the parameter
// rather than read as nothing.
package collection

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// The parameter types a method may declare (Param.Type).
const (
	ParamTypeString       = "string"
	ParamTypeBool         = "bool"
	ParamTypeInt          = "int"
	ParamTypeFloat        = "float"
	ParamTypeList         = "list"
	ParamTypeListOfString = "list of string"
	ParamTypeListOfDict   = "list of dict"
	ParamTypeDict         = "dict"
	ParamTypeAny          = "any"
	ParamTypeIntOrIntList = "int or list of int"
	// ParamTypeScalarOrList is a string, a number, or a list of strings and
	// numbers: a key path dotted or by segment, or a set of ports written as
	// one value, a comma-separated string (the form an undo records, since the
	// journal keeps only single values) or a list.
	ParamTypeScalarOrList = "scalar or list"
)

// ParamTypes returns every type a parameter may declare, sorted.
func ParamTypes() []string {
	types := []string{
		ParamTypeString, ParamTypeBool, ParamTypeInt, ParamTypeFloat, ParamTypeList,
		ParamTypeListOfString, ParamTypeListOfDict, ParamTypeDict, ParamTypeAny, ParamTypeIntOrIntList,
		ParamTypeScalarOrList,
	}
	slices.Sort(types)
	return types
}

// KnownParamType reports whether typ is one of ParamTypes.
func KnownParamType(typ string) bool {
	return slices.Contains(ParamTypes(), typ)
}

// ParamFits reports whether v is a value of the declared type typ, and
// errors for a type that is not one of ParamTypes.
//
// A null is always accepted: it is an absent value, which a method's own
// required check answers. A number is accepted in every shape a tier
// produces for it: int from YAML on the command line, int64 from a wide
// decoder, and float64 once it has crossed the Runner's per-task process
// boundary as JSON, so an int parameter takes a float64 that is a whole
// number and no other. Nothing is converted: a quoted "22" is not an int and
// an unquoted 0644 is not a string, since converting either would hand the
// method a value its author did not write (420 for that mode).
func ParamFits(typ string, v any) (bool, error) {
	if !KnownParamType(typ) {
		return false, fmt.Errorf("%q is not a parameter type (one of %s)", typ, strings.Join(ParamTypes(), ", "))
	}
	if v == nil {
		return true, nil
	}
	switch typ {
	case ParamTypeAny:
		return true, nil
	case ParamTypeString:
		_, ok := v.(string)
		return ok, nil
	case ParamTypeBool:
		_, ok := v.(bool)
		return ok, nil
	case ParamTypeInt:
		return isWhole(v), nil
	case ParamTypeFloat:
		return isNumber(v), nil
	case ParamTypeDict:
		_, ok := v.(map[string]any)
		return ok, nil
	case ParamTypeList:
		_, ok := listOf(v)
		return ok, nil
	case ParamTypeListOfString:
		if _, ok := v.([]string); ok {
			return true, nil
		}
		return everyItem(v, func(e any) bool { _, ok := e.(string); return ok }), nil
	case ParamTypeListOfDict:
		return everyItem(v, func(e any) bool { _, ok := e.(map[string]any); return ok }), nil
	case ParamTypeIntOrIntList:
		return isWhole(v) || everyItem(v, isWhole), nil
	default: // ParamTypeScalarOrList, the last type ParamTypes lists
		textOrNumber := func(e any) bool { _, isText := e.(string); return isText || isNumber(e) }
		return textOrNumber(v) || everyItem(v, textOrNumber), nil
	}
}

// checkParamTypes refuses a method declaring a parameter whose type is not
// one of ParamTypes, since validation and the engine hold every value to its
// declared type and a word neither knows would refuse every value. It also
// refuses a parameter both required and defaulted: validation refuses a
// task leaving out a required one, so a default it states would be one no
// task could ever get. And it refuses a declared set of choices that
// contradicts the parameter (checkChoices).
func checkParamTypes(d Descriptor) error {
	for _, p := range d.Manifest.Doc.Params {
		if !KnownParamType(p.Type) {
			return fmt.Errorf("collection: %q declares parameter %q of type %q, which is not a parameter type (one of %s)",
				d.Name, p.Name, p.Type, strings.Join(ParamTypes(), ", "))
		}
		if p.Required && p.Default != "" {
			return fmt.Errorf("collection: %q declares parameter %q both required and defaulted to %q; a task must give a required one, so drop the default or the requirement",
				d.Name, p.Name, p.Default)
		}
		if err := checkAlias(d, p); err != nil {
			return err
		}
		if err := checkChoices(d, p); err != nil {
			return err
		}
	}
	return nil
}

// checkAlias refuses an alias that could not stand for the parameter it
// names: one naming no other parameter of the method, or one that is
// itself an alias (a chain no reader follows), or of another type, or
// required, which would ask for the same value twice.
func checkAlias(d Descriptor, p Param) error {
	if p.AliasOf == "" {
		return nil
	}
	if p.Required {
		return fmt.Errorf("collection: %q declares parameter %q, an alias of %q, required; mark %q required instead", d.Name, p.Name, p.AliasOf, p.AliasOf)
	}
	for _, other := range d.Manifest.Doc.Params {
		if other.Name != p.AliasOf || other.Name == p.Name {
			continue
		}
		switch {
		case other.AliasOf != "":
			return fmt.Errorf("collection: %q declares parameter %q an alias of %q, which is itself an alias", d.Name, p.Name, p.AliasOf)
		case other.Type != p.Type:
			return fmt.Errorf("collection: %q declares parameter %q (%s) an alias of %q (%s), of another type", d.Name, p.Name, p.Type, p.AliasOf, other.Type)
		}
		return nil
	}
	return fmt.Errorf("collection: %q declares parameter %q an alias of %q, which it does not declare", d.Name, p.Name, p.AliasOf)
}

// listOf returns v as a list of values, for a decoded list or a Go caller's
// []string.
func listOf(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// everyItem reports whether v is a list whose every item satisfies fits.
func everyItem(v any, fits func(any) bool) bool {
	items, ok := listOf(v)
	if !ok {
		return false
	}
	for _, e := range items {
		if !fits(e) {
			return false
		}
	}
	return true
}

// isNumber reports whether v is a number in any shape a decoder produces.
func isNumber(v any) bool {
	switch t := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(t)) && !math.IsInf(float64(t), 0)
	case float64:
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	case json.Number:
		_, err := t.Float64()
		return err == nil
	}
	return false
}

// isWhole reports whether v is a whole number: an integer type, or a float
// or json.Number with no fractional part.
func isWhole(v any) bool {
	switch t := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		f := float64(t)
		return isNumber(t) && f == math.Trunc(f)
	case float64:
		return isNumber(t) && t == math.Trunc(t)
	case json.Number:
		if _, err := t.Int64(); err == nil {
			return true
		}
		f, err := t.Float64()
		return err == nil && f == math.Trunc(f)
	}
	return false
}

// ValueKind names the kind of a decoded YAML or JSON value for an error
// message, without printing the value, which may be a secret.
func ValueKind(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		if v == "" {
			return "an empty string"
		}
		return "a string"
	case bool:
		return "a boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "a number"
	case []any, []string:
		return "a list"
	case map[string]any:
		return "a map"
	default:
		return fmt.Sprintf("a value of type %T", v)
	}
}
