package filters

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// SHA256Hash returns the lowercase hex-encoded SHA-256 digest of s, or ""
// if s exceeds MaxStructuredInputBytes. Treated as document-shaped, not a
// flat scalar: a real hash target (a rendered script, a config file's
// contents) routinely exceeds a flat-scalar length, the same reasoning
// Phase 54's IsValidBase64/IsValidJSON/IsValidYAML already state for
// their own document-shaped inputs. Hashing itself cannot fail; the
// length cap exists only because a custom cel.Function carries no
// registered cost estimator, so nothing else bounds a single call's cost
// against an attacker-sized argument (pkg/filters' own package doc
// comment).
func SHA256Hash(s string) string {
	if len(s) > MaxStructuredInputBytes {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HMACGenerate returns the lowercase hex-encoded HMAC-SHA256 of message
// using key, or "" if message exceeds MaxStructuredInputBytes or key
// exceeds MaxInputBytes (a shared secret is realistically short; a
// message being authenticated, like SHA256Hash's own target, is not).
// Fixed to SHA-256 rather than taking an algorithm-name argument: an
// algorithm-selection parameter is a footgun a filter has no business
// offering (a caller could pick a weak digest without realizing it), and
// SHA-256 is the same digest SHA256Hash itself uses, so the two stay
// consistent.
func HMACGenerate(message, key string) string {
	if len(message) > MaxStructuredInputBytes || len(key) > MaxInputBytes {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// SecureCompare reports whether a and b are equal, in constant time
// regardless of where they first differ, via the stdlib's own
// crypto/subtle.ConstantTimeCompare -- the entire reason this function
// exists over a plain a == b, which Go's own runtime is free to
// short-circuit at the first differing byte.
//
// The length cap below runs before ConstantTimeCompare and is not itself
// a timing side channel: MaxInputBytes is a fixed public constant,
// unrelated to either argument's actual content, so an attacker
// measuring how quickly the cap rejects an oversized guess learns
// nothing about which of the secret's own bytes matched. This is stated
// explicitly because it is exactly the kind of question this phase's own
// Schema/Injection Hardening item asks to be audited, not assumed.
func SecureCompare(a, b string) bool {
	if len(a) > MaxInputBytes || len(b) > MaxInputBytes {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// randomPasswordCharset is GenerateRandomPassword's own character set:
// upper- and lower-case letters and digits with the visually ambiguous
// ones removed (0/O, 1/l/I), plus a small set of symbols chosen to be
// safe to embed directly in a shell command or YAML scalar without
// quoting rules of their own (no backtick, dollar sign, quote, or
// backslash, all of which have special meaning in at least one of those
// two contexts).
const randomPasswordCharset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%^&*-_=+"

// maxRandomPasswordLength bounds GenerateRandomPassword the same way
// every other length-driven allocation in this package is bounded (see
// pkg/filters/network.go's maxSubnetSplitCount): generous for any real
// password policy, small enough that a pathological length argument
// cannot force an unbounded allocation.
const maxRandomPasswordLength = 1024

// GenerateRandomPassword returns a cryptographically random password of
// the given length drawn from randomPasswordCharset, or "" if length is
// not in (0, maxRandomPasswordLength]. Built on crypto/rand, never
// math/rand, per this phase's own checklist requirement: math/rand's
// output is predictable from its seed, which makes it unsafe for
// anything used as a credential. Each character is selected via
// crypto/rand.Int against the charset's own length, not a byte modulo
// the charset length, which would introduce modulo bias favoring the
// characters at the low end of the byte range.
func GenerateRandomPassword(length int) string {
	if length <= 0 || length > maxRandomPasswordLength {
		return ""
	}
	charsetLen := big.NewInt(int64(len(randomPasswordCharset)))
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			// crypto/rand.Reader reading from the OS CSPRNG does not fail
			// on any real, supported platform; this exists only because
			// Go's own API returns an error and every returned error in
			// this codebase is checked, never dropped. Failing closed (no
			// partial, lower-entropy password) rather than continuing is
			// the only defensible response if it somehow did.
			return ""
		}
		out[i] = randomPasswordCharset[n.Int64()]
	}
	return string(out)
}

// maskPIIOversizedMarker is what MaskPII returns in place of an
// oversized input, instead of either the unredacted original text or an
// empty string. A redaction function's failure mode matters more than
// an ordinary parser's: silently passing oversized input through
// unmasked could leak the exact PII this function exists to catch, and
// silently returning "" looks like "there was nothing here" rather than
// "redaction was skipped." Over-redacting the whole input is the one
// failure mode that can never leak anything and can never be mistaken
// for a clean result.
const maskPIIOversizedMarker = "[REDACTED-OVERSIZED-INPUT]"

// ssnPattern, creditCardPattern and bearerTokenPattern are MaskPII's
// curated default pattern set. Documented, deliberate scope, not an
// oversight: ssnPattern matches only the canonical NNN-NN-NNNN grouping
// (not nine bare digits, which would false-positive on every other
// nine-digit number in a log line); creditCardPattern matches the two
// dominant real-world display groupings (16 digits as 4-4-4-4, and
// American Express's 15 digits as 4-6-5), not every possible 13-to-19-
// digit run: no Luhn check, so a random 16-digit number redacts too,
// which is the intended direction for a best-effort tool to err in;
// bearerTokenPattern matches an RFC 6750 Authorization header value
// case-insensitively and keeps the "Bearer " prefix visible, masking
// only the token itself, since the prefix is what makes the redacted
// output still readable as "a token was here."
var (
	ssnPattern         = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	creditCardPattern  = regexp.MustCompile(`\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{4}\b|\b\d{4}[ -]?\d{6}[ -]?\d{5}\b`)
	bearerTokenPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]+=*`)
)

// MaskPII redacts SSN-, credit-card-, and bearer-token-shaped
// substrings from s, replacing each with a fixed marker naming what was
// found. Documented as best-effort, not a compliance guarantee (PLAN.md
// Section 36's own checklist wording): the pattern set above is curated
// and will neither catch every real-world PII shape nor guarantee zero
// false positives. Returns maskPIIOversizedMarker, not s itself and not
// "", if s exceeds MaxStructuredInputBytes (document-shaped: a redaction
// target is realistically a whole log excerpt, not a short scalar).
func MaskPII(s string) string {
	if len(s) > MaxStructuredInputBytes {
		return maskPIIOversizedMarker
	}
	s = ssnPattern.ReplaceAllString(s, "[REDACTED-SSN]")
	s = creditCardPattern.ReplaceAllString(s, "[REDACTED-CC]")
	s = bearerTokenPattern.ReplaceAllString(s, "Bearer [REDACTED-TOKEN]")
	return s
}

// maxWindowsSIDSubAuthorities bounds how many sub-authorities
// WindowsSIDToHex will encode: a real Windows SID never carries more
// than 15, and the binary format's own sub-authority-count byte can only
// represent up to 255 regardless, so this is a defensive bound against a
// pathological input, not a realistic one.
const maxWindowsSIDSubAuthorities = 255

// WindowsSIDToHex converts a Windows SID string (e.g.
// "S-1-5-21-3623811015-3361044348-30300820-1013") to its binary form per
// MS-DTYP's SID structure -- a revision byte, a sub-authority-count
// byte, a 6-byte big-endian identifier authority, then each
// sub-authority as a 4-byte little-endian uint32 -- hex-encoded. Returns
// "" for anything that does not parse as that exact shape.
func WindowsSIDToHex(sid string) string {
	if len(sid) > MaxInputBytes {
		return ""
	}
	parts := strings.Split(sid, "-")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "S") {
		return ""
	}
	revision, err := strconv.ParseUint(parts[1], 10, 8)
	if err != nil {
		return ""
	}
	authority, err := strconv.ParseUint(parts[2], 10, 48)
	if err != nil {
		return ""
	}
	subAuths := parts[3:]
	if len(subAuths) > maxWindowsSIDSubAuthorities {
		return ""
	}

	buf := make([]byte, 8+4*len(subAuths))
	buf[0] = byte(revision)
	// len(subAuths) is already bounds-checked above against
	// maxWindowsSIDSubAuthorities (255), so this narrowing is safe by
	// construction; masked explicitly to say so, not just to satisfy a
	// linter.
	buf[1] = byte(len(subAuths) & 0xff)
	for i := 0; i < 6; i++ {
		// Extracting one byte at a time from a wider integer via shift-
		// then-truncate is the correct, intentional pattern here, not an
		// accidental narrowing: masked explicitly for the same reason.
		buf[7-i] = byte((authority >> (8 * i)) & 0xff)
	}
	for i, sa := range subAuths {
		v, err := strconv.ParseUint(sa, 10, 32)
		if err != nil {
			return ""
		}
		binary.LittleEndian.PutUint32(buf[8+4*i:], uint32(v))
	}
	return hex.EncodeToString(buf)
}

// HexToWindowsSID converts a hex-encoded binary Windows SID back to its
// string form, the inverse of WindowsSIDToHex. Returns "" for anything
// that does not decode to a structurally valid SID (wrong length for
// its own declared sub-authority count, in particular).
func HexToWindowsSID(hexSID string) string {
	if len(hexSID) > MaxInputBytes {
		return ""
	}
	buf, err := hex.DecodeString(strings.TrimSpace(hexSID))
	if err != nil || len(buf) < 8 {
		return ""
	}
	revision := buf[0]
	subCount := int(buf[1])
	if len(buf) != 8+4*subCount {
		return ""
	}

	var authority uint64
	for i := 0; i < 6; i++ {
		authority = authority<<8 | uint64(buf[2+i])
	}

	parts := make([]string, 0, 3+subCount)
	parts = append(parts, "S", strconv.FormatUint(uint64(revision), 10), strconv.FormatUint(authority, 10))
	for i := 0; i < subCount; i++ {
		v := binary.LittleEndian.Uint32(buf[8+4*i:])
		parts = append(parts, strconv.FormatUint(uint64(v), 10))
	}
	return strings.Join(parts, "-")
}

// snmpMIB2Names is SNMPOIDTranslate's own curated table: the standard
// MIB-II system (1.3.6.1.2.1.1) and interfaces (1.3.6.1.2.1.2) groups
// only, per this phase's own explicit scope limit -- a comprehensive
// vendor MIB database is a data-curation problem on the scale of its own
// product, not a function this phase attempts.
var snmpMIB2Names = map[string]string{
	"1.3.6.1.2.1.1.1.0":    "sysDescr.0",
	"1.3.6.1.2.1.1.2.0":    "sysObjectID.0",
	"1.3.6.1.2.1.1.3.0":    "sysUpTime.0",
	"1.3.6.1.2.1.1.4.0":    "sysContact.0",
	"1.3.6.1.2.1.1.5.0":    "sysName.0",
	"1.3.6.1.2.1.1.6.0":    "sysLocation.0",
	"1.3.6.1.2.1.1.7.0":    "sysServices.0",
	"1.3.6.1.2.1.2.1.0":    "ifNumber.0",
	"1.3.6.1.2.1.2.2.1.1":  "ifIndex",
	"1.3.6.1.2.1.2.2.1.2":  "ifDescr",
	"1.3.6.1.2.1.2.2.1.3":  "ifType",
	"1.3.6.1.2.1.2.2.1.4":  "ifMtu",
	"1.3.6.1.2.1.2.2.1.5":  "ifSpeed",
	"1.3.6.1.2.1.2.2.1.6":  "ifPhysAddress",
	"1.3.6.1.2.1.2.2.1.7":  "ifAdminStatus",
	"1.3.6.1.2.1.2.2.1.8":  "ifOperStatus",
	"1.3.6.1.2.1.2.2.1.10": "ifInOctets",
	"1.3.6.1.2.1.2.2.1.16": "ifOutOctets",
}

// SNMPOIDTranslate translates oid to its symbolic name if it names a
// standard MIB-II system or interfaces group object, or "" otherwise.
func SNMPOIDTranslate(oid string) string {
	if len(oid) > MaxInputBytes {
		return ""
	}
	return snmpMIB2Names[strings.TrimSpace(oid)]
}
