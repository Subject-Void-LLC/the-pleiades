// Package launch: the launch field that gives a run's method settings.
package launch

import "fmt"

// MethodSettingsField is the job's method settings: values for the
// parameters a method declares a run may set (collection.Param.RunSetting),
// keyed <method>.<parameter> (file.remove.check_forbidden: warn), for every
// task that leaves the parameter out. A TypeMap, so it merges across layers
// key by key: a launch setting one keeps the template's others, and for each
// setting the most specific layer wins. The task's own value wins over all
// of them. A kind holds each key and value to the method it names through
// its field's Check.
const MethodSettingsField = "method_settings"

// MethodSettings returns f's method settings, nil when it has none. A value
// that is not text is left out: the field's Check refuses one wherever it is
// saved, so it can reach here only from a row written before that check.
func MethodSettings(f Fields) map[string]string {
	raw := f.Map(MethodSettingsField)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// StrictMethodSettings is MethodSettings for a reader that must not drop
// anything, a Runner: a value that is not text is an error naming its key,
// so the dispatch is refused rather than run with the parameter's default.
func StrictMethodSettings(f Fields) (map[string]string, error) {
	for k, v := range f.Map(MethodSettingsField) {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("run setting %q is not text, one of its choices", k)
		}
	}
	return MethodSettings(f), nil
}
