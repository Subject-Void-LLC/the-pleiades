// Package netcfg: the words of a device's show output and log messages that
// use a secret word with no secret after it. The CLI text masker reads
// command output as well as configuration, and IOS prints "key" and
// "authentication" in column headers, field labels and log messages, where
// the masker took the words after them for a secret and hid them
// (FAILURE_PATTERNS 1268, measured on IOS XE 17.15 and IOL on 2026-11-17).
package netcfg

import "slices"

// outputPhrase is one measured phrase: the words from the secret word on,
// and the word that must stand right before the secret word. Both are
// matched exactly, case included, so a phrase names one line shape and
// nothing a configuration writes.
type outputPhrase struct {
	// before is the word right before the secret word.
	before string
	// words are the secret word and the words after it, in order.
	words []string
}

// outputPhrases are the measured shapes. Each is held to its whole phrase
// and the word before it, never to the secret word and the next word alone,
// so "key named" in a RADIUS server's block, say, is still a key:
//
//   - show lacp neighbor and show etherchannel detail's partner header,
//     "... Dev ID  Age  key  Key  Number  State" (IOS XE 17.15 on a Catalyst
//     8000V, and IOL);
//   - show ip ssh's "Minimum expected Diffie Hellman key size : 2048 bits"
//     (IOSvL2 and IOS XE);
//   - %CRYPTO_ENGINE-5-KEY_ADDITION, KEY_DELETED and KEY_REPLACE, "A key
//     named <label> has been ..." (IOS XE 17.15), and KEY_REPLACE's "...
//     with new key data", from the same message's text;
//   - %SSH-5-SSH2_USERAUTH, "User '<name>' authentication for SSH2 Session
//     from ..." (IOS XE 17.15 and IOL), whose before word is the quoted user
//     name and so is not fixed;
//   - show ip http server status's "HTTP server authentication method:
//     local" (IOS XE 17.15).
var outputPhrases = []outputPhrase{
	{before: "Age", words: []string{"key", "Key", "Number", "State"}},
	{before: "Hellman", words: []string{"key", "size", ":"}},
	{before: "A", words: []string{"key", "named"}},
	{before: "new", words: []string{"key", "data"}},
	{before: "", words: []string{"authentication", "for", "SSH2", "Session", "from"}},
	{before: "server", words: []string{"authentication", "method:"}},
}

// inOutputPhrase reports whether the secret word ws[i] begins one of
// outputPhrases. A phrase whose before word is empty needs the quoted user
// name IOS prints there: a word that opens and closes with a single quote.
func inOutputPhrase(ws []word, i int) bool {
	for _, p := range outputPhrases {
		if i == 0 || i+len(p.words) > len(ws) {
			continue
		}
		prev := ws[i-1].text
		if p.before == "" {
			if len(prev) < 2 || prev[0] != '\'' || prev[len(prev)-1] != '\'' {
				continue
			}
		} else if prev != p.before {
			continue
		}
		got := make([]string, len(p.words))
		for k := range p.words {
			got[k] = ws[i+k].text
		}
		if slices.Equal(got, p.words) {
			return true
		}
	}
	return false
}
