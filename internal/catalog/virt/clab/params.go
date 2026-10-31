// The parameters of a virt.clab method: read once, checked against the
// parameters the method declares, and turned into the request the server
// wants.
package clab

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// nameRE is what a lab, node or interface may be called: containerlab's own
// characters, which also keep each one a single path segment or query value.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// imageRE is an image reference: a registry path, a tag or a digest, with
// nothing a request line could read as more.
var imageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]{0,254}$`)

// args is a method's parameters, read through accessors that keep the first
// refusal, so a handler reads what it needs and checks once.
type args struct {
	m   map[string]any
	err error
}

// reserved are the keys the engine and the runbook may add to any task.
var reserved = map[string]bool{"target": true, "within": true}

// newArgs holds m to the parameters spec declares: a key it does not name is
// refused, as is a required one that is missing.
func newArgs(fqcn string, spec []collection.Param, m map[string]any) (*args, error) {
	known := map[string]bool{}
	for _, p := range spec {
		known[p.Name] = true
	}
	var unknown []string
	for k := range m {
		if !known[k] && !reserved[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s: no parameter %s", fqcn, strings.Join(unknown, ", "))
	}
	for _, p := range spec {
		if p.Required {
			if v, ok := m[p.Name]; !ok || v == nil || v == "" {
				return nil, fmt.Errorf("%s: %s is required", fqcn, p.Name)
			}
		}
	}
	return &args{m: m}, nil
}

// fail records the first refusal.
func (a *args) fail(format string, v ...any) {
	if a.err == nil {
		a.err = fmt.Errorf(format, v...)
	}
}

// str returns a text parameter, "" when absent.
func (a *args) str(name string) string {
	v, ok := a.m[name]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		a.fail("%s must be text, not %T", name, v)
	}
	return s
}

// ident returns a name parameter held to nameRE ("" when absent).
func (a *args) ident(name string) string {
	s := a.str(name)
	if s != "" && !nameRE.MatchString(s) {
		a.fail("%s %q is not a name (letters, digits, ., _ and -)", name, s)
	}
	return s
}

// image returns an image reference parameter.
func (a *args) image(name string) string {
	s := a.str(name)
	if s != "" && !imageRE.MatchString(s) {
		a.fail("%s %q is not an image reference", name, s)
	}
	return s
}

// flag returns a bool parameter, false when absent.
func (a *args) flag(name string) bool {
	v, ok := a.m[name]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		a.fail("%s must be true or false, not %T", name, v)
	}
	return b
}

// number returns a numeric parameter and whether it was given.
func (a *args) number(name string) (float64, bool) {
	v, ok := a.m[name]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f, true
		}
	}
	a.fail("%s must be a number, not %v", name, v)
	return 0, false
}

// integer returns a whole-number parameter and whether it was given.
func (a *args) integer(name string) (int, bool) {
	f, ok := a.number(name)
	if ok && f != float64(int(f)) {
		a.fail("%s must be a whole number, not %v", name, f)
	}
	return int(f), ok
}

// list returns a list-of-text parameter and whether it was given.
func (a *args) list(name string) ([]string, bool) {
	v, ok := a.m[name]
	if !ok || v == nil {
		return nil, false
	}
	var out []string
	switch items := v.(type) {
	case []string:
		out = items
	case []any:
		for _, it := range items {
			s, isText := it.(string)
			if !isText {
				a.fail("%s must be a list of text, and holds %T", name, it)
				return nil, true
			}
			out = append(out, s)
		}
	default:
		a.fail("%s must be a list of text, not %T", name, v)
	}
	return out, true
}

// dict returns a mapping parameter, nil when absent.
func (a *args) dict(name string) map[string]any {
	v, ok := a.m[name]
	if !ok || v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		a.fail("%s must be a mapping, not %T", name, v)
	}
	return m
}

// dicts returns a list-of-mappings parameter, nil when absent.
func (a *args) dicts(name string) []map[string]any {
	v, ok := a.m[name]
	if !ok || v == nil {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		if typed, isTyped := v.([]map[string]any); isTyped {
			return typed
		}
		a.fail("%s must be a list of mappings, not %T", name, v)
		return nil
	}
	var out []map[string]any
	for _, it := range items {
		m, isMap := it.(map[string]any)
		if !isMap {
			a.fail("%s must be a list of mappings, and holds %T", name, it)
			return nil
		}
		out = append(out, m)
	}
	return out
}

// jsonBody returns v as a request body, nil when v is nil.
func jsonBody(v any) io.Reader {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Only a value this package built can reach here, and none holds a
		// channel or a function.
		panic(fmt.Sprintf("clab: encoding a request: %v", err))
	}
	return bytes.NewReader(b)
}

// tlsFor is the device's TLS for an https base URL, nil for an http one.
func tlsFor(base *url.URL, settings devicetls.Settings, secrets map[string]string) (*tls.Config, error) {
	if base.Scheme != "https" {
		return nil, nil
	}
	return settings.Config(secrets)
}

// query builds url.Values from pairs, leaving out every empty value.
func query(pairs ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	return q
}
