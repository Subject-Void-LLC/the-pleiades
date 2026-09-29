// Package render provides the one template renderer this platform has.
//
// PLAN.md Section 25 lists a "Template renderer (one Jinja compatible
// engine, compile and cache)" among the Build Once Contracts, with four
// declared call sites: credential injectors, notification messages,
// constructed inventory, and survey defaults. A contract listed there has
// exactly one implementation in the codebase, and a second implementation
// is a defect rather than a variation. This package is that one
// implementation, behind a port, so the rule is enforced by the type a
// caller holds rather than by review.
//
// The renderer exists because an AWX credential type writes every injector
// value as a Jinja template over the type's own input ids, for example
// {{ api_token }}. There is no way to run an imported AWX credential type
// without evaluating those, so this is a hard requirement of Phase 22
// rather than a convenience.
//
// # What this deliberately is not
//
// This is not Jinja2. It is a strict, closed subset of Jinja2's expression
// syntax, chosen so that the whole grammar can be fuzzed and reasoned
// about. Statement blocks, comments, and every filter outside a closed set
// of nine are refused loudly at compile time rather than passed through as
// literal text, because silent passthrough is how an author comes to
// believe a loop ran.
//
// # Strict undefined
//
// The single most important rule here: a template that references a name
// absent from the supplied variables is an error, never the empty string.
// Jinja2's own default is the opposite, and the opposite is dangerous in
// this codebase's first call site. An injector rendering to "" does not
// look like a failure at any layer below it: the environment variable is
// still set, the playbook still runs, and the authentication failure that
// results is attributed to the wrong thing. An error at render time names
// the missing input instead.
//
// The one escape is the default filter, which makes an optional input
// optional out loud: {{ region | default('us-east-1') }}.
//
// # Moving the failure left
//
// Template.Names reports every top-level name a compiled template
// references. That is what lets a caller prove at save time that a
// credential type's injectors only name inputs the type declares, so an
// undefined-variable failure surfaces when an author saves the type rather
// than when an operator launches a job. Architecture Principle 5, type
// safety moves left, is the reason Names exists at all.
package render

import (
	"errors"
	"fmt"
)

// Errors this package returns. Every one is checked with errors.Is by at
// least one caller, so none of them may be replaced by a bare fmt.Errorf.
var (
	// ErrSyntax reports template source this grammar cannot parse. It
	// covers refusals as well as malformed input: a {% %} statement block
	// is syntactically valid Jinja2 and still an ErrSyntax here, because
	// this subset does not implement it and passing it through as text
	// would be a silent lie about what ran.
	ErrSyntax = errors.New("render: template is not valid")

	// ErrUndefined reports a reference to a name that was not supplied.
	// This is the strict-undefined rule described in the package comment,
	// and it is the error a caller most wants to distinguish, because it
	// is the one that names a missing input rather than a broken template.
	ErrUndefined = errors.New("render: template references a variable that was not supplied")

	// ErrUnknownFilter reports a filter outside the closed set. The set is
	// closed rather than extensible on purpose; see filter.go.
	ErrUnknownFilter = errors.New("render: unknown filter")

	// ErrNotRenderable reports a value that resolved successfully but has
	// no safe string form, such as a map or a list reached without the
	// to_json filter. Go's default formatting of a map is not something
	// any caller wants injected into an environment variable, and null is
	// not the empty string.
	ErrNotRenderable = errors.New("render: value has no string form")

	// ErrTooLarge reports template source or rendered output past the
	// limits in limits.go.
	ErrTooLarge = errors.New("render: template or its output exceeds the size limit")
)

// UndefinedError names the variable a template referenced and did not get.
//
// It carries a name and never a value. Every caller of this package
// renders secrets, and this error will be logged, returned over HTTP, and
// attached to a job record. A value in here would defeat every masking
// control downstream of it.
type UndefinedError struct {
	// Name is the reference that could not be resolved, in the template's
	// own spelling, so "tower.filename.cert" rather than "tower".
	Name string
}

// Error implements error.
func (e *UndefinedError) Error() string {
	return fmt.Sprintf("render: no value supplied for %q", e.Name)
}

// Is reports that an UndefinedError matches ErrUndefined, so callers can
// write errors.Is(err, render.ErrUndefined) without knowing this type.
func (e *UndefinedError) Is(target error) bool { return target == ErrUndefined }

// Engine compiles template source into a reusable, cached Template.
//
// This is the port PLAN.md Section 25's "exactly one implementation" rule
// attaches to. A caller takes an Engine, never a concrete type, and this
// package exposes no package-level default instance: an accidental second
// renderer cannot appear by omission, only by somebody typing one, and
// internal/archtest fails the build when they do.
type Engine interface {
	// Compile parses source and returns a Template ready to render. The
	// same source compiled twice returns one shared, immutable Template.
	Compile(source string) (Template, error)
}

// Template is one compiled template, safe for concurrent use.
type Template interface {
	// Render evaluates the template against vars under strict-undefined
	// semantics: a referenced name absent from vars is an error, never the
	// empty string.
	//
	// Render never returns a partially rendered prefix alongside an error.
	// On any failure it returns "" and the error, so a caller cannot
	// mistake truncated output for a value.
	Render(vars map[string]any) (string, error)

	// Names is every top-level variable name this template references,
	// sorted and deduplicated. A caller validates against this at save
	// time so an undefined-variable failure never waits for a launch.
	//
	// Names reports the root of each reference, so "{{ tower.filename }}"
	// reports "tower". A name guarded by the default filter is still
	// reported: it is optional, not absent from the template.
	Names() []string

	// Source is the template text this was compiled from, returned
	// unchanged so an error message or an audit record can quote it.
	Source() string

	// Value evaluates a template that is exactly one expression, with no
	// text around it, to the value its filters leave, keeping its type: a
	// list stays a list and a number a number, so a task parameter written
	// as "{{ nodes.x }}" hands a method the data rather than its text. It
	// reports false, and evaluates nothing, for any other template, whose
	// caller renders text instead. Strict undefined applies as in Render,
	// and so does the output bound, to a value that is text.
	Value(vars map[string]any) (any, bool, error)

	// Expressions describes each expression the template holds, in order:
	// the path it reads and the filters it applies. It is what lets a
	// caller check at save time what a template reads (a register written
	// by an earlier task) and how it ends (quote on a command line).
	Expressions() []ExpressionInfo
}

// ExpressionInfo describes one {{ ... }} expression in a template.
type ExpressionInfo struct {
	// Path is the reference it reads, root first, then each key or index
	// as the author wrote it: "nodes", "ticket", "json" for
	// {{ nodes.ticket.json }}, and "hosts", "0" for {{ hosts[0] }}.
	Path []string

	// Filters are the filter names it applies, left to right.
	Filters []string
}
