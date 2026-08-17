package sdk

import "fmt"

// Typed readers for a task's params map.
//
// Every Collection method receives its parameters as map[string]any,
// because that is what survives YAML decoding on the Walk tier and JSON
// decoding across the Runner's per-task subprocess boundary on the Crawl
// tier. Reading that map correctly is the same problem in every module,
// and getting it wrong is quiet rather than loud: a misspelled key reads
// as absent, and a value of the wrong type reads as the zero value.
//
// These moved here from the exec namespace's own private helpers the
// moment a second namespace needed them. They are in pkg/ rather than
// somewhere under internal/catalog because a Collection package may
// import only pkg/, which internal/archtest enforces, so a shared helper
// under internal/ would be unreachable from the code that needs it.

// StringParam reads a string task parameter, treating a missing or
// non-string value as absent.
func StringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// BoolParam reads a boolean task parameter, treating a missing or
// non-boolean value as false.
//
// It accepts a real bool only, not the string "true". YAML already
// decodes an unquoted true into a bool, and accepting the string form
// would mean silently honoring a quoted "false" as true-ish somewhere
// down the line. That matters more than it sounds: one of the parameters
// read this way turns off host key verification.
func BoolParam(params map[string]any, key string) bool {
	v, ok := params[key].(bool)
	return ok && v
}

// BoolParamOr reads a boolean task parameter, returning fallback when the
// key is absent.
//
// Distinct from BoolParam because some parameters default to true, and
// for those "absent" and "explicitly false" are different answers that
// BoolParam cannot tell apart. A non-boolean value is refused rather than
// treated as absent, since a task that wrote one meant something by it.
func BoolParamOr(params map[string]any, key string, fallback bool) (bool, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return fallback, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return v, nil
}

// StringSlice reads a list-of-strings task parameter, reporting whether
// the key was present at all so a caller can tell an empty list from an
// absent one.
//
// A non-string element is refused rather than rendered with %v. A YAML
// author who wrote a bare 8080 in an argument list meant the text 8080,
// but a value that arrived as a float64 across the Runner's task
// subprocess boundary would render as "8080" in one tier and "8080.000"
// in another, and a module that silently produced two different command
// lines depending on which tier ran it is worse than one that refuses.
func StringSlice(params map[string]any, key string) ([]string, bool, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return nil, false, nil
	}

	items, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("%s must be a list of strings", key)
	}

	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s[%d] is %T, not a string: quote it in the runbook", key, i, item)
		}
		out = append(out, s)
	}
	return out, true, nil
}

// RequiredStringParam reads a string parameter that a method cannot
// proceed without, refusing an absent or empty one by name.
//
// A method with a required parameter should refuse before it connects,
// so the failure costs no round trip and names the runbook's mistake
// rather than the device's.
func RequiredStringParam(params map[string]any, key string) (string, error) {
	v := StringParam(params, key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
