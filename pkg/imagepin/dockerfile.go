// Image references in a Dockerfile or Containerfile.
package imagepin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// token is one whitespace-separated word of an instruction and its byte
// range in the file.
type token struct {
	text       string
	start, end int
}

// argDefault is an ARG's default value and where it is written.
type argDefault struct {
	value      string
	start, end int
	set        bool
}

// variableOnly matches a word that is exactly one variable: $NAME or
// ${NAME}.
var variableOnly = regexp.MustCompile(`^\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))$`)

// heredocWord is BuildKit's own test for a here-document opener: a whole
// word that starts with "<<" (after an optional file descriptor), an
// optional "-" that strips leading tabs, and the terminator, which may be
// quoted. Only RUN, COPY and ADD take one (heredocInstructions). A "<<"
// anywhere else in a word (an arithmetic shift, a quoted string) opens
// nothing, and reading one as an opener would hide every line after it.
var heredocWord = regexp.MustCompile(`^\d*<<(-?)([^<]+)$`)

// heredocInstructions are the instructions BuildKit reads heredocs in.
var heredocInstructions = map[string]bool{"RUN": true, "COPY": true, "ADD": true}

// dockerfileParser holds what a walk of one Dockerfile has seen.
//
// ARGs have two scopes, as in a build: those before the first FROM are
// global, and only a FROM reads them; an ARG inside a stage belongs to
// that stage, which is what COPY --from and RUN --mount read, and a bare
// ARG there imports the global value into the stage without changing it.
type dockerfileParser struct {
	content []byte
	lines   lineIndex
	stages  map[string]bool
	global  map[string]argDefault
	stage   map[string]argDefault
	inStage bool
	refs    []Ref
}

// extractDockerfile walks content's instructions: continuations joined,
// comments and here-document bodies skipped.
func extractDockerfile(content []byte) ([]Ref, error) {
	p := &dockerfileParser{content: content, lines: newLineIndex(content), stages: map[string]bool{}, global: map[string]argDefault{}, stage: map[string]argDefault{}}
	escape := byte('\\')
	lines := splitLines(content)
	directives := true
	var instruction []token
	var heredocs []string
	stripTabs := false

	for _, ln := range lines {
		text := string(content[ln.start:ln.end])
		trimmed := strings.TrimSpace(text)
		if len(heredocs) > 0 {
			candidate := text
			if stripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if strings.TrimRight(candidate, " \r") == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		if directives {
			if value, ok := directive(trimmed, "escape"); ok && len(value) == 1 {
				escape = value[0]
				continue
			}
			if _, ok := directive(trimmed, "syntax"); ok {
				continue
			}
			if _, ok := directive(trimmed, "check"); ok {
				continue
			}
			directives = false
		}
		if strings.HasPrefix(trimmed, "#") || (trimmed == "" && len(instruction) == 0) {
			continue
		}
		continues := strings.HasSuffix(strings.TrimRight(text, " \t\r"), string(escape))
		body := text
		if continues {
			body = strings.TrimRight(text, " \t\r")
			body = body[:len(body)-1]
		}
		instruction = append(instruction, words(body, ln.start)...)
		if continues {
			continue
		}
		if len(instruction) > 0 {
			if heredocInstructions[strings.ToUpper(instruction[0].text)] {
				for _, t := range instruction[1:] {
					if m := heredocWord.FindStringSubmatch(t.text); m != nil {
						heredocs = append(heredocs, unquote(m[2]))
						stripTabs = m[1] == "-"
					}
				}
			}
			if err := p.instruction(instruction); err != nil {
				return nil, err
			}
		}
		instruction = nil
	}
	if len(instruction) > 0 {
		if err := p.instruction(instruction); err != nil {
			return nil, err
		}
	}
	return p.refs, nil
}

// unquote strips one pair of matching quotes from a heredoc terminator.
func unquote(word string) string {
	if len(word) >= 2 && (word[0] == '"' || word[0] == '\'') && word[len(word)-1] == word[0] {
		return word[1 : len(word)-1]
	}
	return word
}

// directive reads a parser directive line (# name=value), which only
// counts at the top of the file.
func directive(line, name string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "#")
	if !ok {
		return "", false
	}
	key, value, ok := strings.Cut(strings.TrimSpace(rest), "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), name) {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// instruction handles one complete instruction.
func (p *dockerfileParser) instruction(tokens []token) error {
	switch strings.ToUpper(tokens[0].text) {
	case "ARG":
		for _, t := range tokens[1:] {
			p.arg(t)
		}
	case "FROM":
		return p.from(tokens[0], tokens[1:])
	case "COPY", "ADD":
		for _, t := range tokens[1:] {
			if !strings.HasPrefix(t.text, "--") {
				break
			}
			if value, ok := strings.CutPrefix(t.text, "--from="); ok {
				p.image(token{text: value, start: t.end - len(value), end: t.end}, "COPY --from", p.stage)
			}
		}
	case "RUN":
		for _, t := range tokens[1:] {
			if !strings.HasPrefix(t.text, "--") {
				break
			}
			if spec, ok := strings.CutPrefix(t.text, "--mount="); ok {
				offset := t.end - len(spec)
				for _, field := range strings.Split(spec, ",") {
					if value, ok := strings.CutPrefix(field, "from="); ok {
						start := offset + len("from=")
						p.image(token{text: value, start: start, end: start + len(value)}, "RUN --mount", p.stage)
					}
					offset += len(field) + 1
				}
			}
		}
	}
	return nil
}

// arg records an ARG NAME or ARG NAME=default in its scope: global before
// the first FROM, the current stage's after it, where a bare ARG NAME
// imports the global value.
func (p *dockerfileParser) arg(t token) {
	name, value, hasDefault := strings.Cut(t.text, "=")
	d := argDefault{}
	if hasDefault {
		start := t.start + len(name) + 1
		end := t.end
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value, start, end = value[1:len(value)-1], start+1, end-1
		}
		d = argDefault{value: value, start: start, end: end, set: value != ""}
	}
	switch {
	case !p.inStage:
		p.global[name] = d
	case !hasDefault:
		p.stage[name] = p.global[name]
	default:
		p.stage[name] = d
	}
}

// from handles FROM [--flags] image [AS name]; keyword is the FROM
// itself, which names the line when nothing follows it.
func (p *dockerfileParser) from(keyword token, tokens []token) error {
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i].text, "--") {
		i++
	}
	if i >= len(tokens) {
		return fmt.Errorf("line %d: FROM names no image", p.lines.line(keyword.start))
	}
	p.image(tokens[i], "FROM", p.global)
	p.inStage, p.stage = true, map[string]argDefault{}
	if i+2 < len(tokens) && strings.EqualFold(tokens[i+1].text, "AS") {
		p.stages[strings.ToLower(tokens[i+2].text)] = true
	}
	return nil
}

