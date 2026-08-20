// Package filterscaffold generates a new pkg/filters function and its
// starter test: the PLAN.md Section 36 "pure value transform" a runbook's
// when/when_or/when_cel can call, distinct from a Collection method the
// way that section's own intro states plainly (no device, no capability,
// no execution context, never a Task.FQCN).
//
// It is the Forge's fourth generator, after
// internal/inventory/devicescaffold, internal/forge/collectionscaffold,
// and internal/forge/pluginscaffold/viewscaffold, and it deliberately
// copies their shape where it fits: raw-string text/template sources
// post-processed through go/format.Source, no filesystem I/O in this
// package, and paths returned relative to the repository root for the
// caller to write or discard. It diverges from all four in one way that
// PLAN.md Section 36 itself forces: every filter lives in the single,
// flat pkg/filters package (there is no per-filter directory the way
// there is a per-method or per-plugin one), and a filter's registration
// is a hand-maintained cel.Function block inside
// internal/engine/cel_filters.go's filtersLibrary.CompileOptions, not an
// init() this generator can emit into a blank-importable package of its
// own. Generate therefore only ever writes into pkg/filters/<Category>.go
// and its _test.go sibling; Reminder renders the CEL registration block
// as text for a human to paste, mirroring viewscaffold.Reminder's own
// "the one step no generator can perform" pattern, for the same reason:
// cel_filters.go is one hand-maintained file, not a directory a blank
// import can silently wire in.
package filterscaffold

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"strings"
	"text/template"
)

// GeneratedFile is one file Generate produces: a path relative to the
// repository root, and its gofmt-clean contents.
type GeneratedFile struct {
	Path    string
	Content []byte
}

// implTemplateData is the fully-resolved, string-only data
// implFuncTemplate renders from.
type implTemplateData struct {
	GoName     string
	CELName    string
	Category   string
	Summary    string
	ParamList  string // "cidr string, newPrefix int"
	ReturnType string
}

// testTemplateData is the fully-resolved data testFuncTemplate renders
// from.
type testTemplateData struct {
	GoName  string
	CELName string
}

var (
	implFuncTemplate = template.Must(template.New("filter_impl").Parse(implFuncTemplateSource))
	testFuncTemplate = template.Must(template.New("filter_test").Parse(testFuncTemplateSource))
)

// Generate renders cfg into a new pkg/filters function and its starter
// test. It performs no filesystem I/O; the caller
// (cmd/pleiades/forge_new_filter.go) decides where, or whether, to write
// the returned files, and whether an existing pkg/filters/<Category>.go
// means appending rather than creating (see AppendOrCreate).
func Generate(cfg Config) ([]GeneratedFile, error) {
	params, ret, err := cfg.validate()
	if err != nil {
		return nil, err
	}

	implData := implTemplateData{
		GoName:     cfg.GoName,
		CELName:    cfg.CELName,
		Category:   cfg.Category,
		Summary:    cfg.Summary,
		ParamList:  goParamList(params),
		ReturnType: ret.GoType,
	}
	implSrc, err := renderAndFormat(implFuncTemplate, implData)
	if err != nil {
		return nil, fmt.Errorf("filterscaffold: rendering %s: %w", cfg.GoName, err)
	}

	testData := testTemplateData{GoName: cfg.GoName, CELName: cfg.CELName}
	testSrc, err := renderAndFormat(testFuncTemplate, testData)
	if err != nil {
		return nil, fmt.Errorf("filterscaffold: rendering %s test: %w", cfg.GoName, err)
	}

	// One file per filter, not one shared file per Category: every other
	// scaffolder in this family generates a brand-new file and refuses to
	// overwrite an existing one (cmd/pleiades/forge_scaffold_io.go's
	// writeGeneratedFile), and pkg/filters has no per-entity directory
	// the way a Collection method or sync plugin does to make an
	// append-to-existing-file story safe. A human is free to hand-merge
	// several filters' generated files into one hand-organized category
	// file afterward (Phase 50's own pkg/filters/cast.go groups three
	// functions this way); that consolidation is an ordinary edit, not
	// something this generator needs to solve.
	base := cfg.PackagePath() + "/" + camelToSnake(cfg.CELName)
	return []GeneratedFile{
		{Path: base + ".go", Content: implSrc},
		{Path: base + "_test.go", Content: testSrc},
	}, nil
}

