// Package playbook: reading a scalar the way Ansible does.
//
// Ansible parses playbooks with PyYAML, which follows YAML 1.1; a runbook
// is read as YAML 1.2. The two disagree about plain (unquoted) scalars
// that look like booleans, nulls and octal numbers: to Ansible, `yes` is
// true, `0644` is 420, and `1:30` is 90, while YAML 1.2 reads all three
// differently. So a value is read here with Ansible's rules, and a value
// whose Ansible meaning cannot be carried into the native parameter's
// type without changing it is refused rather than guessed.
package playbook

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// PyYAML's YAML 1.1 implicit resolvers, for the scalar kinds that matter.
var (
	yaml11Bool  = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	yaml11True  = regexp.MustCompile(`^(?:yes|Yes|YES|true|True|TRUE|on|On|ON)$`)
	yaml11Null  = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	yaml11Int   = regexp.MustCompile(`^[-+]?(?:0b[0-1_]+|0[0-7_]+|(?:0|[1-9][0-9_]*)|0x[0-9a-fA-F_]+|[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	yaml11Float = regexp.MustCompile(`^[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?$`)
)

// ansibleValue is one scalar as Ansible reads it.
type ansibleValue struct {
	// text is the scalar as written.
	text string
	// quoted reports whether it was quoted, which makes it a string
	// whatever it looks like.
	quoted bool
	// kind is "str", "bool", "int", "float" or "null".
	kind string
	// b, i and f hold the value for kind bool, int and float.
	b bool
	i int64
	f float64
	// octal and sexagesimal record the two int forms YAML 1.2 reads
	// differently.
	octal, sexagesimal bool
}

// readScalar reads n, a scalar node, with Ansible's rules. It does not
// look at a vault tag; the caller refuses those first.
func readScalar(n *yaml.Node) ansibleValue {
	v := ansibleValue{text: n.Value, kind: "str"}
	if n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		v.quoted = true
		return v
	}
	switch s := n.Value; {
	case yaml11Null.MatchString(s):
		v.kind = "null"
	case yaml11Bool.MatchString(s):
		v.kind, v.b = "bool", yaml11True.MatchString(s)
	case yaml11Int.MatchString(s):
		if i, octal, sexa, ok := parseYAML11Int(s); ok {
			v.kind, v.i, v.octal, v.sexagesimal = "int", i, octal, sexa
		}
	case yaml11Float.MatchString(s):
		if f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64); err == nil && !math.IsInf(f, 0) {
			v.kind, v.f = "float", f
		}
	}
	return v
}

// parseYAML11Int reads s as a YAML 1.1 int: binary, leading-zero octal,
// hex, decimal or base-60, with underscores.
func parseYAML11Int(s string) (value int64, octal, sexagesimal, ok bool) {
	sign := int64(1)
	if strings.HasPrefix(s, "-") {
		sign = -1
	}
	s = strings.TrimLeft(strings.ReplaceAll(s, "_", ""), "+-")
	var err error
	switch {
	case strings.Contains(s, ":"):
		for _, part := range strings.Split(s, ":") {
			d, perr := strconv.ParseInt(part, 10, 64)
			if perr != nil || value > math.MaxInt64/60 {
				return 0, false, false, false
			}
			value = value*60 + d
		}
		sexagesimal = true
	case strings.HasPrefix(s, "0b"):
		value, err = strconv.ParseInt(s[2:], 2, 64)
	case strings.HasPrefix(s, "0x"):
		value, err = strconv.ParseInt(s[2:], 16, 64)
	case len(s) > 1 && s[0] == '0':
		value, err = strconv.ParseInt(s[1:], 8, 64)
		octal = true
	default:
		value, err = strconv.ParseInt(s, 10, 64)
	}
	if err != nil {
		return 0, false, false, false
	}
	return sign * value, octal, sexagesimal, true
}

// errAmbiguous marks a value whose meaning differs between Ansible's
// reading and what the native parameter would receive.
type errAmbiguous struct{ why string }

// Error implements error.
func (e errAmbiguous) Error() string { return e.why }

// toNative converts n into a value for a native parameter of paramType
// (a collection.Param Type), keeping Ansible's meaning or refusing. param
// is the parameter's native name, which only mode's special rule reads.
func toNative(n *yaml.Node, paramType, param string) (any, error) {
	n = deref(n)
	switch {
	case n == nil:
		return nil, errAmbiguous{"no value"}
	case isVault(n):
		return nil, errAmbiguous{"a vault value"}
	case n.Kind == yaml.SequenceNode:
		if paramType != "list" && paramType != "list of string" && paramType != "int or list of int" {
			return nil, errAmbiguous{fmt.Sprintf("a list where %s takes a %s", param, paramType)}
		}
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			elem := "string"
			if paramType == "int or list of int" {
				elem = "int"
			}
			v, err := toNative(item, elem, param)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case n.Kind == yaml.MappingNode:
		if paramType != "dict" {
			return nil, errAmbiguous{fmt.Sprintf("a map where %s takes a %s", param, paramType)}
		}
		return mapToNative(n)
	}
	return scalarToNative(readScalar(n), paramType, param)
}

// scalarToNative converts one scalar.
func scalarToNative(v ansibleValue, paramType, param string) (any, error) {
	switch paramType {
	case "list", "list of string":
		text, err := scalarToNative(v, "string", param)
		if err != nil {
			return nil, err
		}
		return textAsList(text.(string))
	case "string":
		switch {
		case v.quoted || v.kind == "str" || v.kind == "float":
			return v.text, nil
		case v.kind == "int" && param == "mode" && v.octal:
			// Ansible reads 0644 as the int 420 and applies it as mode
			// 0644; the native mode is that same octal text.
			return v.text, nil
		case v.kind == "int" && param == "mode":
			return nil, errAmbiguous{"an unquoted mode without a leading 0 is a decimal number to Ansible, which it applies as a different octal mode; quote the mode you mean"}
		case v.kind == "int" && !v.octal && !v.sexagesimal:
			return v.text, nil
		}
		return nil, errAmbiguous{fmt.Sprintf("the value is %s to Ansible, not text; quote it if text is meant", describe(v))}
	case "bool":
		if v.kind == "bool" {
			return v.b, nil
		}
		if v.kind == "str" {
			switch strings.ToLower(v.text) {
			case "yes", "true", "on", "1", "y", "t":
				return true, nil
			case "no", "false", "off", "0", "n", "f":
				return false, nil
			}
		}
		if v.kind == "int" && (v.i == 0 || v.i == 1) && !v.octal {
			return v.i == 1, nil
		}
	case "int", "int or list of int":
		if v.kind == "int" && !v.sexagesimal {
			return v.i, nil
		}
		if i, err := strconv.ParseInt(v.text, 10, 64); err == nil && v.kind == "str" {
			return i, nil
		}
	case "float":
		if v.kind == "float" {
			return v.f, nil
		}
		if v.kind == "int" {
			return float64(v.i), nil
		}
	}
	return nil, errAmbiguous{fmt.Sprintf("%s cannot be a %s", describe(v), paramType)}
}

// mapToNative converts a map value, each scalar read with Ansible's rules
// into its own natural Go type.
func mapToNative(n *yaml.Node) (map[string]any, error) {
	entries, repeated := mapEntries(n)
	if len(repeated) > 0 {
		return nil, errAmbiguous{fmt.Sprintf("key %q is repeated", repeated[0].key)}
	}
	out := make(map[string]any, len(entries))
	for _, e := range entries {
		v, err := naturalValue(e.value)
		if err != nil {
			return nil, err
		}
		out[e.key] = v
	}
	return out, nil
}

// naturalValue reads any node into the Go value Ansible would hold.
func naturalValue(n *yaml.Node) (any, error) {
	n = deref(n)
	switch {
	case n == nil:
		return nil, nil
	case isVault(n):
		return nil, errAmbiguous{"a vault value"}
	case n.Kind == yaml.MappingNode:
		return mapToNative(n)
	case n.Kind == yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := naturalValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	v := readScalar(n)
	switch v.kind {
	case "bool":
		return v.b, nil
	case "int":
		return v.i, nil
	case "float":
		return v.f, nil
	case "null":
		return nil, nil
	}
	return v.text, nil
}

// describe names what Ansible reads a plain scalar as.
func describe(v ansibleValue) string {
	switch {
	case v.kind == "bool":
		return "a boolean"
	case v.kind == "null":
		return "null"
	case v.kind == "int" && v.octal:
		return "an octal number"
	case v.kind == "int" && v.sexagesimal:
		return "a base-60 number"
	case v.kind == "int":
		return "a number"
	case v.kind == "str":
		return "a text"
	}
	return "a " + v.kind
}

// textAsList is Ansible's list type applied to one text, which splits it
// at every comma. A text with no comma is a one-item list. One with a
// comma is refused: the split is rarely what a playbook meant
// ("description a, b" would become two configuration lines), and sending
// the text whole would change what runs.
func textAsList(s string) ([]any, error) {
	if strings.Contains(s, ",") {
		return nil, errAmbiguous{"Ansible splits this text at its commas into a list; write the list you mean"}
	}
	return []any{s}, nil
}
