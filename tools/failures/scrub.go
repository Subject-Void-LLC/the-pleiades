// Keeping a machine's details and secrets out of a ledger: every piece of
// free text an entry stores passes through scrub first.
package main

import (
	"net"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// maxText is the most bytes of free text one field of an entry holds.
const maxText = 200

// The placeholders that stand in for what scrub removes.
const (
	userHostMark = "<user@host>"
	ipMark       = "<ip>"
	homeMark     = "~"
)

var (
	// escapeRE matches a terminal escape sequence, which a CI log carries
	// for colour.
	escapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// homeRE matches a home directory on Linux, macOS or Windows (through
	// WSL's /mnt/c too), up to the user's own name.
	homeRE = regexp.MustCompile(`(?i)(?:/home/[^/\s:'"]+|/root\b|(?:[a-z]:|/mnt/[a-z])?[\\/]users[\\/][^\\/\s:'"]+)`)
	// userHostRE matches a user@host name; versionRE is the host part of a
	// Go module version (pkg@v1.2.3), which is not one.
	userHostRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*`)
	versionRE  = regexp.MustCompile(`@v[0-9]`)
	// ipv4RE and ipv6RE match what could be an address; net.ParseIP decides.
	ipv4RE = regexp.MustCompile(`\b[0-9]{1,3}(?:\.[0-9]{1,3}){3}\b`)
	ipv6RE = regexp.MustCompile(`[0-9A-Fa-f]*:[0-9A-Fa-f]*:[0-9A-Fa-f:.]*`)
)

// learnSecrets teaches the shared ruleset the value of every environment
// variable whose name it calls secret, as testgate does, so a token a log
// printed is masked here too.
func learnSecrets() {
	literals := redact.Shared().Literals()
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if ok && len(value) >= redact.MinLiteralLength && redact.SecretName(name) {
			literals.Add(value)
		}
	}
}

// scrub makes text fit for a ledger: one line, no control characters, masked
// by the shared ruleset, each user@host, IP address and home directory
// replaced by a placeholder, and at most maxText bytes.
func scrub(text string) string {
	text, _, _ = strings.Cut(text, "\n")
	text = escapeRE.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || r == 0xfeff:
			return -1 // a control character or a byte order mark says nothing
		}
		return r
	}, text)
	text = redact.Text(nil, text)
	text = homeRE.ReplaceAllString(text, homeMark)
	text = userHostRE.ReplaceAllStringFunc(text, func(m string) string {
		if versionRE.MatchString(m) {
			return m
		}
		return userHostMark
	})
	text = ipv6RE.ReplaceAllStringFunc(text, func(m string) string {
		// The match may run on into a port's colon or a sentence's full stop,
		// so shorten it from the end until it is an address, if it is one.
		for ip := m; strings.Count(ip, ":") >= 2; ip = ip[:len(ip)-1] {
			if net.ParseIP(ip) != nil {
				return ipMark + m[len(ip):]
			}
		}
		return m
	})
	text = ipv4RE.ReplaceAllStringFunc(text, func(m string) string {
		if net.ParseIP(m) != nil {
			return ipMark
		}
		return m
	})
	return truncate(strings.TrimSpace(text), maxText)
}

// truncate cuts text to at most n bytes, at a character boundary.
func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut-- // step back to the start of the character the limit splits
	}
	return text[:cut]
}