// Reminder renders the CEL registration block a human pastes into
// internal/engine/cel_filters.go's filtersLibrary.CompileOptions, plus
// the binding function it calls, exactly the shape
// internal/engine/cel_filters.go's own filters.safeInt/safeFloat/safeBool
// entries already use: a cel.Function carrying FunctionDocs, one
// cel.Overload carrying an ID, its argument/result types, and
// OverloadExamples, and a *Binding function translating ref.Val to and
// from cfg.GoName's real Go signature.
//
// A parameter or return whose GoType is not in wellKnownCELTypes gets a
// "// TODO" conversion line instead of real code: this generator can
// prove a well-known type's ref.Val conversion is correct by construction
// (wellKnownCELTypes' own doc comment lists all seven and why each one
// qualifies), but it cannot safely guess one for an arbitrary Go type
// like []int without risking a silently wrong translation pasted
// straight into a hand-maintained file. A human fills those in, the same
// way a human fills in every stub this generator (and every scaffolder
// before it) produces.
func Reminder(cfg Config) (string, error) {
	params, ret, err := cfg.validate()
	if err != nil {
		return "", err
	}

	overloadID := overloadIDFor(cfg.CELName, params, ret)

	var celTypes []string
	for _, p := range params {
		celTypes = append(celTypes, p.CELType)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "cel.Function(\"filters.%s\",\n", cfg.CELName)
	fmt.Fprintf(&b, "\tcel.FunctionDocs(\n\t\t%q,\n\t),\n", cfg.Summary)
	fmt.Fprintf(&b, "\tcel.Overload(%q,\n", overloadID)
	fmt.Fprintf(&b, "\t\t[]*cel.Type{%s}, %s,\n", strings.Join(celTypes, ", "), ret.CELType)
	fmt.Fprintf(&b, "\t\tcel.OverloadExamples(\n\t\t\t%q,\n\t\t),\n", exampleFor(cfg.CELName, params))
	fmt.Fprintf(&b, "\t\t%s(%sBinding),\n", bindingFuncFor(len(params)), cfg.CELName)
	b.WriteString("\t),\n")
	b.WriteString("),\n")

	b.WriteString("\n")
	b.WriteString(bindingFunc(cfg.GoName, cfg.CELName, params, ret))

	return b.String(), nil
}

// goParamList renders params as a Go function's parameter list source,
// e.g. "cidr string, newPrefix int".
func goParamList(params []resolvedParam) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name + " " + p.GoType
	}
	return strings.Join(parts, ", ")
}

// snakeTypeRe strips anything that is not a letter or digit from a Go
// type spelling, for building a readable overload ID fragment out of a
// type like "[]string" or "map[string]int" without producing invalid
// characters in the ID.
var snakeTypeRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func snakeType(goType string) string {
	return strings.ToLower(strings.Trim(snakeTypeRe.ReplaceAllString(goType, "_"), "_"))
}

