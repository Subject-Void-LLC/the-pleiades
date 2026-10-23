// Package iosconfig reads a Cisco IOS configuration as the tree its
// indentation draws, reads the contextual differences IOS itself reports
// between two configurations (show archive config differences), and writes
// the configuration lines that undo a change for a device that cannot keep
// a checkpoint to return to. It reads text only; a Collection method sends
// the commands and masks what it reports.
package iosconfig

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Node is one configuration line and the lines indented under it.
type Node struct {
	// Line is the line without its indentation.
	Line string
	// Children are the lines indented under it, in order.
	Children []*Node
}

// ignored are lines a configuration shows that no change makes: NTP's
// clock period, which IOS rewrites as it disciplines the clock, so two
// reads of an unchanged device can differ by it.
var ignored = []string{"ntp clock-period"}

// Parse reads config, the text show running-config prints, into its
// top-level nodes. Comment lines (starting with !), the header IOS prints
// before the configuration and the closing end are left out. A banner is
// kept as one node whose line holds its whole text, so a change inside one
// is still a difference, and Inverse refuses it.
func Parse(config string) []*Node {
	var roots []*Node
	type open struct {
		indent int
		node   *Node
	}
	var stack []open
	banner := ""
	var bannerNode *Node
	for raw := range strings.SplitSeq(config, "\n") {
		raw = strings.TrimRight(raw, "\r \t")
		if banner != "" {
			// A banner's text runs until its delimiter comes round again,
			// and is kept in its node's line so a change to it differs.
			bannerNode.Line += "\n" + raw
			if strings.Contains(raw, banner) {
				banner = ""
			}
			continue
		}
		line := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(line)
		if line == "" || strings.HasPrefix(line, "!") || skipHeader(line, indent) || isIgnored(line) {
			continue
		}
		node := &Node{Line: line}
		if indent == 0 && strings.HasPrefix(line, "banner ") {
			banner, bannerNode = bannerDelimiter(line), node
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1].node
			parent.Children = append(parent.Children, node)
		}
		stack = append(stack, open{indent, node})
	}
	return roots
}

// skipHeader reports whether a margin line is part of what IOS prints
// around a configuration rather than the configuration itself.
func skipHeader(line string, indent int) bool {
	if indent != 0 {
		return false
	}
	return line == "end" || strings.HasPrefix(line, "Building configuration") || strings.HasPrefix(line, "Current configuration")
}

// isIgnored reports whether line is one no change makes.
func isIgnored(line string) bool {
	return slices.ContainsFunc(ignored, func(prefix string) bool { return strings.HasPrefix(line, prefix) })
}

// bannerDelimiter returns the delimiter a banner's first line opens with
// ("banner motd ^C" prints ^C for Ctrl-C), or "" when the banner is
// closed on the same line.
func bannerDelimiter(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ""
	}
	open := fields[2]
	delim := open
	if strings.HasPrefix(open, "^") && len(open) >= 2 {
		delim = open[:2]
	} else if len(open) > 0 {
		delim = open[:1]
	}
	rest := strings.TrimPrefix(strings.Join(fields[2:], " "), delim)
	if strings.Contains(rest, delim) {
		return ""
	}
	return delim
}

// ErrUnsupported is what Inverse's error wraps when the difference touches
// configuration whose undo it cannot write: an access list's entries,
// whose order a re-added entry would not keep, a banner, which is entered
// as several lines of free text, or a certificate.
var ErrUnsupported = errors.New("the difference touches configuration whose undo cannot be written line by line")

// unsupportedInside are the blocks Inverse refuses to write an undo inside:
// an access list's entries keep their order only as entered, so one put
// back would land at the end. A whole access list added or removed is
// undone like any block.
var unsupportedInside = []string{"ip access-list ", "ipv6 access-list ", "mac access-list "}

// unsupportedLines are the lines Inverse refuses to undo at all, or
// anything under: a banner is entered as several lines of free text, and
// removing a certificate chain asks for a confirmation.
var unsupportedLines = []string{"banner ", "crypto pki certificate "}

