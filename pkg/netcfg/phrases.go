// Package netcfg: the words of a device's show output and log messages that
// use a secret word with no secret after it. The CLI text masker reads
// command output as well as configuration, and IOS prints "key" and
// "authentication" in column headers, field labels and log messages, where
// the masker took the words after them for a secret and hid them
// (FAILURE_PATTERNS 1268, measured on IOS XE 17.15 and IOL on 2026-11-17).
package netcfg

import (
	"regexp"
	"slices"
	"strings"
)

// outputPhrase is one measured phrase: the secret word and the words after
// it (words), and the line's own context before them, which a configuration
// line cannot have. A phrase matched only by its words and the word before
// them also matched "tacacs-server host A key named Xyz123", leaving the key
// in the clear, so the context is the line's whole start: lead, the exact
// words from the line's first word up to the secret word, for a show
// command's header or field; or for a log message its tag, the %FACILITY
// word that must stand tagAt words before the secret word with nothing but
// a syslog timestamp before it on the line, and between the tag and the
// secret word exactly between (or, for an empty between, any words). Words
// are matched exactly, case included.
type outputPhrase struct {
	// lead is every word before the secret word, from the line's start.
	lead []string
	// tag is the start of the log message's tag word ("%SSH-5-SSH2_USERAUTH:").
	tag string
	// between are the words from the tag to the secret word, a "'" meaning
	// a quoted user name; nil allows any.
	between []string
	// words are the secret word and the words after it, in order.
	words []string
}

// outputPhrases are the measured shapes:
//
//   - show lacp neighbor and show etherchannel detail's partner header,
//     "Port  Flags  Priority  Dev ID  Age  key  Key  Number  State" (IOS XE
//     17.15 on a Catalyst 8000V, and IOL);
//   - show ip ssh's "Minimum expected Diffie Hellman key size : 2048 bits"
//     (IOSvL2 and IOS XE);
//   - %CRYPTO_ENGINE-5-KEY_ADDITION, KEY_DELETED and KEY_REPLACE, "A key
//     named <label> has been ..." (IOS XE 17.15), and KEY_REPLACE's "...
//     with new key data", from the same message's text;
//   - %SSH-5-SSH2_USERAUTH, "User '<name>' authentication for SSH2 Session
//     from ..." (IOS XE 17.15 and IOL);
//   - show ip http server status's "HTTP server authentication method:
//     local" (IOS XE 17.15).
var outputPhrases = []outputPhrase{
	{lead: []string{"Port", "Flags", "Priority", "Dev", "ID", "Age"}, words: []string{"key", "Key", "Number", "State"}},
	{lead: []string{"Minimum", "expected", "Diffie", "Hellman"}, words: []string{"key", "size", ":"}},
	{lead: []string{"HTTP", "server"}, words: []string{"authentication", "method:"}},
	{tag: "%CRYPTO_ENGINE-", between: []string{"A"}, words: []string{"key", "named"}},
	{tag: "%CRYPTO_ENGINE-5-KEY_REPLACE:", words: []string{"key", "data"}},
	{tag: "%SSH-5-SSH2_USERAUTH:", between: []string{"User", "'"}, words: []string{"authentication", "for", "SSH2", "Session", "from"}},
}

// syslogPrefix is what may stand before a log message's tag on its line:
// nothing, or a sequence number and a timestamp as IOS prints them ("000123:
// *Nov 17 02:13:31.437 UTC: ", "Nov 17 2026 02:13:31: ", "1d02h: ").
var syslogPrefix = regexp.MustCompile(`^\s*(?:\d+:\s+)?(?:[*.]?(?:[A-Z][a-z]{2}\s+\d{1,2}\s+(?:\d{4}\s+)?\d{1,2}:\d{2}:\d{2}(?:\.\d+)?(?:\s+[A-Z]{2,5})?|\d+[wdh]\d+[dhm]|\d{2}:\d{2}:\d{2}(?:\.\d+)?):\s+)?$`)

// inOutputPhrase reports whether the secret word ws[i] of line begins one
// of outputPhrases, in that phrase's own context.
func inOutputPhrase(line string, ws []word, i int) bool {
	for _, p := range outputPhrases {
		if i+len(p.words) > len(ws) || !wordsAre(ws[i:i+len(p.words)], p.words) {
			continue
		}
		if p.lead != nil {
			if i == len(p.lead) && wordsAre(ws[:i], p.lead) {
				return true
			}
			continue
		}
		if inLogContext(line, ws, i, p) {
			return true
		}
	}
	return false
}

// inLogContext reports whether ws[i] stands in p's log message: p's tag word
// before it, with only a syslog timestamp before the tag on the line, and
// p.between, when given, the words between them.
func inLogContext(line string, ws []word, i int, p outputPhrase) bool {
	for t := range i {
		if !strings.HasPrefix(ws[t].text, p.tag) || !strings.HasSuffix(ws[t].text, ":") {
			continue
		}
		// The tag is the message's own: only a timestamp is before it, so a
		// configuration line, which begins with its command, never matches.
		if !syslogPrefix.MatchString(line[:ws[t].start]) {
			return false
		}
		if p.between == nil {
			return true
		}
		if i-t-1 != len(p.between) {
			return false
		}
		for k, want := range p.between {
			got := ws[t+1+k].text
			if want == "'" {
				if len(got) < 2 || got[0] != '\'' || got[len(got)-1] != '\'' {
					return false
				}
				continue
			}
			if got != want {
				return false
			}
		}
		return true
	}
	return false
}

// wordsAre reports whether ws hold exactly want.
func wordsAre(ws []word, want []string) bool {
	return slices.EqualFunc(ws, want, func(w word, s string) bool { return w.text == s })
}
