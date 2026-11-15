// Package netcfg: the SNMP secrets of a net-snmp agent's snmpd.conf, which a
// Linux device or an appliance built on net-snmp holds where a Cisco device
// holds snmp-server lines: a community after rocommunity, rwcommunity, their
// IPv6 forms or com2sec, and an SNMPv3 user's passphrases after createUser.
package netcfg

import (
	"slices"
	"strings"
)

// netSNMPCommunityWords open a line whose first argument is a community:
// rocommunity COMMUNITY [SOURCE [OID | -V VIEW [CONTEXT]]], and the same for
// rwcommunity and the IPv6 forms (snmpd.conf(5)).
var netSNMPCommunityWords = []string{"rocommunity", "rwcommunity", "rocommunity6", "rwcommunity6"}

// netSNMPAuthAlgorithms and netSNMPPrivAlgorithms are what createUser names
// before each passphrase: createUser [-e ENGINEID] USER
// (MD5|SHA|SHA-224|SHA-256|SHA-384|SHA-512) AUTHPASS [DES|AES|AES-192|AES-256]
// [PRIVPASS]. net-snmp reads them in any case.
var (
	netSNMPAuthAlgorithms = []string{"md5", "sha", "sha-224", "sha-256", "sha-384", "sha-512", "sha1"}
	netSNMPPrivAlgorithms = []string{"des", "aes", "aes-128", "aes-192", "aes-256", "aes128", "aes192", "aes256"}
)

// maskNetSNMP masks the secrets of one snmpd.conf line, reporting whether the
// line was one of net-snmp's SNMP lines at all (when it was not, the caller
// reads it as any other configuration line).
func maskNetSNMP(line string, ws []word, found *[]string) (string, bool) {
	if len(ws) < 2 {
		return line, false
	}
	keyword := strings.ToLower(ws[0].text)
	switch {
	case slices.Contains(netSNMPCommunityWords, keyword):
		return maskNetSNMPWord(line, ws, 1, found), true
	case keyword == "com2sec" || keyword == "com2sec6":
		// com2sec [-Cn CONTEXT] NAME SOURCE COMMUNITY: the community is the
		// third argument after any -Cn option.
		at := 1
		if at+1 < len(ws) && ws[at].text == "-Cn" {
			at += 2
		}
		return maskNetSNMPWord(line, ws, at+2, found), true
	case keyword == "createuser":
		return maskCreateUser(line, ws, found), true
	}
	return line, false
}

// maskNetSNMPWord masks the argument at ws[at], or a quoted one starting
// there, unless the line ends first or it is a placeholder already.
func maskNetSNMPWord(line string, ws []word, at int, found *[]string) string {
	if at >= len(ws) || strings.HasPrefix(ws[at].text, MaskPrefix) || strings.HasPrefix(ws[at].text, `"`+MaskPrefix) {
		return line
	}
	return replaceArgument(line, ws, at, found)
}

// maskCreateUser masks a createUser line's authentication passphrase, after
// its algorithm, and its privacy passphrase, after its own; a line naming a
// privacy algorithm with no passphrase after it reuses the authentication
// one, so holds no second secret.
func maskCreateUser(line string, ws []word, found *[]string) string {
	// The user is the first argument after any -e ENGINEID option; the
	// algorithm follows it.
	at := 1
	if at+1 < len(ws) && ws[at].text == "-e" {
		at += 2
	}
	auth := at + 1
	if auth >= len(ws) || !slices.Contains(netSNMPAuthAlgorithms, strings.ToLower(ws[auth].text)) {
		return line
	}
	// The privacy passphrase is found first and masked last, from the end of
	// the line back, so the authentication passphrase's offset holds.
	authEnd := argumentEnd(ws, auth+1)
	priv := authEnd + 1
	if priv+1 < len(ws) && slices.Contains(netSNMPPrivAlgorithms, strings.ToLower(ws[priv].text)) {
		line = maskNetSNMPWord(line, ws, priv+1, found)
	}
	return maskNetSNMPWord(line, ws, auth+1, found)
}

// argumentEnd is the index of the last word of the argument starting at
// ws[at]: the word itself, or for a double-quoted argument the word its
// closing quote ends, or the line's last word when it has none.
func argumentEnd(ws []word, at int) int {
	if at >= len(ws) || !strings.HasPrefix(ws[at].text, `"`) || (len(ws[at].text) > 1 && strings.HasSuffix(ws[at].text, `"`)) {
		return at
	}
	for k := at + 1; k < len(ws); k++ {
		if strings.HasSuffix(ws[k].text, `"`) {
			return k
		}
	}
	return len(ws) - 1
}

// replaceArgument replaces the argument starting at ws[at] with its
// placeholder: one word, or a double-quoted one, spaces and all, with its
// quotes kept so the line still reads as net-snmp's.
func replaceArgument(line string, ws []word, at int, found *[]string) string {
	end := argumentEnd(ws, at)
	start, stop := ws[at].start, ws[end].start+len(ws[end].text)
	if strings.HasPrefix(ws[at].text, `"`) {
		start++
		if stop-1 > start && line[stop-1] == '"' {
			stop--
		}
	}
	if stop <= start {
		return line
	}
	return replaceWord(line, word{text: line[start:stop], start: start}, found)
}
