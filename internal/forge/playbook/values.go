// Package playbook: reading an argument's value, templates resolved, and
// converting a resolved value to a native parameter's type.
package playbook

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// goValue reads n into the Go value Ansible holds, with every template in
// it resolved from the playbook's own variables and bindings.
func (t *translator) goValue(n *yaml.Node, bindings map[string]any, at Position) (any, *resolveError) {
	v, err := naturalValue(n)
	if err != nil {
		return nil, &resolveError{"vault.value", err.Error()}
	}
	return t.res.expand(v, bindings, at, 0)
}

// hasTemplateNode reports whether any scalar under n holds Jinja.
func hasTemplateNode(n *yaml.Node) bool {
	n = deref(n)
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return hasTemplate(n.Value)
	}
	for _, c := range n.Content {
		if hasTemplateNode(c) {
			return true
		}
	}
	return false
}

// coerceGo converts v, a value a template resolved to, into a native
// parameter of type typ. The value no longer carries how it was written,
// so the conversions that depended on that (an octal mode, a float's
// digits) are refused rather than guessed.
func coerceGo(v any, typ, param string) (any, error) {
	switch typ {
	case "string":
		switch x := v.(type) {
		case string:
			return x, nil
		case int64:
			if param == "mode" {
				return nil, errAmbiguous{"a mode that reaches the task as a number; quote it where it is defined"}
			}
			return strconv.FormatInt(x, 10), nil
		}
	case "list", "list of string", "int or list of int":
		elem := "string"
		if typ == "int or list of int" {
			elem = "int"
		}
		items, ok := v.([]any)
		if text, isText := v.(string); isText && elem == "string" {
			list, err := textAsList(text)
			if err != nil {
				return nil, err
			}
			items, ok = list, true
		}
		if !ok {
			items = []any{v}
		}
		out := make([]any, len(items))
		for i, item := range items {
			x, err := coerceGo(item, elem, param)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case "bool":
		// Ansible's own boolean(): these texts, any case, and 0 or 1.
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		case string:
			switch strings.ToLower(x) {
			case "yes", "true", "on", "1", "y", "t":
				return true, nil
			case "no", "false", "off", "0", "n", "f":
				return false, nil
			}
		}
	case "int":
		switch x := v.(type) {
		case int64:
			return x, nil
		case string:
			if i, err := strconv.ParseInt(x, 10, 64); err == nil {
				return i, nil
			}
		}
	case "float":
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
	case "dict":
		if m, ok := v.(map[string]any); ok {
			return m, nil
		}
	}
	return nil, errAmbiguous{fmt.Sprintf("%s where %s takes a %s", kindOf(v), param, typ)}
}

// kindOf names v's kind as a playbook's reader knows it, never its value.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case int64, float64:
		return "a number"
	case string:
		return "a text"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	}
	return "a value"
}
