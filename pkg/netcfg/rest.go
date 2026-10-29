// Package netcfg: the secrets that are not one word. IOS reads a clear-text
// (type 0, or untyped) password, secret or key to the end of its line, so
// "username admin password 0 two words" holds the password "two words", and
// a few commands hold more than one secret on a line (an SNMPv3 user's auth
// and priv passwords, an OSPFv3 IPsec SPI's keys, an ipsec-manual crypto
// map entry's ESP cipher and authenticator session keys).
package netcfg

import (
	"slices"
	"strings"
)

// snmpUserAlgorithms are the words that can stand between an snmp-server
// user line's auth or priv and the password after it: snmp-server user u g
// v3 auth sha x priv aes 128 y.
var snmpUserAlgorithms = []string{"md5", "sha", "sha-2", "des", "3des", "aes", "128", "192", "256", "384", "512"}

// ipsecWords are the words of an OSPFv3 IPsec SPI line that are not keys:
// ipv6 ospf authentication ipsec spi 500 md5 0 k, ospfv3 encryption ipsec
// spi 1001 esp aes-cbc 128 0 k1 sha1 0 k2. A key-encryption type (0 or 7)
// is a small number, which is never a key here: each key is hexadecimal and
// at least 32 digits long.
var ipsecWords = []string{"ipsec", "spi", "md5", "sha1", "esp", "null", "des", "3des", "aes-cbc", "128", "192", "256"}

// secretIsOneWord reports whether the secret after ws[i] is one word that
// more of the command may follow, rather than the rest of the line: an SNMP
// community (blank spaces are not permitted, and RO, RW or an ACL follow), an
// IS-IS area or domain password (authenticate snp may follow), a mode or
// authentication string ("authentication" names many things, and the strings
// it takes, HSRP's, VRRP's and NHRP's, are at most eight characters with no
// spaces), an NTP key (its encryption type follows it), an IS-IS interface
// password (level-1 or level-2 follows), and a crypto isakmp key (address or
// hostname follows).
func secretIsOneWord(ws []word, i int) bool {
	switch ws[i].text {
	case "community", "area-password", "domain-password", "authentication":
		return true
	}
	switch ws[0].text {
	case "ntp", "isis":
		return true
	case "crypto":
		return len(ws) > 1 && ws[1].text == "isakmp"
	}
	return false
}

// typedSecret reports whether the secret at ws[j], after the secret word at
// ws[i], carries an encryption type other than 0 (5, 6, 7, 8, 9): such a
// secret is a hash or an encoding, which is one word. A number after
// "level" is a privilege level (enable password level 15 x), not a type.
func typedSecret(ws []word, i, j int) bool {
	if j-1 <= i || !isSmallNumber(ws[j-1].text) || ws[j-1].text == "0" {
		return false
	}
	return j-2 < 0 || ws[j-2].text != "level"
}

// replaceRest replaces ws[0] through the last word of ws in line with one
// placeholder, adding the text it replaced, spaces inside it included, to
// found. Blank space after the last word stays (a carriage return the
// device ended the line with, say).
func replaceRest(line string, ws []word, found *[]string) string {
	first, last := ws[0], ws[len(ws)-1]
	end := last.start + len(last.text)
	return replaceWord(line, word{text: line[first.start:end], start: first.start}, found)
}

// maskSNMPUser masks an snmp-server user line's auth and priv passwords:
// snmp-server user u g v3 [encrypted] auth {md5|sha} x [priv {des|3des|aes
// {128|192|256}} y] [access acl]. Each is one word.
func maskSNMPUser(line string, ws []word, found *[]string) string {
	// Masking from the last word back keeps every earlier word's offset.
	for k := len(ws) - 1; k >= 2; k-- {
		if ws[k].text != "auth" && ws[k].text != "priv" {
			continue
		}
		j := k + 1
		for j < len(ws)-1 && slices.Contains(snmpUserAlgorithms, ws[j].text) {
			j++
		}
		// A line that ends at an algorithm holds no password there, and a
		// placeholder is a password masked already.
		if j >= len(ws) || slices.Contains(snmpUserAlgorithms, ws[j].text) || strings.HasPrefix(ws[j].text, MaskPrefix) {
			continue
		}
		line = replaceWord(line, ws[j], found)
	}
	return line
}

// maskIPsecSPI masks every key on an OSPFv3 IPsec SPI line: each word after
// the SPI's number that is not an algorithm, a key length or a key
// encryption type.
func maskIPsecSPI(line string, ws []word, spi int, found *[]string) string {
	// Masking from the last word back keeps every earlier word's offset.
	for k := len(ws) - 1; k > spi+1; k-- {
		t := ws[k].text
		if slices.Contains(ipsecWords, t) || isSmallNumber(t) || strings.HasPrefix(t, MaskPrefix) {
			continue
		}
		line = replaceWord(line, ws[k], found)
	}
	return line
}

// sessionKeyWords are the words of a manual IPsec session key line, past
// its SPI, that are not keys: set session-key {inbound|outbound} esp <spi>
// cipher <hex> [authenticator <hex>].
var sessionKeyWords = []string{"cipher", "authenticator"}

// sessionKeySPI returns the index of the SPI on an ipsec-manual crypto map
// entry's session key line, set session-key {inbound|outbound} {ah|esp}
// <spi> ..., or -1 when the line is not one. Every word after the SPI is a
// key or one of sessionKeyWords (IOS XE's set session-key: ah <spi>
// <hex-key>, esp <spi> cipher <hex-key> [authenticator <hex-key>]).
func sessionKeySPI(ws []word) int {
	if len(ws) < 5 || ws[0].text != "set" || ws[1].text != "session-key" {
		return -1
	}
	if ws[3].text != "ah" && ws[3].text != "esp" {
		return -1
	}
	return 4
}

// maskSessionKey masks every key on a manual IPsec session key line: each
// word after the SPI that is not cipher or authenticator. Masking every
// such word, rather than only the word after each keyword, keeps a key in
// a form the reference does not list (esp <spi> authenticator <hex>, say)
// from passing unmasked.
func maskSessionKey(line string, ws []word, spi int, found *[]string) string {
	// Masking from the last word back keeps every earlier word's offset.
	for k := len(ws) - 1; k > spi; k-- {
		t := ws[k].text
		if slices.Contains(sessionKeyWords, t) || strings.HasPrefix(t, MaskPrefix) {
			continue
		}
		line = replaceWord(line, ws[k], found)
	}
	return line
}

// sessionKeyLeft reports the word before the first key a session key line
// still holds after masking, for MaskChecked: every word past the SPI must
// be cipher, authenticator or a placeholder. ok is true only when such a
// key is left; a line that is not a session key line is never one.
func sessionKeyLeft(ws []word) (before string, ok bool) {
	spi := sessionKeySPI(ws)
	if spi < 0 {
		return "", false
	}
	for k := spi + 1; k < len(ws); k++ {
		t := ws[k].text
		if !slices.Contains(sessionKeyWords, t) && !strings.HasPrefix(t, MaskPrefix) {
			return ws[k-1].text, true
		}
	}
	return "", false
}

// ipsecSPIAt returns the index of an OSPFv3 line's "spi" word when the line
// is "... ipsec spi <n> ...", or -1.
func ipsecSPIAt(ws []word) int {
	for k := 1; k+1 < len(ws); k++ {
		if ws[k].text == "spi" && ws[k-1].text == "ipsec" {
			return k
		}
	}
	return -1
}