// image records the reference t names, unless it names a build stage, a
// stage by number, or scratch; a reference made of one variable is read
// from that ARG's default in args, the scope the instruction reads.
func (p *dockerfileParser) image(t token, where string, args map[string]argDefault) {
	lower := strings.ToLower(t.text)
	if p.stages[lower] || lower == "scratch" {
		return
	}
	if _, err := strconv.Atoi(t.text); err == nil {
		return
	}
	if m := variableOnly.FindStringSubmatch(t.text); m != nil {
		name := m[1] + m[2]
		d, declared := args[name]
		if !declared || !d.set {
			p.refs = append(p.refs, Ref{Line: p.lines.line(t.start), Start: t.start, End: t.end, Text: t.text, Where: where,
				Unresolvable: fmt.Sprintf("it is the build argument %s, which has no default in this file", name)})
			return
		}
		if p.stages[strings.ToLower(d.value)] || strings.EqualFold(d.value, "scratch") {
			return
		}
		p.refs = append(p.refs, Ref{Line: p.lines.line(d.start), Start: d.start, End: d.end, Text: d.value, Where: "ARG " + name})
		return
	}
	r := Ref{Line: p.lines.line(t.start), Start: t.start, End: t.end, Text: t.text, Where: where}
	if strings.Contains(t.text, "$") {
		r.Unresolvable = "part of it is a build argument, so the reference is only known when the image is built"
	}
	p.refs = append(p.refs, r)
}

// span is one physical line's byte range, its newline excluded.
type span struct{ start, end int }

// splitLines returns each line's byte range.
func splitLines(content []byte) []span {
	var lines []span
	start := 0
	for i, b := range content {
		if b == '\n' {
			lines = append(lines, span{start, i})
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, span{start, len(content)})
	}
	return lines
}

// words splits text, which begins at byte offset base, on spaces and tabs,
// keeping each word's byte range.
func words(text string, base int) []token {
	var out []token
	start := -1
	for i := 0; i <= len(text); i++ {
		blank := i == len(text) || text[i] == ' ' || text[i] == '\t' || text[i] == '\r'
		switch {
		case blank && start >= 0:
			out = append(out, token{text: text[start:i], start: base + start, end: base + i})
			start = -1
		case !blank && start < 0:
			start = i
		}
	}
	return out
}
