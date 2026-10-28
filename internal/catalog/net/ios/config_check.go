// Package ios: net.ios.config's check, which reads the running
// configuration and predicts which of a task's lines would change it,
// sending none of them.
package ios

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// CheckConfig predicts Config: the same refusals and the same backup, then
// the lines that are not yet in the running configuration, under the
// blocks they sit in, as the differences a run would report. A call it
// cannot predict from the configuration (configCheckCall) is answered
// unchecked before the device is reached.
func CheckConfig(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := configCheckCall(params); err != nil {
		return collection.Result{}, err
	}
	return config(ctx, rc, device, params, collection.ModeCheck)
}

// configCheckCall is the part of the check that reads only the call: nil
// when the running configuration can tell what the lines would change, and
// a CannotCheck naming why when only applying them could. A malformed
// lines is left to the body, which refuses it in both modes.
func configCheckCall(params map[string]any) error {
	// A question is asked only as a line is applied, so what answers it
	// (and what it changes) cannot be read from the configuration.
	if clidialog.Uses(params) {
		return collection.CannotCheck("prompts and ask answer the questions a line asks as it is applied, and a check applies nothing")
	}
	lines, _, err := sdk.StringSlice(params, paramLines)
	if err != nil {
		return nil
	}
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		switch {
		case secretref.Has(l):
			// The device prints a secret only in its own encoding (type 7,
			// 8 or 9), so the value a reference names cannot be compared.
			return collection.CannotCheck(fmt.Sprintf("%s[%d] names a stored secret, which the device shows only in its own encoding, so whether it is already set cannot be read", paramLines, i))
		case keyMaterial.MatchString(l):
			return collection.CannotCheck(fmt.Sprintf("%s[%d] changes key material, which the configuration does not hold", paramLines, i))
		case strings.HasPrefix(trimmed, "banner "):
			return collection.CannotCheck(fmt.Sprintf("%s[%d] is a banner, whose text is entered as free text across lines", paramLines, i))
		case trimmed == "end":
			return collection.CannotCheck(fmt.Sprintf("%s[%d] is end, which leaves configuration mode, so the lines after it are not configuration", paramLines, i))
		}
	}
	return nil
}

// checkConfig records the predicted differences of lines against running,
// masked as the backup is, and reports changed when there is one.
func checkConfig(rc sdk.RunbookContext, running string, lines []string) (collection.Result, error) {
	// The configuration's own secrets join the run's masking set, as a run's
	// read of it does.
	_ = learnSecrets(rc, running)
	predicted := predictLines(iosconfig.Parse(running), lines)
	masked, err := maskPredicted(rc, predicted)
	if err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statDifferences, masked); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	return collection.Result{Changed: len(predicted) > 0}, nil
}

// predictedLine is one line of a check's predicted differences: a line of
// the block a change sits in (sign empty), or a line added (+) or removed
// (-), at its depth in the configuration.
type predictedLine struct {
	depth int
	sign  string
	line  string
}

// maskPredicted renders predicted as IOS's contextual differences do (the
// sign after the block's indentation), masked through maskedBackup, the
// backup's own masking: the lines are masked as the configuration text
// they are, signs set aside, and a line masking cannot vouch for leaves the
// call unchecked rather than recorded.
func maskPredicted(rc sdk.RunbookContext, predicted []predictedLine) ([]string, error) {
	out := []string{}
	if len(predicted) == 0 {
		return out, nil
	}
	text := make([]string, len(predicted))
	for i, p := range predicted {
		text[i] = strings.Repeat(" ", p.depth) + p.line
	}
	masked, err := maskedBackup(rc, strings.Join(text, "\n"))
	if err != nil {
		return nil, collection.CannotCheck("a line it would report holds a secret masking cannot vouch for, so the prediction is not recorded")
	}
	maskedLines := strings.Split(masked, "\n")
	if len(maskedLines) != len(predicted) {
		// Masking keeps every line; one that did not cannot be matched back
		// to its sign, so nothing is recorded.
		return nil, collection.CannotCheck("masking did not keep the predicted lines apart, so the prediction is not recorded")
	}
	for i, p := range predicted {
		l := maskedLines[i]
		at := min(p.depth, len(l))
		out = append(out, l[:at]+p.sign+l[at:])
	}
	return out, nil
}

// level is one block a line is entered in: the configuration's own node,
// or, synthetic, a block the lines open that the configuration lacks.
type level struct {
	node      *iosconfig.Node
	synthetic bool
}

// predictLines walks lines as IOS's configuration mode would take them and
// returns the differences they would make to the configuration roots holds.
// Each line is looked for, as written, among the lines of the block the
// lines before it entered, then of each block enclosing that one: found, it
// changes nothing and, when it opens a block, becomes the block the next
// line is looked for in.
// Once the lines have opened a block the configuration lacks, a line is
// looked for at the top level only, so a line of the new block never
// matches the same line in a block the lines left. A "no X" line found
// nowhere removes what X begins (the lines it names, shown with -), and
// with nothing printed to remove is predicted to change, since IOS prints
// neither some defaults nor their negations. exit leaves one block, and
// comments and do commands change nothing.
func predictLines(roots []*iosconfig.Node, lines []string) []predictedLine {
	p := predictor{stack: []level{{node: &iosconfig.Node{Children: roots}}}}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "do "):
			continue
		case line == "exit":
			if len(p.stack) > 1 {
				p.stack = p.stack[:len(p.stack)-1]
			}
			continue
		}
		if at, n := p.find(func(c *iosconfig.Node) bool { return c.Line == line }); n != nil {
			// Found, the line is entered in its block; it is the block the
			// next line is looked for in only when it opens one (hostname
			// does not).
			p.stack = p.stack[:at+1]
			if len(n[0].Children) > 0 || isOpener(line) {
				p.stack = append(p.stack, level{node: n[0]})
			}
			continue
		}
		if x, ok := strings.CutPrefix(line, "no "); ok {
			at, removed := p.find(func(c *iosconfig.Node) bool { return c.Line == x || strings.HasPrefix(c.Line, x+" ") })
			if len(removed) > 0 {
				for _, r := range removed {
					p.removeTree(p.path(at), r)
				}
				continue
			}
		}
		if at := p.opens(line); at >= 0 {
			p.emit(p.path(at), "+", line)
			p.stack = append(p.stack[:at+1], level{node: &iosconfig.Node{Line: line}, synthetic: true})
			continue
		}
		p.emit(p.path(len(p.stack)-1), "+", line)
	}
	return p.out
}

