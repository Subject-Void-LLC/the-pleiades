// Package playbook: writing a template the way the runbook reads it.
//
// Most templates in a playbook are folded into the literal values their
// variables have (templates.go). A template that cannot be folded, because
// it reads a loop's item, a variable set at more than one scope, a set_fact
// or a registered result, stays a template, with each variable it reads
// rewritten to the root the runbook reads it under: vars.<name>, result.<name>
// or the loop's own names. The rewrite works on the expression's tokens, never
// on its meaning, and the result is compiled with the platform's renderer and
// held to the same rules validation holds a parameter to before it is kept.
package playbook

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// jinjaWords are the words of an expression that are never a variable.
var jinjaWords = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "is": true, "if": true, "else": true,
	"true": true, "false": true, "none": true, "True": true, "False": true, "None": true,
	"defined": true, "undefined": true,
}

// fieldAfter matches the .field a registered name is read through.
var fieldAfter = regexp.MustCompile(`^\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)`)

// lastFilter matches the final filter of an expression.
var lastFilter = regexp.MustCompile(`\|\s*([A-Za-z_][A-Za-z0-9_]*)(\s*\((?:[^()]|\([^()]*\))*\))?\s*$`)

// exprEnd returns the index of the }} closing an expression that begins s,
// skipping quoted text, or -1.
func exprEnd(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '}' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// commandSafe reports whether expr already ends with a filter the native
// command-text rule accepts.
func commandSafe(expr string) bool {
	m := lastFilter.FindStringSubmatch(expr)
	return m != nil && (m[1] == "quote" || m[1] == "cli_token")
}

// isIdentStart and isIdentPart classify bytes of a Jinja name.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// rewriteExpr rewrites the names one expression reads.
func (t *translator) rewriteExpr(expr string, names []string, at Position) (string, *resolveError) {
	var out strings.Builder
	var prevChar byte
	var prev, prevPrev string
	emit := func(tok string) {
		out.WriteString(tok)
		if tok = strings.TrimSpace(tok); tok != "" {
			prevChar = tok[len(tok)-1]
			prevPrev, prev = prev, tok
		}
	}
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(expr) && expr[j] != c {
				if expr[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(expr) {
				return "", &resolveError{"template.unsupported", "a quote is never closed"}
			}
			emit(expr[i : j+1])
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(expr) && (isIdentPart(expr[j]) || expr[j] == '.') {
				j++
			}
			emit(expr[i:j])
			i = j
		case isIdentStart(c):
			j := i
			for j < len(expr) && isIdentPart(expr[j]) {
				j++
			}
			word := expr[i:j]
			after := strings.TrimLeft(expr[j:], " \t")
			switch {
			case prevChar == '.', prev == "|", prev == "is", prev == "not" && prevPrev == "is", jinjaWords[word]:
				emit(word)
			case strings.HasPrefix(after, "(") && !slices.Contains(names, word):
				return "", &resolveError{"template.unsupported", fmt.Sprintf("the template calls %s, which a runbook's template does not have", word)}
			case strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "=="):
				emit(word)
			default:
				rewritten, consumed, err := t.rewriteRoot(word, expr[j:], names, at)
				if err != nil {
					return "", err
				}
				emit(rewritten)
				j += consumed
			}
			i = j
		default:
			emit(string(c))
			i++
		}
	}
	return out.String(), nil
}

