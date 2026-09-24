// Package engine: the strict key check every decoded runbook passes
// before it becomes a WorkflowDef.
//
// A decoder that drops a key it does not know turns a word the author
// wrote into nothing, silently: a runbook-level check_mode: true was once
// read and ignored and the runbook ran for real (FAILURE_PATTERNS 252),
// and a misspelled key under metadata: or secret_mask: is still read as
// though it had never been written (FAILURE_PATTERNS 10). yaml.v3's own
// KnownFields option cannot close that here, because the engine decodes
// a yaml.Node it has already rewritten (task_syntax.go) and
// (*yaml.Node).Decode has no strict mode. So this file walks the parsed
// tree against the Go type it will decode into, reading the same struct
// tags the decoder reads, and refuses any key the type does not declare,
// naming it, its place in the runbook and, for YAML, its line.
package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// keyTree is one node of a parsed runbook document, as the key check sees
// it: a map, a list, or anything else. It hides whether the document was
// YAML or JSON, so one walker serves both formats.
type keyTree interface {
	// identity is a comparable value naming this node, so the walker can
	// visit a node shared by several YAML aliases once per type instead
	// of once per alias (an alias bomb would otherwise cost exponential
	// time here, before the decoder's own alias budget ever ran).
	identity() any

	// mapping returns this node's entries when it is a map, and false
	// when it is not.
	mapping() ([]keyEntry, bool)

	// sequence returns this node's items when it is a list, and false
	// when it is not.
	sequence() ([]keyTree, bool)
}

// keyEntry is one key of a map, with the value it holds and, when the
// format has lines, the line the key sits on (zero otherwise).
type keyEntry struct {
	key   string
	line  int
	value keyTree
}

// visitKey is one (node, type) pair the walker has already checked.
type visitKey struct {
	node any
	typ  reflect.Type
}

// keyWalker holds one check's state: which struct tag it reads ("yaml"
// or "json") and which (node, type) pairs it has already visited.
type keyWalker struct {
	tag  string
	seen map[visitKey]bool
}

// yamlUnmarshalerType and jsonUnmarshalerType are the interfaces a type
// implements when it decodes its own shape. The walker stops at such a
// type, since only its own decoder knows what keys it accepts.
var (
	yamlUnmarshalerType = reflect.TypeFor[yaml.Unmarshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// checkKnownKeys refuses the first map key in tree that typ, the Go type
// tree is about to decode into, does not declare under tag ("yaml" or
// "json"). path names tree's own place in the runbook ("" for the whole
// document), and every error names the key, its place and, when known,
// its line. A shape mismatch (a list where a map belongs) is not this
// function's business: the decoder reports it with its own message.
func checkKnownKeys(tree keyTree, typ reflect.Type, tag, path string) error {
	w := keyWalker{tag: tag, seen: make(map[visitKey]bool)}
	return w.walk(tree, typ, path)
}

// walk checks tree against typ, recursing into every field, list item
// and map value whose type could itself hold a struct.
func (w *keyWalker) walk(tree keyTree, typ reflect.Type, path string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if w.decodesItself(typ) {
		return nil
	}
	// Visit each (node, type) pair once: a YAML alias makes one node
	// reachable from many places, and it only needs checking once.
	visit := visitKey{node: tree.identity(), typ: typ}
	if w.seen[visit] {
		return nil
	}
	w.seen[visit] = true

	switch typ.Kind() {
	case reflect.Struct:
		return w.walkStruct(tree, typ, path)
	case reflect.Slice, reflect.Array:
		items, ok := tree.sequence()
		if !ok {
			return nil
		}
		for i, item := range items {
			if err := w.walk(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		entries, ok := tree.mapping()
		if !ok {
			return nil
		}
		// A map's keys are data (params is the case that matters), so
		// only its values are checked, and only when their type could
		// hold a struct: an interface value stops the walk immediately.
		for _, e := range entries {
			if err := w.walk(e.value, typ.Elem(), joinKeyPath(path, e.key)); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkStruct checks each key of tree, a map, against typ's declared
// fields, then recurses into each field's value.
func (w *keyWalker) walkStruct(tree keyTree, typ reflect.Type, path string) error {
	entries, ok := tree.mapping()
	if !ok {
		return nil
	}
	fields := w.fieldsOf(typ)
	for _, e := range entries {
		fieldType, known := fields[e.key]
		if !known {
			return unknownKeyError(path, e, fields)
		}
		if err := w.walk(e.value, fieldType, joinKeyPath(path, e.key)); err != nil {
			return err
		}
	}
	return nil
}

// decodesItself reports whether typ (or a pointer to it) implements this
// walker's format's own unmarshaler interface.
func (w *keyWalker) decodesItself(typ reflect.Type) bool {
	iface := yamlUnmarshalerType
	if w.tag == "json" {
		iface = jsonUnmarshalerType
	}
	return typ.Implements(iface) || reflect.PointerTo(typ).Implements(iface)
}

// fieldsOf maps every key typ declares under this walker's tag to the
// type that key decodes into, following the decoder's own rules: an
// unexported field and a "-" tag declare nothing, an untagged field is
// its lower-cased name for YAML and its exact name for JSON, and an
// embedded struct's fields are promoted when YAML marks it inline or when
// JSON gives it no name of its own.
func (w *keyWalker) fieldsOf(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get(w.tag), ",")
		if name == "-" {
			continue
		}
		inline := w.tag == "yaml" && strings.Contains(","+opts+",", ",inline,")
		promoted := w.tag == "json" && f.Anonymous && name == ""
		if (inline || promoted) && f.Type.Kind() == reflect.Struct {
			maps.Copy(fields, w.fieldsOf(f.Type))
			continue
		}
		if name == "" {
			name = f.Name
			if w.tag == "yaml" {
				name = strings.ToLower(name)
			}
		}
		fields[name] = f.Type
	}
	return fields
}

// unknownKeyError names e's key, where it sits, its line when known, the
// keys that place does accept and, when one is close, the likely intent.
func unknownKeyError(path string, e keyEntry, fields map[string]reflect.Type) error {
	known := make(map[string]bool, len(fields))
	for k := range fields {
		known[k] = true
	}
	where := "at the top level"
	if path != "" {
		where = "under " + path
	}
	if e.line > 0 {
		where += fmt.Sprintf(" (line %d)", e.line)
	}
	hint := ""
	if near := nearestKey(e.key, known); near != "" {
		hint = fmt.Sprintf(" (did you mean %q?)", near)
	}
	names := make([]string, 0, len(known))
	for k := range known {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Errorf("unknown key %q %s%s; this place may carry only %s", e.key, where, hint, strings.Join(names, ", "))
}

// joinKeyPath appends key to path with a dot, or returns key alone when
// path is the document root.
func joinKeyPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