// Inverse returns the configuration lines that take a device from after
// back to before, both as Parse read them: every line after added is
// negated (a block it added is removed whole), every line it removed is
// put back with the block under it, and a block both hold is entered and
// compared the same way. Negations come before restorations in each block,
// so a setting a change replaced (description A to description B) is
// negated and then set again. An added "no X" is undone by X, unless a
// restored line already sets X. Each line is preceded by the lines of the
// blocks it sits in, indented as IOS indents them. Nil means the two do
// not differ.
func Inverse(before, after []*Node) ([]string, error) {
	var out []string
	if err := undo(before, after, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// undo writes the lines that undo after's difference from before, both
// the children of the block path names, onto out.
func undo(before, after []*Node, path []string, out *[]string) error {
	had := byLine(before)
	has := byLine(after)
	var removed []*Node
	for _, n := range before {
		if _, ok := has[n.Line]; !ok {
			removed = append(removed, n)
		}
	}
	var negate []string
	var common [][2]*Node
	for _, n := range after {
		old, ok := had[n.Line]
		if ok {
			common = append(common, [2]*Node{old, n})
			continue
		}
		if err := refuseInside(path, n.Line); err != nil {
			return err
		}
		if x, ok := strings.CutPrefix(n.Line, "no "); ok {
			if !slices.ContainsFunc(removed, func(r *Node) bool { return r.Line == x || strings.HasPrefix(r.Line, x+" ") }) {
				negate = append(negate, x)
			}
			continue
		}
		negate = append(negate, "no "+n.Line)
	}
	for _, r := range removed {
		if err := refuseInside(path, r.Line); err != nil {
			return err
		}
	}
	body := negate
	for _, r := range removed {
		body = append(body, subtree(r, 0)...)
	}
	if len(body) > 0 {
		for i, p := range path {
			*out = append(*out, strings.Repeat(" ", i)+p)
		}
		for _, l := range body {
			*out = append(*out, strings.Repeat(" ", len(path))+l)
		}
	}
	for _, pair := range common {
		if equal(pair[0], pair[1]) {
			continue
		}
		if err := undo(pair[0].Children, pair[1].Children, append(slices.Clone(path), pair[1].Line), out); err != nil {
			return err
		}
	}
	return nil
}

// refuseInside refuses a difference of line, inside the block path names,
// whose undo cannot be written line by line.
func refuseInside(path []string, line string) error {
	for _, p := range path {
		if slices.ContainsFunc(append(slices.Clone(unsupportedInside), unsupportedLines...), func(u string) bool { return strings.HasPrefix(p, u) }) {
			return fmt.Errorf("%w (%s)", ErrUnsupported, firstLine(p))
		}
	}
	if slices.ContainsFunc(unsupportedLines, func(u string) bool { return strings.HasPrefix(line, u) }) {
		return fmt.Errorf("%w (%s)", ErrUnsupported, firstLine(line))
	}
	return nil
}

// byLine indexes nodes by their line, the first of any repeated line.
func byLine(nodes []*Node) map[string]*Node {
	m := make(map[string]*Node, len(nodes))
	for _, n := range nodes {
		if _, seen := m[n.Line]; !seen {
			m[n.Line] = n
		}
	}
	return m
}

// subtree returns n's line and every line under it, indented from depth.
func subtree(n *Node, depth int) []string {
	lines := []string{strings.Repeat(" ", depth) + n.Line}
	for _, c := range n.Children {
		lines = append(lines, subtree(c, depth+1)...)
	}
	return lines
}

// equal reports whether two nodes hold the same line and the same lines
// under it, in the same order.
func equal(a, b *Node) bool {
	if a.Line != b.Line || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !equal(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// ErrNotDifferences is what Differences returns for output that is not
// show archive config differences' report, so a caller never reads an
// error or an unexpected answer as "no differences".
var ErrNotDifferences = errors.New("the device's answer is not a configuration difference report")

// Differences reads the output of show archive config differences: nil
// when the device reports no changes, else each difference line as the
// device printed it, its + or - marker after the indentation of the block
// it sits in ("+interface Loopback1", " +description x"). Lines no change
// makes (Parse's ignored lines) are left out.
func Differences(output string) ([]string, error) {
	if !strings.Contains(output, "Contextual Config Diffs") {
		return nil, fmt.Errorf("%w: %q", ErrNotDifferences, firstLine(output))
	}
	if strings.Contains(output, "No changes were found") {
		return nil, nil
	}
	var lines []string
	started := false
	for raw := range strings.SplitSeq(output, "\n") {
		raw = strings.TrimRight(raw, "\r \t")
		if strings.Contains(raw, "Contextual Config Diffs") {
			started = true
			continue
		}
		trimmed := strings.TrimLeft(raw, " ")
		if !started || trimmed == "" || strings.HasPrefix(trimmed, "!") {
			continue
		}
		if (strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-")) && isIgnored(strings.TrimLeft(trimmed[1:], " ")) {
			continue
		}
		lines = append(lines, raw)
	}
	return lines, nil
}

// firstLine returns text's first non-blank line, for an error.
func firstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