// rewriteRoot rewrites one variable read: word is its name, rest the text
// after it, and consumed is how much of rest the rewrite used.
func (t *translator) rewriteRoot(word, rest string, names []string, at Position) (string, int, *resolveError) {
	if _, ok := t.bound[word]; ok {
		return "", 0, &resolveError{"template.unsupported", fmt.Sprintf("an unrolled item (%s) inside an expression with more than its path", word)}
	}
	if slices.Contains(names, word) {
		return word, 0, nil
	}
	if p, ok := t.registers[word]; ok {
		m := fieldAfter.FindStringSubmatch(rest)
		if m == nil {
			return "", 0, &resolveError{"template.unsupported", fmt.Sprintf("%s is a registered result read as a whole; only one field of it translates", word)}
		}
		native, ok := p.returns[m[1]]
		if !ok {
			return "", 0, &resolveError{"template.unsupported", fmt.Sprintf("%s.%s: %s returns no equivalent of Ansible's %s", word, m[1], p.fqcn, m[1])}
		}
		t.note("template.register", at, "a template reads the registered result "+word)
		return "result." + word + celKey(native), len(m[0]), nil
	}
	if isMagic(word) {
		return "", 0, &resolveError{"template.fact", fmt.Sprintf("%s is a fact or a magic variable, known only on a host at run time", word)}
	}
	if !t.ix.native(word) {
		_, rerr := t.ix.literal(word)
		if rerr == nil {
			rerr = &resolveError{"template.unresolved", fmt.Sprintf("%s is defined outside the playbook's own vars, so it is not carried into the runbook", word)}
		}
		return "", 0, rerr
	}
	if rerr := t.varSafe(word); rerr != nil {
		return "", 0, rerr
	}
	if err := engine.CheckVarNames([]string{word}); err != nil {
		return "", 0, &resolveError{"vars.unreadable", fmt.Sprintf("%s cannot be read as vars.%s", word, word)}
	}
	if t.usedVars == nil {
		t.usedVars = map[string]bool{}
	}
	t.usedVars[word] = true
	return "vars." + word, 0, nil
}

// pathStep matches one .field or ['key'] step after a name.
var pathStep = regexp.MustCompile(`^\s*(?:\.\s*([A-Za-z_][A-Za-z0-9_]*)|\[\s*(?:'([^'\\]*)'|"([^"\\]*)"|([0-9]+))\s*\])`)