// predictor is predictLines' state: the blocks the lines have entered
// (stack[0] the top level), the differences so far, and the block path of
// the last line written, so a block's line is written once above the
// changes inside it.
type predictor struct {
	stack []level
	out   []predictedLine
	shown []string
}

// find returns the level and the nodes matching match among the children
// of the innermost block holding any, searching from the current block
// outward; with a synthetic block entered, only the top level is searched.
// It returns -1 and nil when none matches.
func (p *predictor) find(match func(*iosconfig.Node) bool) (int, []*iosconfig.Node) {
	from := len(p.stack) - 1
	if slices.ContainsFunc(p.stack, func(l level) bool { return l.synthetic }) {
		from = 0
	}
	for i := from; i >= 0; i-- {
		var found []*iosconfig.Node
		for _, c := range p.stack[i].node.Children {
			if match(c) {
				found = append(found, c)
			}
		}
		if len(found) > 0 {
			return i, found
		}
	}
	return -1, nil
}

// opens returns the level whose block a line the configuration lacks opens
// a new block inside, or -1 when it is a line of the current block. A line
// opens one when a block in the configuration starts with the same keyword
// (the innermost such level), or when it is one of the blocks IOS always
// opens at the top level (blockOpeners) or inside the current one
// (nestedOpeners). Taking a line for a new block only ever predicts more
// change, never less.
func (p *predictor) opens(line string) int {
	key := keyword(line)
	for i := len(p.stack) - 1; i >= 0; i-- {
		if slices.ContainsFunc(p.stack[i].node.Children, func(c *iosconfig.Node) bool { return len(c.Children) > 0 && keyword(c.Line) == key }) {
			return i
		}
	}
	if slices.ContainsFunc(blockOpeners, func(o string) bool { return strings.HasPrefix(line, o) }) {
		return 0
	}
	if slices.ContainsFunc(nestedOpeners, func(o string) bool { return strings.HasPrefix(line, o) }) {
		return len(p.stack) - 1
	}
	return -1
}

// path is the lines of the blocks from the top level to stack[at].
func (p *predictor) path(at int) []string {
	out := make([]string, 0, at)
	for _, l := range p.stack[1 : at+1] {
		out = append(out, l.node.Line)
	}
	return out
}

// emit writes line with sign inside the block path names, first writing
// each of path's lines the last line written did not already sit under.
func (p *predictor) emit(path []string, sign, line string) {
	common := 0
	for common < len(path) && common < len(p.shown) && path[common] == p.shown[common] {
		common++
	}
	for i := common; i < len(path); i++ {
		p.out = append(p.out, predictedLine{depth: i, line: path[i]})
	}
	p.out = append(p.out, predictedLine{depth: len(path), sign: sign, line: line})
	p.shown = append(slices.Clone(path), line)
}

// removeTree writes n and every line under it as removed, inside path.
func (p *predictor) removeTree(path []string, n *iosconfig.Node) {
	p.emit(path, "-", n.Line)
	inner := append(slices.Clone(path), n.Line)
	for _, c := range n.Children {
		p.removeTree(inner, c)
	}
}

// keyword is what a line's block is known by: its first word, and for a
// first word many kinds of block share (ip, ipv6, mac, crypto, aaa), its
// second too.
func keyword(line string) string {
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return ""
	case len(fields) > 1 && slices.Contains([]string{"ip", "ipv6", "mac", "crypto", "aaa"}, fields[0]):
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

// isOpener reports whether line is one of the lines IOS always takes as
// opening a block (blockOpeners, nestedOpeners).
func isOpener(line string) bool {
	return slices.ContainsFunc(append(slices.Clone(blockOpeners), nestedOpeners...), func(o string) bool { return strings.HasPrefix(line, o) })
}

// blockOpeners are lines that open a block at the top level of an IOS
// configuration, whether or not the configuration holds one yet.
var blockOpeners = []string{
	"interface ", "router ", "line ", "vlan ", "vrf definition ", "ip vrf ",
	"ip access-list ", "ipv6 access-list ", "mac access-list ", "object-group ",
	"class-map ", "policy-map ", "route-map ", "key chain ", "track ", "ip sla ",
	"crypto ikev2 ", "crypto isakmp policy ", "crypto isakmp profile ", "crypto keyring ",
	"crypto ipsec profile ", "crypto ipsec transform-set ", "crypto map ", "crypto pki trustpoint ",
	"aaa group server ", "ip dhcp pool ", "parameter-map ", "zone security ", "zone-pair security ",
	"flow record ", "flow exporter ", "flow monitor ", "control-plane", "archive", "redundancy",
}

// nestedOpeners are lines that open a block inside the block they are
// entered in: an address family under a router or a VRF, a class under a
// policy map.
var nestedOpeners = []string{"address-family ", "class "}
