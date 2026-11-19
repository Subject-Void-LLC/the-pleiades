// Package netcfg: running-configuration digests (Phase 41c). A network method
// records config.running_digest, a digest of the running configuration with
// its volatile lines removed, never the text. The digest is over the device's
// own encoded form, secrets included, so it shows someone else's change; and it
// is handed over as a raw digest (RawDigestField), which the engine
// keys under the project's key (or seals to the Controller) before anything
// keeps or shows it, since a plain digest of a short secret is a guessing
// oracle.
//
// The normalization is written down per platform here, and nowhere else: what
// a platform prints that changes on its own, with no one changing the device.
package netcfg

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// RawDigestField is the field a raw digest is handed over under. It is
// pkg/collection's RawDigestField, which this package cannot import
// (collection reaches netcfg through sdk), held equal to it by this package's
// tests.
const RawDigestField = "sha256_raw"

// RunningDigestFact is the fact's key, the same on every platform and
// protocol: the state lives in a protocol-agnostic domain (owner decision
// 2026-11-14).
const RunningDigestFact = "config.running_digest"

// The schemes a digest names, so two digests compare only when they were taken
// the same way: one platform's normalized text.
const (
	SchemeIOSText = "ios-cli-text/v1"
	SchemeFRRText = "frr-cli-text/v1"
)

// iosVolatile are the lines IOS and IOS XE print in show running-config that
// change with no configuration change: the build banner, the byte count, the
// last-change and NVRAM-save stamps, and the NTP clock period the device tunes
// itself.
var iosVolatile = []*regexp.Regexp{
	regexp.MustCompile(`^Building configuration\.\.\.$`),
	regexp.MustCompile(`^Current configuration ?: ?\d+ bytes$`),
	regexp.MustCompile(`^! Last configuration change at `),
	regexp.MustCompile(`^! NVRAM config last updated at `),
	regexp.MustCompile(`^! No configuration change since last restart`),
	regexp.MustCompile(`^ntp clock-period \d+$`),
}

// frrVolatile are the lines vtysh's show running-config prints around the
// configuration itself, which carry nothing a router was told.
var frrVolatile = []*regexp.Regexp{
	regexp.MustCompile(`^Building configuration\.\.\.$`),
	regexp.MustCompile(`^Current configuration:$`),
}

// NormalizeIOS is an IOS running configuration with its volatile lines removed,
// line endings made "\n" and every other carriage return dropped, each line's trailing space trimmed, and the blank
// lines before and after the configuration dropped.
func NormalizeIOS(config string) string { return normalize(config, iosVolatile) }

// NormalizeFRR is NormalizeIOS's rule for an FRR running configuration.
func NormalizeFRR(config string) string { return normalize(config, frrVolatile) }

// normalize applies one platform's volatile lines and the shared rules.
func normalize(config string, volatile []*regexp.Regexp) string {
	config = strings.ReplaceAll(config, "\r\n", "\n")
	// A carriage return left after that is a terminal's, never configuration:
	// no configuration line holds one (FuzzNormalize found a stray one).
	config = strings.ReplaceAll(config, "\r", "")
	var kept []string
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if isVolatile(line, volatile) {
			continue
		}
		kept = append(kept, line)
	}
	for len(kept) > 0 && kept[0] == "" {
		kept = kept[1:]
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// isVolatile reports whether line is one of a platform's volatile lines.
func isVolatile(line string, volatile []*regexp.Regexp) bool {
	for _, re := range volatile {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// RunningDigest is the fact value for a normalized configuration: its SHA-256
// as a raw digest the engine keys, the scheme it was taken under, and the
// datastore it was read from. It holds no size, since even a length is more
// than a secret-bearing configuration needs to give away.
func RunningDigest(normalized, scheme string) map[string]any {
	sum := sha256.Sum256([]byte(normalized))
	return map[string]any{
		RawDigestField: hex.EncodeToString(sum[:]),
		"scheme":       scheme,
		"datastore":    "running",
	}
}