// boundText writes the value an unrolled loop's item has, when expr reads
// nothing but the item and the .field steps after it, as the text that
// expression would print: the copy of the task for that item has no loop
// left to read it from, so the value goes into the text as the unrolled
// conversion has always put it. ok is false when expr is anything else.
func (t *translator) boundText(expr string) (text string, ok bool, err *resolveError) {
	m := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)`).FindStringSubmatch(expr)
	if m == nil {
		return "", false, nil
	}
	v, bound := t.bound[m[1]]
	if !bound {
		return "", false, nil
	}
	rest := expr[len(m[1]):]
	for strings.TrimSpace(rest) != "" {
		step := pathStep.FindStringSubmatch(rest)
		if step == nil {
			return "", false, &resolveError{"template.unsupported", fmt.Sprintf("%s is read through more than a plain path, which an unrolled item cannot be", m[1])}
		}
		var key any
		switch {
		case step[1] != "":
			key = step[1]
		case step[2] != "":
			key = step[2]
		case step[3] != "":
			key = step[3]
		default:
			n, perr := strconv.ParseInt(step[4], 10, 64)
			if perr != nil {
				return "", false, &resolveError{"template.unsupported", "an index that is not a number"}
			}
			key = n
		}
		next, found := index(v, key)
		if !found {
			return "", false, &resolveError{"template.unsupported", fmt.Sprintf("%s has no %v", m[1], key)}
		}
		v, rest = next, rest[len(step[0]):]
	}
	switch x := v.(type) {
	case string:
		if termsafeUnsafe(x) {
			return "", false, &resolveError{"template.unsupported", fmt.Sprintf("the value of %s holds a control character", m[1])}
		}
		return x, true, nil
	case int64:
		return strconv.FormatInt(x, 10), true, nil
	}
	return "", false, &resolveError{"template.unsupported", fmt.Sprintf("%s prints as a map, a list or a boolean, which Jinja and this renderer print differently", m[1])}
}

// termsafeUnsafe reports whether s holds a control character.
func termsafeUnsafe(s string) bool {
	return termsafe.Check(s) != nil
}

// varSafe refuses a variable that must never be copied into a runbook:
// one named like a secret, holding one, or vault-encrypted.
func (t *translator) varSafe(name string) *resolveError {
	if redact.SecretName(name) {
		return &resolveError{"template.secret", fmt.Sprintf("%s is named like a secret", name)}
	}
	for _, d := range t.ix.defs[name] {
		if d.value == nil {
			continue
		}
		if isVault(d.value) {
			return &resolveError{"vault.value", fmt.Sprintf("%s is vault-encrypted", name)}
		}
		if v, err := naturalValue(d.value); err == nil && secretValue(v) {
			return &resolveError{"template.secret", fmt.Sprintf("%s holds a value shaped like a secret", name)}
		}
	}
	return nil
}

// note records a finding raised on the way, for the task being converted to
// cite.
func (t *translator) note(code Code, at Position, why string) {
	t.noted = append(t.noted, t.raise(code, at, "", why))
}

// takeNoted returns the findings noted since the last call.
func (t *translator) takeNoted() []string {
	out := t.noted
	t.noted = nil
	return out
}

// nativeValue rewrites every template inside v, a value read from YAML.
func (t *translator) nativeValue(v any, names []string, quote string, at Position) (any, *resolveError) {
	switch x := v.(type) {
	case string:
		if !hasTemplate(x) {
			return x, nil
		}
		return t.rewriteTemplate(x, names, quote, at)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			r, err := t.nativeValue(item, names, quote, at)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			r, err := t.nativeValue(item, names, quote, at)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	}
	return v, nil
}

// nativeNode is nativeValue for a YAML node.
func (t *translator) nativeNode(n *yaml.Node, names []string, quote string, at Position) (any, *resolveError) {
	v, err := naturalValue(n)
	if err != nil {
		return nil, &resolveError{"vault.value", err.Error()}
	}
	return t.nativeValue(v, names, quote, at)
}

// mentions reports whether any template under n reads one of names.
func mentions(n *yaml.Node, names []string) bool {
	n = deref(n)
	if n == nil || len(names) == 0 {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		if !hasTemplate(n.Value) {
			return false
		}
		tpl, err := render.New().Compile(n.Value)
		if err != nil {
			return false
		}
		for _, name := range tpl.Names() {
			if slices.Contains(names, name) {
				return true
			}
		}
		return false
	}
	for _, c := range n.Content {
		if mentions(c, names) {
			return true
		}
	}
	return false
}

// paramFormat returns the declared format of fqcn's parameter.
func paramFormat(fqcn, param string) collection.ParamFormat {
	d, ok := collection.Lookup(fqcn)
	if !ok {
		return ""
	}
	for _, p := range d.Manifest.Doc.Params {
		if p.Name == param {
			return p.Format
		}
	}
	return ""
}

// quoteFilter is the filter command text in fqcn's parameter needs: quote
// for a shell, cli_token for a network CLI, none for other text.
func quoteFilter(fqcn string, format collection.ParamFormat) string {
	if format != collection.ParamFormatCommand {
		return ""
	}
	if strings.HasPrefix(fqcn, "net.") {
		return "cli_token"
	}
	return "quote"
}

// nativeParam writes a parameter's templated value the runbook's way and
// holds it to validation's rules for the parameter's format and type.
func (t *translator) nativeParam(node *yaml.Node, fqcn, param, typ string, bindings map[string]any, at Position) (any, *resolveError) {
	t.bound = bindings
	defer func() { t.bound = nil }()
	format := paramFormat(fqcn, param)
	v, rerr := t.nativeNode(node, t.loopNames, quoteFilter(fqcn, format), at)
	if rerr != nil {
		return nil, rerr
	}
	if rerr := t.checkRewritten(v, format, typ); rerr != nil {
		return nil, rerr
	}
	return v, nil
}

// checkRewritten compiles every rewritten template under v and refuses what
// validation would refuse: a root a parameter may not read, unquoted command
// text, a type the template cannot keep.
func (t *translator) checkRewritten(v any, format collection.ParamFormat, typ string) *resolveError {
	task := &engine.Task{}
	if len(t.loopNames) > 0 {
		task.Loop = []any{0}
		task.LoopControl = &engine.LoopControl{LoopVar: t.loopVarName, IndexVar: t.loopIndexName}
	}
	var walk func(x any) *resolveError
	walk = func(x any) *resolveError {
		switch s := x.(type) {
		case string:
			if !hasTemplate(s) {
				return nil
			}
			tpl, err := render.New().Compile(s)
			if err != nil {
				return &resolveError{"template.unsupported", "the rewritten template does not compile"}
			}
			if probs := engine.TaskTemplateProblems(tpl, format, task); len(probs) > 0 {
				return &resolveError{"template.unsupported", probs[0]}
			}
			if typ != "" && typ != "string" && !wholeExpr(s) {
				return &resolveError{"args.value", "a template inside text where the parameter takes a " + typ}
			}
		case []any:
			for _, item := range s {
				if err := walk(item); err != nil {
					return err
				}
			}
		case map[string]any:
			for _, item := range s {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(v)
}

// wholeExpr reports whether s is one {{ }} expression and nothing else.
func wholeExpr(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") && exprEnd(s[2:]) == len(s)-4
}
