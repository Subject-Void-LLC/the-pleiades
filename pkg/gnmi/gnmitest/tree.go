// Package gnmitest: the data a test server holds, one value per path, and
// how a request's path finds it: a stored value at or under the path (a
// wildcard matching any one element or key, "..." any run of elements), or,
// for a path inside a stored JSON value, the member it names.
package gnmitest

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
)

// entry is one stored value.
type entry struct {
	path gnmi.Path
	val  *gpb.TypedValue
}

// tree is the stored values, by path as written.
type tree map[string]entry

// clone is a copy of t a Set changes, so a refused Set changes nothing.
func (t tree) clone() tree {
	return maps.Clone(t)
}

// put stores val at p.
func (t tree) put(p gnmi.Path, val *gpb.TypedValue) {
	t[p.String()] = entry{path: p, val: val}
}

// match returns the entries at or under q, sorted by path, and, when none is
// and q has no wildcard, the member of a stored JSON value above q that q
// names, as one entry at q.
func (t tree) match(q gnmi.Path) []entry {
	var out []entry
	for _, e := range t {
		if e.path.Origin == q.Origin && covers(q.Elems, e.path.Elems) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path.String() < out[j].path.String() })
	if len(out) > 0 || q.HasWildcard() {
		return out
	}
	for n := len(q.Elems) - 1; n >= 0; n-- {
		above, ok := t[gnmi.Path{Origin: q.Origin, Elems: q.Elems[:n]}.String()]
		if !ok {
			continue
		}
		raw, ok := jsonOf(above.val)
		if !ok {
			return nil
		}
		member, found := descend(raw, q.Elems[n:])
		if !found {
			return nil
		}
		return []entry{{path: q, val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: member}}}}
	}
	return nil
}

// remove deletes every entry at or under q, and q's member of a stored JSON
// value above it, reporting whether anything was there.
func (t tree) remove(q gnmi.Path) bool {
	removed := false
	for key, e := range t {
		if e.path.Origin == q.Origin && covers(q.Elems, e.path.Elems) {
			delete(t, key)
			removed = true
		}
	}
	for n := len(q.Elems) - 1; n >= 0 && !removed; n-- {
		key := gnmi.Path{Origin: q.Origin, Elems: q.Elems[:n]}.String()
		above, ok := t[key]
		if !ok {
			continue
		}
		raw, ok := jsonOf(above.val)
		if !ok {
			return false
		}
		if out, changed := removeMember(raw, q.Elems[n:]); changed {
			t[key] = entry{path: above.path, val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: out}}}
			return true
		}
		return false
	}
	return removed
}

// covers reports whether the request elements q name a node at or above the
// stored elements e: each of q's elements matches e's in turn, a "*" any one,
// a "..." any run, and a q element without keys a whole list.
func covers(q, e []gnmi.Elem) bool {
	if len(q) == 0 {
		return true
	}
	if q[0].Name == gnmi.WildcardDeep {
		for i := 0; i <= len(e); i++ {
			if covers(q[1:], e[i:]) {
				return true
			}
		}
		return false
	}
	if len(e) == 0 || !elemMatches(q[0], e[0]) {
		return false
	}
	return covers(q[1:], e[1:])
}

// elemMatches reports whether a request element matches a stored one.
func elemMatches(q, e gnmi.Elem) bool {
	if q.Name != gnmi.WildcardAny && q.Name != e.Name {
		return false
	}
	for k, v := range q.Keys {
		if got, ok := e.Keys[k]; !ok || (v != "*" && v != got) {
			return false
		}
	}
	return true
}

// jsonOf returns a JSON value's bytes.
func jsonOf(v *gpb.TypedValue) ([]byte, bool) {
	switch v.GetValue().(type) {
	case *gpb.TypedValue_JsonIetfVal:
		return v.GetJsonIetfVal(), true
	case *gpb.TypedValue_JsonVal:
		return v.GetJsonVal(), true
	}
	return nil, false
}

// descend finds the member elems name inside raw: each element an object
// member by its name (or module:name, as RFC 7951 qualifies one), and one
// with keys the list entry whose fields hold those values.
func descend(raw []byte, elems []gnmi.Elem) ([]byte, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	for _, e := range elems {
		next, ok := step(v, e)
		if !ok {
			return nil, false
		}
		v = next
	}
	out, err := json.Marshal(v)
	return out, err == nil
}

// step is one element's move into v.
func step(v any, e gnmi.Elem) (any, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	member, ok := memberOf(obj, e.Name)
	if !ok {
		return nil, false
	}
	if len(e.Keys) == 0 {
		return obj[member], true
	}
	list, ok := obj[member].([]any)
	if !ok {
		return nil, false
	}
	for _, item := range list {
		if entryHasKeys(item, e.Keys) {
			return item, true
		}
	}
	return nil, false
}

// memberOf returns the member of obj named name or module:name.
func memberOf(obj map[string]any, name string) (string, bool) {
	if _, ok := obj[name]; ok {
		return name, true
	}
	for k := range obj {
		if _, local, found := cut(k); found && local == name {
			return k, true
		}
	}
	return "", false
}

// cut splits a qualified member name at its colon.
func cut(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}

// entryHasKeys reports whether a list entry's fields hold every key.
func entryHasKeys(item any, keys map[string]string) bool {
	obj, ok := item.(map[string]any)
	if !ok {
		return false
	}
	for k, want := range keys {
		member, ok := memberOf(obj, k)
		if !ok || fmt.Sprint(obj[member]) != want {
			return false
		}
	}
	return true
}

// removeMember removes the member elems name from raw.
func removeMember(raw []byte, elems []gnmi.Elem) ([]byte, bool) {
	var root any
	if json.Unmarshal(raw, &root) != nil || len(elems) == 0 {
		return nil, false
	}
	parent := root
	for _, e := range elems[:len(elems)-1] {
		next, ok := step(parent, e)
		if !ok {
			return nil, false
		}
		parent = next
	}
	obj, ok := parent.(map[string]any)
	if !ok {
		return nil, false
	}
	last := elems[len(elems)-1]
	member, ok := memberOf(obj, last.Name)
	if !ok {
		return nil, false
	}
	if len(last.Keys) == 0 {
		delete(obj, member)
	} else {
		list, _ := obj[member].([]any)
		kept := list[:0]
		found := false
		for _, item := range list {
			if entryHasKeys(item, last.Keys) {
				found = true
				continue
			}
			kept = append(kept, item)
		}
		if !found {
			return nil, false
		}
		obj[member] = kept
	}
	out, err := json.Marshal(root)
	return out, err == nil
}

// merge is update into base, both JSON: objects merged member by member,
// anything else replaced.
func merge(base, update []byte) ([]byte, error) {
	var a, b any
	if err := json.Unmarshal(base, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(update, &b); err != nil {
		return nil, err
	}
	return json.Marshal(mergeValues(a, b))
}

// mergeValues merges b into a.
func mergeValues(a, b any) any {
	ao, aok := a.(map[string]any)
	bo, bok := b.(map[string]any)
	if !aok || !bok {
		return b
	}
	for k, v := range bo {
		ao[k] = mergeValues(ao[k], v)
	}
	return ao
}