// camelToSnake converts a camelCase CEL name (e.g. "cidrToNetmask") to
// snake_case ("cidr_to_netmask") for use in an overload ID and a
// generated file name, matching internal/engine/cel_filters.go's
// existing "filters_safe_int_dyn_int" convention (from CEL name
// "safeInt").
//
// An uppercase run is treated as one acronym, not one underscore per
// letter: an underscore is inserted before an uppercase rune only at the
// start of a new word, which is either a lowercase-to-uppercase
// transition ("cidrTo" -> "cidr_To") or the last letter of a run of
// uppercase runes immediately followed by a lowercase one ("IPTo" ->
// "IP_To", the boundary inside an acronym like "classifyIP" or
// "macOUI"). Without the second case, an acronym-heavy CEL name is
// mangled letter by letter ("classifyIP" would become "classify_i_p",
// not "classify_ip"); this was a real defect found by using this
// generator against Phase 51's own acronym-heavy names (ClassifyIP,
// MACOUI, ValidateVLAN, ValidateASN), not a hypothetical one.
//
// This is not a universal solution: a name mixing an acronym directly
// against a version-style suffix (ToIPv4MappedIPv6) still splits
// awkwardly, because no purely mechanical rule can tell "IPv4" (one
// token) from "IPServer" (two) without a dictionary. A generated file
// name this ugly is meant to be visibly wrong and renamed by hand, the
// same as any other rough edge in a first-draft scaffold; it is not
// silently wrong the way the letter-by-letter bug was.
func camelToSnake(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if !prevUpper || nextLower {
				b.WriteByte('_')
			}
		}
		if isUpper {
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// overloadIDFor mirrors the "filters_<name snake>_<each param type
// snake>_<return type snake>" shape internal/engine/cel_filters.go's
// existing overloads already use.
func overloadIDFor(celName string, params []resolvedParam, ret resolvedReturn) string {
	parts := []string{"filters", camelToSnake(celName)}
	for _, p := range params {
		parts = append(parts, snakeType(p.GoType))
	}
	parts = append(parts, snakeType(ret.GoType))
	return strings.Join(parts, "_")
}

// bindingFuncFor names the cel-go OverloadOpt matching arity: cel-go
// ships typed bindings for exactly one and two arguments. Zero is also
// fully supported here (cel.FunctionBinding's real signature,
// func(...ref.Val) ref.Val, accepts being called with no arguments;
// bindingFunc emits a real "_ ...ref.Val" parameter for it, not a
// guess), used first by Phase 52's GenerateUUIDv4. Anything wider than
// two still needs the variadic cel.FunctionBinding, but this generator
// cannot pre-fill a real signature for that case beyond a TODO, since a
// fixed arity above two has no typed OverloadOpt to target and no
// established convention in this codebase yet to copy.
func bindingFuncFor(arity int) string {
	switch arity {
	case 0:
		return "cel.FunctionBinding"
	case 1:
		return "cel.UnaryBinding"
	case 2:
		return "cel.BinaryBinding"
	default:
		return "cel.FunctionBinding /* TODO: arity */"
	}
}

// exampleFor renders one plausible cel.OverloadExamples string, a
// starting point a human is expected to correct with a real expected
// output once the function is implemented (this generator has no way to
// know what CIDRToNetmask("10.0.0.0/24") actually returns; only the
// implementation does).
func exampleFor(celName string, params []resolvedParam) string {
	args := make([]string, len(params))
	for i, p := range params {
		args[i] = exampleArg(p.GoType)
	}
	return fmt.Sprintf("filters.%s(%s) // TODO: real expected output", celName, strings.Join(args, ", "))
}

func exampleArg(goType string) string {
	switch goType {
	case "string":
		return `"TODO"`
	case "int":
		return "0"
	case "bool":
		return "false"
	case "[]string":
		return `["TODO"]`
	case "map[string]any":
		return `{"TODO": "TODO"}`
	case "[]any":
		return `["TODO"]`
	case "[]map[string]any":
		return `[{"TODO": "TODO"}]`
	default:
		return "/* TODO */"
	}
}

// bindingFunc renders the *Binding function Reminder's cel.Overload
// references: one parameter per Param, converted from ref.Val via the
// matching wellKnownCELTypes conversion helper (celToString/celToInt/
// celToBool/celToStringList/celToMap/celToDynList/celToMapList, all
// defined once in internal/engine/cel_filters.go), a call into the real
// pkg/filters function, and the result wrapped back into a ref.Val. A
// Param or Return outside wellKnownCELTypes gets a TODO line instead of
// guessed conversion code; see Reminder's own doc comment for why
// guessing would be worse than an explicit gap.
//
// A zero-Param filter (Phase 52's GenerateUUIDv4) gets the real
// cel.FunctionBinding signature, func(...ref.Val) ref.Val, spelled as
// "_ ...ref.Val" since nothing in bindingFuncFor's typed one/two-arg
// case ever reaches here with an unused argument to convert.
func bindingFunc(goName, celName string, params []resolvedParam, ret resolvedReturn) string {
	var b strings.Builder
	fnName := celName + "Binding"

	argNames := make([]string, len(params))
	sig := make([]string, len(params))
	for i := range params {
		argNames[i] = fmt.Sprintf("arg%d", i)
		sig[i] = fmt.Sprintf("%s ref.Val", argNames[i])
	}

	sigStr := strings.Join(sig, ", ")
	if len(params) == 0 {
		sigStr = "_ ...ref.Val"
	}
	fmt.Fprintf(&b, "func %s(%s) ref.Val {\n", fnName, sigStr)

	callArgs := make([]string, len(params))
	for i, p := range params {
		conv, ok := conversionFor(p.GoType)
		if !ok {
			fmt.Fprintf(&b, "\t// TODO: convert %s (%s ref.Val) to Go %s; no known conversion for this type.\n", p.Name, argNames[i], p.GoType)
			callArgs[i] = fmt.Sprintf("/* TODO %s */", p.Name)
			continue
		}
		localVar := "go" + strings.ToUpper(p.Name[:1]) + p.Name[1:]
		fmt.Fprintf(&b, "\t%s, ok := %s(%s)\n", localVar, conv, argNames[i])
		fmt.Fprintf(&b, "\tif !ok {\n\t\treturn types.NewErr(\"filters.%s: argument %s is not convertible to %s\")\n\t}\n",
			celName, p.Name, p.GoType)
		callArgs[i] = localVar
	}

	wrap, ok := wrapperFor(ret.GoType)
	if !ok {
		fmt.Fprintf(&b, "\t// TODO: wrap the %s result back into a ref.Val; no known wrapper for this type.\n", ret.GoType)
		fmt.Fprintf(&b, "\treturn types.NewErr(\"filters.%s: TODO wrap %s result\")\n", celName, ret.GoType)
	} else {
		fmt.Fprintf(&b, "\treturn %s(filters.%s(%s))\n", wrap, goName, strings.Join(callArgs, ", "))
	}
	b.WriteString("}\n")
	return b.String()
}

// conversionFor returns the shared ref.Val -> Go conversion helper name
// for a well-known Go type, all defined once in
// internal/engine/cel_filters.go: celToString/celToInt/celToBool
// (Phase 50/51), celToStringList (Phase 51's Supernet), and
// celToMap/celToDynList/celToMapList (added for Phase 52's structured-
// data filters).
func conversionFor(goType string) (string, bool) {
	switch goType {
	case "string":
		return "celToString", true
	case "int":
		return "celToInt", true
	case "bool":
		return "celToBool", true
	case "[]string":
		return "celToStringList", true
	case "map[string]any":
		return "celToMap", true
	case "[]any":
		return "celToDynList", true
	case "[]map[string]any":
		return "celToMapList", true
	default:
		return "", false
	}
}

// wrapperFor returns the Go -> ref.Val wrapper for a well-known return
// type.
func wrapperFor(goType string) (string, bool) {
	switch goType {
	case "string":
		return "types.String", true
	case "int":
		return "types.Int", true
	case "bool":
		return "types.Bool", true
	case "[]string":
		return "wrapStringList", true
	case "map[string]any":
		return "wrapMap", true
	case "[]any":
		return "wrapDynList", true
	case "[]map[string]any":
		return "wrapMapList", true
	default:
		return "", false
	}
}

// renderAndFormat executes tmpl and formats the result via go/format
// rather than shelling out to a gofmt binary, so generated output is
// deterministically gofmt-clean with no subprocess involved, matching
// every other scaffolder in this package family.
func renderAndFormat(tmpl *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting generated source: %w\n---\n%s", err, buf.String())
	}
	return formatted, nil
}
