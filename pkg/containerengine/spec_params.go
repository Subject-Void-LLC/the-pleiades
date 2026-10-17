// Reading a container spec's parameters, each refused when it is not the
// type it should be rather than guessed at.
package containerengine

import (
	"fmt"
	"strings"
)

// textOf reads an optional text parameter, refusing any other type.
func textOf(params map[string]any, key string) (string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %v is not text; quote it", key, raw)
	}
	return s, nil
}

// boolOf reads an optional boolean parameter.
func boolOf(params map[string]any, key string) (bool, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s: %v is not true or false", key, raw)
	}
	return b, nil
}

// listOf reads an optional list of text.
func listOf(params map[string]any, key string) ([]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if strs, ok := raw.([]string); ok {
			return append([]string{}, strs...), nil
		}
		return nil, fmt.Errorf("%s is a list", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %v is not text; quote it", key, item)
		}
		out = append(out, s)
	}
	return out, nil
}

// argvOf reads a command as a list of arguments; text is refused, since a
// container's command is never run through a shell unless the task names
// one.
func argvOf(params map[string]any, key string) ([]string, error) {
	if s, ok := params[key].(string); ok {
		return nil, fmt.Errorf("%s: %q is text; give a list of arguments (for a shell, [/bin/sh, -c, \"...\"])", key, s)
	}
	return listOf(params, key)
}

// stringMap reads an optional mapping of text to text.
func stringMap(params map[string]any, key string) (map[string]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is a mapping", key)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s's value is not text; quote it", key, k)
		}
		out[k] = s
	}
	return out, nil
}

// envOf reads env, refusing a name the engine would split differently.
func envOf(params map[string]any) (map[string]string, error) {
	env, err := stringMap(params, "env")
	if err != nil {
		return nil, err
	}
	for k, v := range env {
		if !envName.MatchString(k) {
			return nil, fmt.Errorf("env: %q is not a variable name", k)
		}
		if strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("env: %s's value holds a NUL", k)
		}
	}
	return env, nil
}
