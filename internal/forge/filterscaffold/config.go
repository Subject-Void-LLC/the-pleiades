package filterscaffold

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/genutil"
)

// celNamePattern is the legal shape of a filter's CEL-facing name, the
// part written after "filters." in a runbook condition. It is
// deliberately camelCase, matching the convention cel-go's own extension
// libraries use (base64.encode, json.encode) and this project's own
// filters.safeInt/safeFloat/safeBool (PLAN.md Section 36): a leading
// lowercase letter, then any run of letters or digits, no underscores.
var celNamePattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// paramNamePattern is the legal shape of a Param.Name: an unexported Go
// identifier, camelCase permitted (Go convention favors "newPrefix" over
// "new_prefix" for a parameter, unlike a package-name segment, so this
// intentionally does not reuse genutil.ValidateIdentSegment's
// snake_case-only pattern).
var paramNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// goNamePattern is the legal shape of GoName: an exported Go identifier.
// Unlike CELName, this is not run through genutil.ValidateIdentSegment
// (that helper's own pattern forces every character but the first to be
// lowercase, built for a snake_case segment ToExportedIdent capitalizes
// itself, not for validating an already-PascalCase name like
// "CIDRToNetmask" whose acronym stays uppercase throughout). A leading
// uppercase ASCII letter, then any run of Go identifier characters, is
// the actual and only requirement here; go/parser on Generate's own
// output is what proves the result is real Go, the same way every other
// scaffolder in this family ultimately proves it.
var goNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// wellKnownCELTypes maps a Go type spelling this generator understands
// end to end (it can derive a real CEL type expression, a real
// ref.Val<->Go conversion, and a real wrapped return) to the cel-go type
// expression a filters.<name> overload declares for it. Every other Go
// type is still accepted (a filter is free to take or return a list, a
// map, or anything else CEL can represent), but Generate cannot infer
// its CEL type or write real conversion code for it, so the caller must
// supply CELType explicitly and Reminder emits a TODO conversion in its
// place instead of guessing.
var wellKnownCELTypes = map[string]string{
	"string": "cel.StringType",
	"int":    "cel.IntType",
	"bool":   "cel.BoolType",
}

// Param is one argument a generated filter function takes, on both the
// Go side (pkg/filters) and the CEL side (the cel.Overload this filter
// registers).
type Param struct {
	// Name is the argument's name, used as the Go parameter name and in
	// generated examples. Must satisfy paramNamePattern: a lowercase
	// first letter, camelCase permitted after it.
	Name string

	// GoType is the argument's Go type, exactly as it appears in the
	// generated pkg/filters function signature (e.g. "string", "int",
	// "[]string"). Not validated against Go's grammar; Generate's own
	// go/parser check on its output is what actually proves it compiles.
	GoType string

	// CELType is the raw cel-go type expression for this argument's CEL
	// overload declaration (e.g. "cel.StringType"). May be left empty
	// when GoType is one of wellKnownCELTypes' keys, in which case
	// Validate fills it in; required otherwise.
	CELType string
}

// Return is a generated filter function's single result, on both the Go
// side and the CEL side. Shaped identically to Param but named
// separately because a return value has no argument Name and the two are
// never interchangeable in a template.
type Return struct {
	GoType  string
	CELType string
}

// Config is the input to Generate: everything needed to emit one new
// pkg/filters function and its starter test, plus (via Reminder) the CEL
// registration block a human pastes into internal/engine/cel_filters.go.
//
// Unlike collectionscaffold/pluginscaffold/viewscaffold, a filter has no
// per-entity package of its own: every filter in this codebase lives in
// the single, flat pkg/filters package (PLAN.md Section 36's own "one
// flat filters. prefix" rule), so Config carries two independent names
// rather than deriving one from the other. GoName ("CIDRToNetmask") and
// CELName ("cidrToNetmask") diverge on acronym casing in a way no
// mechanical rule can safely reverse (capitalizing only CELName's first
// rune would produce "CIDR" wrong as "CIDRToNetmask" only by accident,
// and would mangle anything whose CEL name does not happen to start with
// an acronym), so both are required, explicit inputs rather than one
// being computed from the other.
type Config struct {
	// GoName is the exported Go function name Generate writes into
	// pkg/filters, for example "CIDRToNetmask".
	GoName string

	// CELName is the bare name after the "filters." prefix, for example
	// "cidrToNetmask". Consumed by cel.Function("filters."+CELName, ...)
	// in the Reminder block, and by nothing else: it is never used as a
	// Go identifier.
	CELName string

	// Category names the pkg/filters/<Category>.go file this filter's Go
	// function and test are written into (grouped by PLAN.md Section
	// 36's own category table, e.g. "network", "structured"), a single
	// genutil-validated segment.
	Category string

	// Summary is one sentence describing what this filter does. It
	// becomes the generated function's doc comment and (verbatim) the
	// cel.FunctionDocs text in Reminder's output, the same source
	// tools/gendocs later reads to build the generated filter reference,
	// so this is the one piece of prose Generate/Reminder do not
	// duplicate.
	Summary string

	// Params is this filter's ordered argument list. May be empty (a
	// zero-argument filter, e.g. a clock read, though nothing in this
	// codebase needs one yet).
	Params []Param

	// Return is this filter's single result.
	Return Return
}

