package render

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// jinjaEngine is the one implementation of Engine.
//
// It is a Flyweight cache over compiled templates, the same pattern
// internal/engine/cel.go applies to compiled CEL programs and for the same
// reason: a compiled template is immutable and stateless once built, so
// every caller compiling identical source can share one instance rather
// than each keeping a privately parsed copy. A long-lived controller
// process renders the same handful of injector templates on every launch,
// and a credential type's injectors do not change between renders.
type jinjaEngine struct {
	mu    sync.Mutex
	cache map[string]Template
}

// New returns the renderer.
//
// There is deliberately no package-level default instance and no
// render.Default(). A caller must be handed an Engine by its composition
// root, which is what makes "exactly one renderer" a property of the wiring
// rather than a convention: a second renderer cannot appear because someone
// forgot to pass one in.
func New() Engine {
	return &jinjaEngine{cache: make(map[string]Template)}
}

// compiled is one parsed template, safe to share across goroutines because
// nothing mutates it after Compile returns.
type compiled struct {
	source string
	nodes  []node
	names  []string
}

// Compile parses source and returns a shared, cached Template.
//
// The miss path parses without holding the lock, because parsing does real
// work and there is no shared mutable state to protect during it, then
// stores the result only if no other goroutine has already won that key.
// Concurrent first-time compiles of the same source therefore converge on
// one shared winner instead of each keeping a separate copy.
func (e *jinjaEngine) Compile(source string) (Template, error) {
	e.mu.Lock()
	if cached, ok := e.cache[source]; ok {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()

	nodes, err := parse(source)
	if err != nil {
		return nil, err
	}
	tmpl := &compiled{source: source, nodes: nodes, names: collectNames(nodes)}

	e.mu.Lock()
	defer e.mu.Unlock()
	if cached, ok := e.cache[source]; ok {
		// Another goroutine won the race for this exact source while we
		// were parsing. Share theirs so every caller of this source
		// converges on one instance.
		return cached, nil
	}
	// Past the bound the template is returned uncached rather than
	// evicting something. See maxCachedTemplates in limits.go for why a
	// slowdown is the right degradation here and an eviction policy is not.
	if len(e.cache) < maxCachedTemplates {
		e.cache[source] = tmpl
	}
	return tmpl, nil
}

// Render evaluates the template against vars.
//
// On any failure it returns the empty string rather than the output built
// so far. A partially rendered prefix is indistinguishable from a complete
// value at the call site, and this package's call sites inject what they
// are given.
func (t *compiled) Render(vars map[string]any) (string, error) {
	var b strings.Builder

	for _, n := range t.nodes {
		if n.expr == nil {
			b.WriteString(n.text)
		} else {
			out, err := n.expr.eval(vars)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
		}

		// Checked per node rather than once at the end so a filter chain
		// that amplifies its input cannot build a value far past the limit
		// before anybody notices.
		if b.Len() > maxOutputBytes {
			return "", fmt.Errorf("%w: output passed %d bytes", ErrTooLarge, maxOutputBytes)
		}
	}

	return b.String(), nil
}

// Names returns the top-level variable names this template references.
//
// The returned slice is a fresh copy on every call. The Template itself is
// shared across every caller that compiled the same source, so handing out
// the backing array would let one caller's append corrupt another's view.
func (t *compiled) Names() []string {
	out := make([]string, len(t.names))
	copy(out, t.names)
	return out
}

// Source returns the text this template was compiled from.
func (t *compiled) Source() string { return t.source }

// collectNames gathers the sorted, deduplicated set of root names the
// nodes reference. Computed once at compile time because callers use it to
// validate on a write path where it is read repeatedly.
func collectNames(nodes []node) []string {
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.expr != nil {
			seen[n.expr.root] = struct{}{}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
