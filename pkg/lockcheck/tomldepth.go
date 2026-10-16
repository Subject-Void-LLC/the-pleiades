// How deeply a TOML document nests, measured before go-toml reads it.
package lockcheck

import "fmt"

// maxTOMLNesting bounds how many arrays and inline tables a Cargo file may
// open inside one another. A real one opens a few.
const maxTOMLNesting = 100

// checkTOMLNesting refuses a TOML document that nests arrays or inline
// tables more than maxTOMLNesting deep.
//
// go-toml parses a nested value by recursing, with no limit of its own, so
// a megabyte of "[" exhausted the goroutine's stack, which Go cannot
// recover from: the whole process died. This counts the nesting the way
// TOML's own grammar reads it, skipping comments and the four kinds of
// string, so a bracket inside a string counts for nothing and one outside
// any string cannot be hidden from the count by a string's quotes. A
// document invalid in a way this reads differently is one go-toml refuses
// at that point, before it could nest any deeper.
func checkTOMLNesting(name string, data []byte) error {
	depth, deepest := 0, 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '#':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case '"', '\'':
			i = skipTOMLString(data, i)
		case '[', '{':
			depth++
			if depth > deepest {
				deepest = depth
			}
			if deepest > maxTOMLNesting {
				return fmt.Errorf("%s nests arrays or tables more than %d deep", name, maxTOMLNesting)
			}
		case ']', '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}

// skipTOMLString returns the index of the last byte of the string that
// opens at data[start]: a basic or literal string (one double or single
// quote), or a multi-line one of either kind (three of them). Basic
// strings honor backslash escapes; literal ones have none. A single-line
// string ends at a newline too, which TOML refuses inside one.
func skipTOMLString(data []byte, start int) int {
	quote := data[start]
	multi := start+2 < len(data) && data[start+1] == quote && data[start+2] == quote
	i := start + 1
	if multi {
		i = start + 3
	}
	for ; i < len(data); i++ {
		switch {
		case data[i] == '\\' && quote == '"':
			i++
		case data[i] == '\n' && !multi:
			return i
		case data[i] == quote && !multi:
			return i
		case data[i] == quote && i+2 < len(data) && data[i+1] == quote && data[i+2] == quote:
			// A closing delimiter may follow one or two quotes of the
			// content (""""" is a quote, a quote, then the end).
			end := i + 2
			for n := 0; n < 2 && end+1 < len(data) && data[end+1] == quote; n++ {
				end++
			}
			return end
		}
	}
	return len(data)
}
