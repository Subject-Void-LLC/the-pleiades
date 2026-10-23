// Package netcfg reads a network device's configuration text: it masks the
// secrets in it before any of it leaves the method that read it, and lays an
// FRRouting configuration out the way Batfish reads one. A Collection may
// import only pkg/, and both the FRR methods and net.batfish.check need the
// same masking, so a secret masked for a model's eyes and one masked for
// Batfish's are masked alike.
package netcfg

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// MaskPrefix begins every placeholder.
const MaskPrefix = "pleiades-masked-"

// secretWords are the words a secret follows, in FRRouting's and Cisco's
// configurations: password zebra, neighbor 10.0.0.2 password x, ip ospf
// message-digest-key 1 md5 x, area-password md5 x, enable secret 9 x,
// snmp-server community x RO, crypto isakmp key x address y. A key chain's
// "key 1" names a key, not a secret, so "key" counts only after a line's
// first word.
var secretWords = []string{
	"password", "secret", "authentication-key", "message-digest-key", "key-string",
	"area-password", "domain-password", "community", "key",
}

// skipped are the words that can stand between a secret word and its
// secret: an encryption type, a key id, an algorithm.
var skipped = []string{"md5", "sha1", "sha256", "sha384", "sha512", "hmac-sha-256", "clear", "hash-algo"}

// maskKey keys the placeholders: random, made once per process and never
// written anywhere, so a placeholder is the same for one secret throughout
// one command-line run and says nothing about the secret to anyone without
// the key.
var maskKey = sync.OnceValue(func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(fmt.Sprintf("netcfg: reading randomness for a mask key: %v", err))
	}
	return k
})

// Mask replaces every secret in config with its placeholder, leaving every
// other byte where it was. Equal secrets get equal placeholders within one
// process, so two routers' keys can still be compared.
func Mask(config string) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		lines[i] = maskLine(line)
	}
	return strings.Join(lines, "\n")
}

// Placeholder is secret's stand-in.
func Placeholder(secret string) string {
	mac := hmac.New(sha256.New, maskKey())
	mac.Write([]byte(secret))
	return MaskPrefix + hex.EncodeToString(mac.Sum(nil))[:16]
}

// word is one word of a line and where it starts.
type word struct {
	text  string
	start int
}

// words splits line at every Unicode space, as strings.Fields does,
// keeping each word's offset. Splitting at fewer (spaces and tabs only)
// let "password\vzebra" pass as one word to a reader that splits at all of
// them (found by FuzzMask).
func words(line string) []word {
	var out []word
	start := -1
	for i, r := range line {
		blank := unicode.IsSpace(r)
		switch {
		case !blank && start < 0:
			start = i
		case blank && start >= 0:
			out = append(out, word{line[start:i], start})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, word{line[start:], start})
	}
	return out
}

// maskLine masks the secret one line holds, if any: the first word after a
// secret word that is not an encryption type, a key id or an algorithm.
func maskLine(line string) string {
	ws := words(line)
	for i, w := range ws {
		if !slices.Contains(secretWords, w.text) || (w.text == "key" && i == 0) {
			continue
		}
		j := i + 1
		for j < len(ws)-1 && (isSmallNumber(ws[j].text) || slices.Contains(skipped, ws[j].text)) {
			j++
		}
		if j >= len(ws) || strings.HasPrefix(ws[j].text, MaskPrefix) {
			return line
		}
		s := ws[j]
		return line[:s.start] + Placeholder(s.text) + line[s.start+len(s.text):]
	}
	return line
}

// isSmallNumber reports whether s is a key id or an encryption type: one to
// three digits.
func isSmallNumber(s string) bool {
	if len(s) == 0 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
