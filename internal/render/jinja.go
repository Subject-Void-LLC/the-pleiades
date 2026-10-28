package render

import (
	"fmt"
	"sort"
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
	cache map[cacheKey]Template
}

// cacheKey is what decides a compiled template: its source and the options
// it was compiled with, since the same text compiles to different output
// under trim_blocks.
type cacheKey struct {
	source string
	opts   Options
}

// New returns the renderer.
//
// There is deliberately no package-level default instance and no
// render.Default(). A caller must be handed an Engine by its composition
// root, which is what makes "exactly one renderer" a property of the wiring
// rather than a convention: a second renderer cannot appear because someone
// forgot to pass one in.
func New() Engine {
	return &jinjaEngine{cache: make(map[cacheKey]Template)}
}

// compiled is one parsed template, safe to share across goroutines because
// nothing mutates it after Compile returns.
type compiled struct {
	source string
	nodes  []node
	names  []string

	// plain reports a template with no statement and no comment, the only
	// kind Value evaluates as one typed expression.
	plain bool
}

// Compile parses source and returns a shared, cached Template.
//
// The miss path parses without holding the lock, because parsing does real
// work and there is no shared mutable state to protect during it, then
// stores the result only if no other goroutine has already won that key.
// Concurrent first-time compiles of the same source therefore converge on
// one shared winner instead of each keeping a separate copy.
func (e *jinjaEngine) Compile(source string) (Template, error) {
	return e.CompileWith(source, Options{})
}

// CompileWith implements Engine: Compile under opts, cached per source and
// options together.
func (e *jinjaEngine) CompileWith(source string, opts Options) (Template, error) {
	key := cacheKey{source: source, opts: opts}
	e.mu.Lock()
	if cached, ok := e.cache[key]; ok {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()

	nodes, plain, err := parse(source, opts)
	if err != nil {
		return nil, err
	}
	// The scope analysis runs once here, both to refuse a template whose
	// names it cannot bound and to fix Names, which callers read often.
	exprs, err := analyze(nodes)
	if err != nil {
		return nil, err
	}
	tmpl := &compiled{source: source, nodes: nodes, names: collectNames(exprs), plain: plain}

	e.mu.Lock()
	defer e.mu.Unlock()
	if cached, ok := e.cache[key]; ok {
		// Another goroutine won the race for this exact source while we
		// were parsing. Share theirs so every caller of this source
		// converges on one instance.
		return cached, nil
	}
	// Past the bound the template is returned uncached rather than
	// evicting something. See maxCachedTemplates in limits.go for why a
	// slowdown is the right degradation here and an eviction policy is not.
	if len(e.cache) < maxCachedTemplates {
		e.cache[key] = tmpl
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
	st := &renderState{vars: vars}
	if err := st.run(t.nodes); err != nil {
		return "", err
	}
	return st.out.String(), nil
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

// Value implements Template: a template that is one expression and nothing
// else evaluates to that expression's value, typed.
func (t *compiled) Value(vars map[string]any) (any, bool, error) {
	if !t.plain || len(t.nodes) != 1 || t.nodes[0].kind != nodeOutput {
		return nil, false, nil
	}
	value, err := t.nodes[0].expr.evalValue(&renderState{vars: vars})
	if err != nil {
		return nil, true, err
	}
	if s, isText := value.(string); isText && len(s) > maxOutputBytes {
		return nil, true, fmt.Errorf("%w: output passed %d bytes", ErrTooLarge, maxOutputBytes)
	}
	return value, true, nil
}

// Expressions implements Template. The analysis is run again on each call
// rather than kept, since most compiled templates are never asked, and its
// output is bounded by maxBindingOrigins per expression. It cannot fail
// here: Compile ran it already and refused the template it fails on.
func (t *compiled) Expressions() []ExpressionInfo {
	out, _ := analyze(t.nodes)
	return out
}

// collectNames gathers the sorted, deduplicated set of root names the
// expressions read from the caller. A name a statement bound is never one:
// analysis reports a read through it as a read of what it was bound from.
func collectNames(exprs []ExpressionInfo) []string {
	seen := make(map[string]struct{})
	for _, e := range exprs {
		seen[e.Path[0]] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
