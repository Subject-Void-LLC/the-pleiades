// Package netcfg: masking a whole configuration that a method hands back
// as it is (net.ios.facts's config subset), where a secret the masker
// passed over must stop the read rather than reach a stat.
package netcfg

import (
	"fmt"
	"slices"
	"strings"
)

// placeholderLen is the length of one placeholder Placeholder writes: the
// prefix and sixteen hexadecimal digits.
var placeholderLen = len(MaskPrefix) + 16

// MaskChecked is MaskFound for a configuration returned whole. MaskFound
// masks one secret a line, so a line holding two (a word after the first
// placeholder that is itself a secret word's secret) would still carry the
// second. MaskChecked reads each masked line again past its last
// placeholder, with the masker's own rules, and returns an error when that
// finds another secret: the caller then returns none of the text. The error
// names the line's number and the word the secret follows, never the
// secret. found is MaskFound's, for the run's masking set.
func MaskChecked(config string) (masked string, found []string, err error) {
	masked, found = MaskFound(config)
	keyChain := false
	for i, line := range strings.Split(masked, "\n") {
		ws := words(line)
		if len(ws) > 0 && ws[0].start == 0 {
			// The same block tracking MaskFound does: a line at the left
			// margin opens a block or stands alone.
			keyChain = len(ws) >= 2 && ws[0].text == "key" && ws[1].text == "chain"
		}
		// A manual IPsec session key line holds a key in every word past its
		// SPI that is not cipher or authenticator, so it is held to that whole
		// rule rather than read only past its last placeholder.
		if before, left := sessionKeyLeft(ws); left {
			return "", nil, fmt.Errorf("line %d holds a session key, after %q, that masking left in place", i+1, before)
		}
		at := strings.LastIndex(line, MaskPrefix)
		if at < 0 {
			// No placeholder: MaskFound read this whole line and found no
			// secret in it, and reading it again would find the same.
			continue
		}
		tail := line[min(at+placeholderLen, len(line)):]
		var more []string
		again := maskLine(tail, keyChain, &more)
		if len(more) > 0 {
			return "", nil, fmt.Errorf("line %d holds a second secret, after %q, that masking one secret a line leaves in place", i+1, wordBeforePlaceholder(again))
		}
	}
	return masked, found, nil
}

// wordBeforePlaceholder is the keyword (password, key, secret) the first
// placeholder in text follows, stepping back over an encryption type, a key
// id or an algorithm between them, for an error that may name the keyword
// and never the secret.
func wordBeforePlaceholder(text string) string {
	ws := strings.Fields(text)
	for i, w := range ws {
		if !strings.HasPrefix(w, MaskPrefix) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if !isSmallNumber(ws[j]) && !slices.Contains(skipped, ws[j]) {
				return ws[j]
			}
		}
	}
	return "a secret word"
}
