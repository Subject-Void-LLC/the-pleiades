// Package playbook: rewriting the statements of a template that reads
// variables at run time, so a playbook's {% if %}, {% for %} and {% set %}
// reach the runbook reading its roots, and render there as Ansible renders
// them.
package playbook

import (
	"fmt"
	"slices"
	"strings"
)

// rewriteTemplate rewrites every {{ }} expression in s, and the expression
// in each {% if %}, {% elif %}, {% for %} and {% set %}, so it reads the
// runbook's roots. names are the loop names a task's own loop gives, kept as
// written, as are the names the template's own for and set bind. A
// comment and a raw block are kept as written. A non-empty quote is
// appended to each {{ }} expression that does not already end with a
// command-safe filter.
func (t *translator) rewriteTemplate(s string, names []string, quote string, at Position) (string, *resolveError) {
	var out strings.Builder
	sc := &statementScope{names: slices.Clone(names)}
	rest := s
	for {
		open := nextJinja(rest)
		if open < 0 {
			if len(sc.loops) > 0 {
				return "", &resolveError{"template.unsupported", "a for loop is never closed"}
			}
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:open])
		body := rest[open+2:]
		switch rest[open+1] {
		case '#':
			end := strings.Index(body, "#}")
			if end < 0 {
				return "", &resolveError{"template.unsupported", "a {# is never closed"}
			}
			out.WriteString(rest[open : open+2+end+2])
			rest = body[end+2:]
			continue
		case '%':
			end := statementEnd(body)
			if end < 0 {
				return "", &resolveError{"template.unsupported", "a {% is never closed"}
			}
			if _, inner, _ := splitMarkers(body[:end]); strings.TrimSpace(inner) == "raw" {
				stop := rawEnd(body[end+2:])
				if stop < 0 {
					return "", &resolveError{"template.unsupported", "a {% raw %} is never closed"}
				}
				out.WriteString(rest[open : open+2+end+2+stop])
				rest = body[end+2+stop:]
				continue
			}
			tag, err := t.rewriteStatement(body[:end], sc, at)
			if err != nil {
				return "", err
			}
			out.WriteString(tag)
			rest = body[end+2:]
			continue
		}
		end := exprEnd(body)
		if end < 0 {
			return "", &resolveError{"template.unsupported", "a {{ is never closed"}
		}
		inner := body[:end]
		trimmed := strings.TrimSpace(inner)
		if strings.HasPrefix(trimmed, "-") || strings.HasSuffix(trimmed, "-") {
			return "", &resolveError{"template.unsupported", "whitespace control in {{ }} has no equivalent here"}
		}
		if text, ok, berr := t.boundText(trimmed); berr != nil {
			return "", berr
		} else if ok {
			out.WriteString(text)
			rest = body[end+2:]
			continue
		}
		expr, err := t.rewriteExpr(trimmed, sc.names, at)
		if err != nil {
			return "", err
		}
		if quote != "" && !commandSafe(expr) {
			expr += " | " + quote
			t.note("template.quoted", at, "a {{ }} expression in command text was given | "+quote)
		}
		out.WriteString("{{ " + expr + " }}")
		rest = body[end+2:]
	}
}

// nextJinja returns where the first {{, {% or {# in s opens, or -1.
func nextJinja(s string) int {
	for from := 0; ; {
		i := strings.IndexByte(s[from:], '{')
		if i < 0 || from+i+1 >= len(s) {
			return -1
		}
		if c := s[from+i+1]; c == '{' || c == '%' || c == '#' {
			return from + i
		}
		from += i + 1
	}
}

// statementScope is the names a template's own statements bind as the
// rewrite walks it: set names and for loop names, each read as written
// rather than as vars.<name>, and dropped again at the endfor of the loop
// that bound them, as Jinja scopes them.
type statementScope struct {
	names []string
	// loops holds len(names) as each open for loop began.
	loops []int
}

// rewriteStatement rewrites one {% %} tag's body, its delimiters removed:
// the expression in an if, elif, for or set, with the names a loop or set
// binds added to scope. Every other statement Ansible has is one the
// renderer refuses, so it leaves the task for a person.
func (t *translator) rewriteStatement(body string, sc *statementScope, at Position) (string, *resolveError) {
	open, inner, close := splitMarkers(body)
	word, rest, _ := strings.Cut(strings.TrimSpace(inner), " ")
	rest = strings.TrimSpace(rest)
	wrap := func(text string) string { return "{%" + open + " " + text + " " + close + "%}" }

	switch word {
	case "if", "elif":
		expr, err := t.rewriteExpr(rest, sc.names, at)
		if err != nil {
			return "", err
		}
		return wrap(word + " " + expr), nil

	case "else", "endif":
		return wrap(word), nil

	case "for":
		targets, iter, ok := strings.Cut(rest, " in ")
		if !ok {
			return "", &resolveError{"template.unsupported", "a for loop without in"}
		}
		expr, err := t.rewriteExpr(strings.TrimSpace(iter), sc.names, at)
		if err != nil {
			return "", err
		}
		sc.loops = append(sc.loops, len(sc.names))
		for _, name := range strings.Split(targets, ",") {
			sc.names = append(sc.names, strings.TrimSpace(name))
		}
		sc.names = append(sc.names, "loop")
		return wrap("for " + strings.TrimSpace(targets) + " in " + expr), nil

	case "endfor":
		if len(sc.loops) == 0 {
			return "", &resolveError{"template.unsupported", "an endfor with no for"}
		}
		sc.names = sc.names[:sc.loops[len(sc.loops)-1]]
		sc.loops = sc.loops[:len(sc.loops)-1]
		return wrap(word), nil

	case "set":
		name, value, ok := strings.Cut(rest, "=")
		if !ok || strings.HasPrefix(value, "=") {
			return "", &resolveError{"template.unsupported", "a set block, or a set with no value"}
		}
		expr, err := t.rewriteExpr(strings.TrimSpace(value), sc.names, at)
		if err != nil {
			return "", err
		}
		name = strings.TrimSpace(name)
		if !slices.Contains(sc.names, name) {
			sc.names = append(sc.names, name)
		}
		return wrap("set " + name + " = " + expr), nil
	}
	return "", &resolveError{"template.unsupported", fmt.Sprintf("the template uses {%% %s %%}, which the platform's renderer refuses", word)}
}

// splitMarkers takes a tag body's whitespace markers ("-" or "+") off
// either end, so a rewrite keeps them where Jinja reads them.
func splitMarkers(body string) (open, inner, close string) {
	inner = body
	if inner != "" && (inner[0] == '-' || inner[0] == '+') {
		open, inner = inner[:1], inner[1:]
	}
	if n := len(inner); n > 0 && (inner[n-1] == '-' || inner[n-1] == '+') {
		close, inner = inner[n-1:], inner[:n-1]
	}
	return open, inner, close
}

// statementEnd returns the index of the %} closing a statement that begins
// s, skipping quoted text, or -1.
func statementEnd(s string) int {
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
		case c == '%' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// rawEnd returns the index just past the {% endraw %} that closes a raw
// block whose body begins s, or -1.
func rawEnd(s string) int {
	for from := 0; ; {
		i := strings.Index(s[from:], "{%")
		if i < 0 {
			return -1
		}
		at := from + i
		end := statementEnd(s[at+2:])
		if end < 0 {
			return -1
		}
		if _, inner, _ := splitMarkers(s[at+2 : at+2+end]); strings.TrimSpace(inner) == "endraw" {
			return at + 2 + end + 2
		}
		from = at + 2
	}
}
