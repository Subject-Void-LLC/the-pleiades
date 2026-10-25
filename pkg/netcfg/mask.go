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
// snmp-server community x RO, crypto isakmp key x address y, standby 1
// authentication x, ip rip authentication string x, an IKEv2 keyring
// peer's pre-shared-key [local|remote] x, and an IKEv2 profile's
// authentication local pre-share key x. A key chain's "key 1"
// names a key, not a secret, so a line's leading "key" counts only outside
// a key chain block (a RADIUS or TACACS server's "key 7 x" holds one).
var secretWords = []string{
	"password", "secret", "authentication-key", "message-digest-key", "key-string",
	"area-password", "domain-password", "community", "key", "authentication",
	"pre-shared-key", "passphrase", "password-encrypt",
}

// skipped are the words that can stand between a secret word and its
// secret: an encryption type, a key id, an algorithm, a form.
var skipped = []string{"md5", "sha1", "sha256", "sha384", "sha512", "hmac-sha-256", "clear", "hash-algo", "text", "string"}

// notSecret are the words after "authentication" that name a mode rather
// than hold a secret: ip ospf authentication message-digest, ip rip
// authentication mode md5, isis authentication key-chain k, and an IKEv2
// profile's authentication local|remote pre-share, whose key, when the
// line holds one, follows a later "key".
var notSecret = []string{"message-digest", "null", "key-chain", "mode", "key-id", "local", "remote"}

// keyCommands are the words after "key" that name what a crypto key
// command does, not a secret: crypto key generate rsa, crypto key zeroize
// rsa, crypto key pubkey-chain rsa, and IOS's "% The key modulus size is
// 2048 bits" (measured on IOL XE 17.15).
var keyCommands = []string{"generate", "zeroize", "modulus", "pubkey-chain", "import", "export", "storage", "move", "decrypt", "encrypt", "lock", "unlock", "config-key"}

// keyTransferEnds are what crypto key import or export may end with that is
// not its passphrase: where the key comes from or goes, and its form.
var keyTransferEnds = []string{"terminal", "exportable", "pem", "rsa", "ec", "general-purpose", "signature", "encryption"}

// pskSides are the words that can stand between pre-shared-key and its
// secret in an IKEv2 keyring: the side of the exchange the key is for.
var pskSides = []string{"local", "remote"}

// pskPeers are the words after pre-shared-key that name a peer rather than
// hold a secret: an ISAKMP keyring's pre-shared-key address y key x, whose
// secret follows the later "key".
var pskPeers = []string{"address", "hostname"}

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
	keyChain := false
	for i, line := range lines {
		ws := words(line)
		if len(ws) > 0 && ws[0].start == 0 {
			// A line at the left margin opens a block or stands alone.
			keyChain = len(ws) >= 2 && ws[0].text == "key" && ws[1].text == "chain"
		}
		lines[i] = maskLine(line, keyChain)
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
// secret word that is not an encryption type, a key id, an algorithm or a
// mode. inKeyChain says the line is inside a key chain block, where a
// leading "key" names a key.
func maskLine(line string, inKeyChain bool) string {
	ws := words(line)
	// crypto key import|export ... <passphrase>: the passphrase is the last
	// word, after no word that names it.
	if len(ws) >= 4 && ws[0].text == "crypto" && ws[1].text == "key" && (ws[2].text == "import" || ws[2].text == "export") {
		last := ws[len(ws)-1]
		if !slices.Contains(keyTransferEnds, last.text) && !strings.Contains(last.text, ":") && !strings.HasPrefix(last.text, MaskPrefix) {
			return replaceWord(line, last)
		}
		return line
	}
	if len(ws) >= 3 && ws[0].text == "snmp-server" && ws[1].text == "host" {
		return maskSNMPHost(line, ws)
	}
	for i, w := range ws {
		if !slices.Contains(secretWords, w.text) || (w.text == "key" && i == 0 && inKeyChain) {
			continue
		}
		psk := w.text == "pre-shared-key"
		j := i + 1
		for j < len(ws)-1 && (isSmallNumber(ws[j].text) || slices.Contains(skipped, ws[j].text) || psk && slices.Contains(pskSides, ws[j].text)) {
			j++
		}
		if j >= len(ws) {
			return line
		}
		target := ws[j].text
		if strings.HasPrefix(target, MaskPrefix) {
			return line
		}
		// A mode, or another secret word whose own secret follows it
		// (standby 1 authentication md5 key-string x), is not the secret.
		if slices.Contains(secretWords, target) || (w.text == "authentication" && slices.Contains(notSecret, target)) || (psk && slices.Contains(pskPeers, target)) || (w.text == "key" && slices.Contains(keyCommands, target)) {
			continue
		}
		return replaceWord(line, ws[j])
	}
	return line
}

// maskSNMPHost masks an snmp-server host line's community: snmp-server
// host <addr> [traps|informs] [version 1|2c|3 [auth|noauth|priv]] <community>
// ...; SNMPv3's word there is a user, not a secret.
func maskSNMPHost(line string, ws []word) string {
	j := 3
	for j < len(ws) && (ws[j].text == "traps" || ws[j].text == "informs") {
		j++
	}
	if j+1 < len(ws) && ws[j].text == "version" {
		if ws[j+1].text == "3" {
			return line
		}
		j += 2
	}
	if j >= len(ws) || strings.HasPrefix(ws[j].text, MaskPrefix) {
		return line
	}
	return replaceWord(line, ws[j])
}

// replaceWord replaces one word of line with its placeholder.
func replaceWord(line string, w word) string {
	return line[:w.start] + Placeholder(w.text) + line[w.start+len(w.text):]
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
