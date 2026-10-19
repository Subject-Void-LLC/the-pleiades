// HeaderWarnings: the advice that a runbook open with a comment saying what
// it does.
package validate

import (
	"bytes"
	"strings"
)

// HeaderRule is the RuleName of the header comment's warning.
const HeaderRule = "header"

// headerAdvice is the header warning's message.
const headerAdvice = "the runbook does not open with a comment saying what it does; start the file with # lines naming what it does, what it changes, and how to undo it"

// notAHeader are the comment openings that say nothing about what a
// runbook does: an editor's modeline, a schema hint, a licence or a
// copyright line. Each is compared without regard to case.
var notAHeader = []string{"yaml-language-server:", "vim:", "vi:", "-*-", "spdx-license-identifier:", "copyright"}

// HeaderWarnings advises, in a warning that never fails validation, that a
// YAML runbook open with a comment saying what it does, so whoever opens
// the file learns that before reading its tasks. The header is the comment
// lines before the first line of YAML (a blank line, a document marker or
// a directive may come first); it counts when one of them has text and is
// not only a modeline, schema hint, licence or copyright line. The
// runbook's metadata description does not count: the comment is what a
// reader of the file sees, and the advice is to write one.
func HeaderWarnings(source []byte) []Finding {
	source = bytes.TrimPrefix(source, []byte("\xef\xbb\xbf"))
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || strings.HasPrefix(line, "%") {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		if describes(strings.TrimSpace(strings.TrimLeft(line, "#"))) {
			return nil
		}
	}
	return []Finding{{RuleName: HeaderRule, Message: headerAdvice}}
}

// describes reports whether a header comment's text says something: it is
// not empty and is not one of the openings in notAHeader.
func describes(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, prefix := range notAHeader {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}
