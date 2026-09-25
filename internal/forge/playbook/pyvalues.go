// Package playbook: Python's value rules, which Ansible's Jinja follows,
// for folding a condition over values the playbook fixes.
package playbook

import (
	"reflect"
	"strconv"
	"strings"
)

// pyTruthy is Python's truth of v.
func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t != ""
	}
	n, ok := length(v)
	return ok && n > 0
}

// ansibleBool is Ansible's bool filter: yes, on, true, 1 and y (any case)
// are true.
func ansibleBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t == 1
	case float64:
		return t == 1
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "yes", "on", "true", "1", "y", "t":
			return true
		}
	}
	return false
}

// toInt is Jinja's int filter on a constant.
func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// length is len(v) for text, a list or a map.
func length(v any) (int, bool) {
	switch t := v.(type) {
	case string:
		return len([]rune(t)), true
	case []any:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

// number reads v as a Python number (a bool is 0 or 1).
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// pyCompare evaluates a op b with Python's rules, reporting false when
// the operands cannot be compared (which Python would raise on).
func pyCompare(op string, a, b any) (bool, bool) {
	switch op {
	case "==":
		return pyEqual(a, b), true
	case "!=":
		return !pyEqual(a, b), true
	case "in", "not in":
		in, ok := pyIn(a, b)
		return in == (op == "in"), ok
	}
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return order(op, x < y, x == y), true
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return order(op, x < y, x == y), true
		}
	}
	return false, false
}

// order answers an ordering operator from less and equal.
func order(op string, less, equal bool) bool {
	switch op {
	case "<":
		return less
	case "<=":
		return less || equal
	case ">":
		return !less && !equal
	}
	return !less
}

// pyEqual is Python's ==.
func pyEqual(a, b any) bool {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return x == y
		}
		return false
	}
	return reflect.DeepEqual(a, b)
}

// pyIn is Python's in: a substring, a list member, or a map key.
func pyIn(a, b any) (bool, bool) {
	switch t := b.(type) {
	case string:
		s, ok := a.(string)
		return ok && strings.Contains(t, s), ok
	case []any:
		for _, item := range t {
			if pyEqual(a, item) {
				return true, true
			}
		}
		return false, true
	case map[string]any:
		s, ok := a.(string)
		_, in := t[s]
		return ok && in, ok
	}
	return false, false
}