// resolvedParam and resolvedReturn are Param/Return after Validate has
// filled in any empty CELType from wellKnownCELTypes, so Generate and
// Reminder never re-run that lookup themselves.
type resolvedParam struct {
	Name    string
	GoType  string
	CELType string
}

type resolvedReturn struct {
	GoType  string
	CELType string
}

// Validate reports whether cfg is safe to generate from, and resolves any
// omitted well-known CELType along the way. It fails closed on anything
// that would otherwise produce unbuildable Go, an unsafe path, or a CEL
// declaration Reminder cannot render: an unknown GoType with no explicit
// CELType, an empty Summary (the same discipline collectionscaffold's own
// Doc.Summary requires, since this text becomes the generated filter
// reference's own copy), and a Return with no GoType (a filter with no
// result is not a filter).
func (c Config) validate() ([]resolvedParam, resolvedReturn, error) {
	// No Go-keyword check is needed here: every Go keyword is spelled
	// entirely in lowercase, and goNamePattern already requires an
	// uppercase first letter, so an exported name can never collide with
	// one.
	if !goNamePattern.MatchString(c.GoName) {
		return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: invalid GoName %q: must match %s", c.GoName, goNamePattern.String())
	}
	if !celNamePattern.MatchString(c.CELName) {
		return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: invalid CELName %q: must match %s", c.CELName, celNamePattern.String())
	}
	if err := genutil.ValidateSegment(c.Category); err != nil {
		return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: invalid Category %q: %w", c.Category, err)
	}
	if strings.TrimSpace(c.Summary) == "" {
		return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: filter %q has no Summary", c.CELName)
	}
	if strings.TrimSpace(c.Return.GoType) == "" {
		return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: filter %q has no Return.GoType", c.CELName)
	}

	seen := map[string]bool{}
	params := make([]resolvedParam, len(c.Params))
	for i, p := range c.Params {
		if !paramNamePattern.MatchString(p.Name) {
			return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: filter %q param %d: invalid Name %q: must match %s", c.CELName, i, p.Name, paramNamePattern.String())
		}
		if seen[p.Name] {
			return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: filter %q: duplicate param name %q", c.CELName, p.Name)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.GoType) == "" {
			return nil, resolvedReturn{}, fmt.Errorf("filterscaffold: filter %q param %q: no GoType", c.CELName, p.Name)
		}
		celType := p.CELType
		if celType == "" {
			var ok bool
			celType, ok = wellKnownCELTypes[p.GoType]
			if !ok {
				return nil, resolvedReturn{}, fmt.Errorf(
					"filterscaffold: filter %q param %q: GoType %q is not well-known (%s); pass an explicit CELType",
					c.CELName, p.Name, p.GoType, knownTypeList())
			}
		}
		params[i] = resolvedParam{Name: p.Name, GoType: p.GoType, CELType: celType}
	}

	ret := c.Return
	celType := ret.CELType
	if celType == "" {
		var ok bool
		celType, ok = wellKnownCELTypes[ret.GoType]
		if !ok {
			return nil, resolvedReturn{}, fmt.Errorf(
				"filterscaffold: filter %q: return GoType %q is not well-known (%s); pass an explicit CELType",
				c.CELName, ret.GoType, knownTypeList())
		}
	}

	return params, resolvedReturn{GoType: ret.GoType, CELType: celType}, nil
}

// PackagePath returns pkg/filters, the one directory every generated
// filter's Go source lives in regardless of Category.
func (c Config) PackagePath() string {
	return "pkg/filters"
}

// knownTypeList renders wellKnownCELTypes' keys for an error message,
// sorted so the message is deterministic across runs.
func knownTypeList() string {
	return "string, int, bool"
}
