package filters

import (
	"regexp"
	"strconv"
	"strings"
)

// rfc5424VersionPattern matches VERSION SP immediately after PRI's
// closing '>', the one syntactic marker that distinguishes an RFC 5424
// message from a legacy RFC 3164 one: RFC 3164's PRI is followed
// directly by a month abbreviation ("Oct"), never by a bare digit and a
// space.
var rfc5424VersionPattern = regexp.MustCompile(`^([1-9][0-9]{0,2}) `)

// rfc3164TimestampPattern matches RFC 3164's legacy BSD syslog header --
// a 3-letter month abbreviation, a 1- or 2-digit day (however it is
// padded), and an hh:mm:ss clock, with no year (RFC 3164 never carries
// one).
var rfc3164TimestampPattern = regexp.MustCompile(`^([A-Z][a-z]{2})\s+([1-9]|[12][0-9]|3[01])\s+(\d{2}:\d{2}:\d{2}) `)

// SyslogParse parses one syslog line, RFC 5424 or legacy RFC 3164
// (BSD), into its component fields. Format detection and success both
// rest on the one part both formats share and neither can be loose
// about: PRI, the "<NNN>" prefix encoding facility*8+severity as an
// integer 0-191. Everything after PRI is parsed best-effort and
// degrades field by field rather than failing the whole line, since RFC
// 3164 in particular is a legacy, loosely followed convention in real-
// world logs; PRI is the one hard gate, exercised directly by this
// phase's own Fuzz/Stress Test requirement ("a syslog line with a
// malformed priority field").
//
// The returned map's keys are the same across both formats, so a
// runbook condition need not branch on which one it received:
// "format" ("rfc5424" or "rfc3164"), "facility" and "severity" (int),
// "version" (int; RFC 5424's own VERSION field, 0 for RFC 3164 which
// has none), "timestamp", "hostname", "app_name", "proc_id", "msg_id",
// "structured_data", and "message" (string). RFC 5424's own NILVALUE
// convention ("-" for an absent field) is passed through verbatim
// rather than translated to "": that literal hyphen is itself part of
// the wire format, and silently discarding it would be a normalization
// decision this parser does not make. RFC 3164 has no NILVALUE
// convention, so its own absent fields (hostname/app_name/proc_id/
// timestamp when the line has no recognizable header) come back as ""
// instead.
//
// STRUCTURED-DATA (RFC 5424 only) is returned as its raw, still-
// bracketed text (e.g. `[id@1 a="1"]`), not decoded into nested
// SD-PARAM key/value pairs: the checklist names RFC 5424 parsing, not a
// second, independent SD-PARAM grammar layered on top of it, and a
// runbook author who needs those pairs can still reach them with
// filters.regexExtract against this field.
//
// Returns nil if PRI is missing, non-numeric, out of the 0-191 range,
// or line exceeds MaxInputBytes -- a single syslog line is a flat
// scalar (RFC 5424 itself recommends implementations support at least
// 2048 octets), not the document-shaped payload MaxStructuredInputBytes
// exists for, so this reuses the flat-scalar cap.
func SyslogParse(line string) map[string]any {
	if len(line) > MaxInputBytes {
		return nil
	}
	facility, severity, rest, ok := parseSyslogPRI(line)
	if !ok {
		return nil
	}
	if m := rfc5424VersionPattern.FindStringSubmatch(rest); m != nil {
		// err is unreachable: rfc5424VersionPattern's own capture group
		// is "[1-9][0-9]{0,2}", 1 to 3 ASCII digits with no leading
		// zero, which strconv.Atoi always parses successfully (max
		// value 999, nowhere near overflowing Go's int on any platform
		// this project builds for). Kept as a real check rather than
		// asserted away, the same defensive-but-provably-unreachable
		// shape this Part's own established precedent (FormatCurrency's
		// big.Rat.SetString check, and others) already carries.
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		return parseRFC5424(rest[len(m[0]):], facility, severity, version)
	}
	return parseRFC3164(rest, facility, severity)
}

// parseSyslogPRI splits "<NNN>REST" into facility (NNN/8), severity
// (NNN%8), and REST. ok is false unless line begins with "<", is
// followed by 1 to 3 ASCII digits and then ">", and that number falls
// in 0-191, the full facility*8+severity range PRI can ever legally
// hold.
func parseSyslogPRI(line string) (facility, severity int, rest string, ok bool) {
	if len(line) < 3 || line[0] != '<' {
		return 0, 0, "", false
	}
	end := strings.IndexByte(line, '>')
	if end < 2 || end > 4 { // "<0>" .. "<191>": 1 to 3 digits between '<' and '>'
		return 0, 0, "", false
	}
	pri, err := strconv.Atoi(line[1:end])
	if err != nil || pri < 0 || pri > 191 {
		return 0, 0, "", false
	}
	return pri / 8, pri % 8, line[end+1:], true
}

// parseRFC5424 parses everything after "<PRI>VERSION " into an RFC 5424
// message; rest begins at TIMESTAMP.
func parseRFC5424(rest string, facility, severity, version int) map[string]any {
	fields := strings.SplitN(rest, " ", 5)
	for len(fields) < 5 {
		fields = append(fields, "")
	}
	timestamp, hostname, appName, procID, tail := fields[0], fields[1], fields[2], fields[3], fields[4]

	msgID, tail, _ := strings.Cut(tail, " ")
	sd, message, ok := scanStructuredData(tail)
	if !ok {
		sd, message = "", tail
	}

	return map[string]any{
		"format":          "rfc5424",
		"facility":        facility,
		"severity":        severity,
		"version":         version,
		"timestamp":       timestamp,
		"hostname":        hostname,
		"app_name":        appName,
		"proc_id":         procID,
		"msg_id":          msgID,
		"structured_data": sd,
		"message":         message,
	}
}

// scanStructuredData scans s for RFC 5424's STRUCTURED-DATA field: "-"
// (NILVALUE) or one or more bracketed "[SD-ID (SP SD-PARAM)*]" elements
// back to back. It tracks whether it is inside an SD-PARAM's quoted
// value so an escaped or literal "]" inside that value never ends the
// element early -- RFC 5424 Section 6.3.3's own PARAM-VALUE grammar
// allows any UTF-8 character in a quoted value provided '"', '\' and
// ']' are backslash-escaped. rest is everything after STRUCTURED-DATA
// and its one separating space, if any. ok is false only for an
// unterminated bracket (real syslog output never emits one); this
// function never loops unboundedly either way, since it scans s exactly
// once, bounded by SyslogParse's own MaxInputBytes check on the whole
// line.
func scanStructuredData(s string) (sd, rest string, ok bool) {
	if s == "" {
		return "", "", true
	}
	if s[0] == '-' {
		return "-", strings.TrimPrefix(s[1:], " "), true
	}
	if s[0] != '[' {
		return "", s, true
	}
	depth := 0
	inQuotes := false
	escaped := false
	end := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			if inQuotes {
				escaped = true
			}
		case '"':
			inQuotes = !inQuotes
		case '[':
			if !inQuotes {
				depth++
			}
		case ']':
			if !inQuotes {
				depth--
			}
		}
		if depth == 0 {
			end = i + 1
			// The run of SD elements has closed, but another element
			// may follow immediately with no separating space; only
			// continuing the scan can tell.
			if end >= len(s) || s[end] != '[' {
				break
			}
		}
	}
	if depth != 0 || end < 0 {
		return "", "", false
	}
	return s[:end], strings.TrimPrefix(s[end:], " "), true
}

// parseRFC3164 parses everything after "<PRI>" into a legacy BSD
// syslog message. It recognizes the classic "Mmm dd hh:mm:ss HOSTNAME
// TAG[PID]: MSG" header; a rest that does not start with that timestamp
// shape (RFC 3164 is a legacy, loosely followed convention, and real-
// world lines vary widely) still parses, with timestamp/hostname/
// app_name/proc_id left "" and message set to rest as a whole, rather
// than failing the line -- only PRI is a hard gate for this function.
func parseRFC3164(rest string, facility, severity int) map[string]any {
	m := rfc3164TimestampPattern.FindStringSubmatch(rest)
	if m == nil {
		return map[string]any{
			"format": "rfc3164", "facility": facility, "severity": severity,
			"version": 0, "timestamp": "", "hostname": "", "app_name": "",
			"proc_id": "", "msg_id": "", "structured_data": "",
			"message": rest,
		}
	}
	// Reconstructed with single-space separators regardless of RFC
	// 3164's traditional space-padding for a single-digit day
	// ("Oct  3"): a canonical, deterministic form, the same preference
	// SymbolicToOctalPerms's own doc comment states for its own
	// canonical output.
	timestamp := m[1] + " " + m[2] + " " + m[3]
	hostname, tail, _ := strings.Cut(rest[len(m[0]):], " ")
	tag, message := splitRFC3164Tag(tail)
	appName, procID := splitTagPID(tag)

	return map[string]any{
		"format": "rfc3164", "facility": facility, "severity": severity,
		"version": 0, "timestamp": timestamp, "hostname": hostname,
		"app_name": appName, "proc_id": procID, "msg_id": "",
		"structured_data": "", "message": message,
	}
}

// splitRFC3164Tag splits "TAG[PID]: MSG" or "TAG: MSG" into tag (with
// its own optional "[PID]" suffix still attached) and message. If no
// ": " separator is found, the whole string is treated as message with
// no tag, matching this function's own graceful-degrade policy for
// anything RFC 3164 does not strictly enforce in practice.
func splitRFC3164Tag(s string) (tag, message string) {
	i := strings.Index(s, ": ")
	if i < 0 {
		return "", s
	}
	return s[:i], s[i+2:]
}

// splitTagPID splits a TAG that may carry a trailing "[PID]" (e.g.
// "sshd[1234]") into the bare app name and the PID string, "" for
// either half not present.
func splitTagPID(tag string) (appName, procID string) {
	if i := strings.IndexByte(tag, '['); i >= 0 && strings.HasSuffix(tag, "]") {
		return tag[:i], tag[i+1 : len(tag)-1]
	}
	return tag, ""
}

// LineEndingConvert normalizes every line ending in content (a bare
// "\r", a bare "\n", or "\r\n") to the single style requested: "lf" for
// a bare "\n", or "crlf" for "\r\n", matched case-insensitively. Every
// line ending is normalized to "\n" first regardless of which one the
// source used, then rewritten to the requested style, so mixed line
// endings within one input never survive into the result.
//
// Returns "" for an unrecognized style, or for content exceeding
// MaxStructuredInputBytes -- reused rather than MaxInputBytes, since
// content here is arbitrary file text (a config file, captured command
// output), not a flat scalar like an FQDN, the same document-shaped
// reasoning MaxStructuredInputBytes' own doc comment states.
func LineEndingConvert(content, style string) string {
	if len(content) > MaxStructuredInputBytes {
		return ""
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(content)
	switch strings.ToLower(style) {
	case "lf":
		return normalized
	case "crlf":
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	default:
		return ""
	}
}

// TrimNormalizeWhitespace collapses every run of Unicode whitespace
// (spaces, tabs, newlines -- unicode.IsSpace's own definition, via
// strings.Fields) into a single ASCII space, and trims the result's
// leading and trailing space. Reuses MaxStructuredInputBytes rather
// than MaxInputBytes for the same document-shaped reason
// LineEndingConvert does: this is meant to run over captured command
// output or file content, not a flat scalar.
func TrimNormalizeWhitespace(s string) string {
	if len(s) > MaxStructuredInputBytes {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

// PayloadChunker splits items into consecutive chunks of at most size
// elements each, preserving order (the final chunk may hold fewer than
// size elements when len(items) is not an exact multiple of it). Each
// element of the returned list is itself a []any chunk -- CEL's own dyn
// type represents a nested list natively, so this needs no new
// well-known shape beyond []any, the same one Pluck/ListDiff/
// ListContains already use, verified directly against a real
// cel.Program (a nested-list round trip through
// types.NewDynamicList/celToAny) before relying on it here rather than
// assumed from those functions' own single-level use.
//
// No length cap on items: a CEL list argument's construction cost is
// already reflected in the engine's own per-element cost accounting
// (unlike a flat string argument, whose byte length is invisible to
// it), the same reasoning every other list-taking filter in this
// package (ListDiff, ListIntersect, FilterListByKV) already relies on
// implicitly by carrying no MaxInputBytes-style check of their own.
//
// Returns nil (not an empty, non-nil list) if size is not positive: a
// zero or negative chunk size is malformed control input, this
// package's own sentinel-return convention for that case, distinct from
// a legitimately empty items list (which yields a real, empty, non-nil
// result).
func PayloadChunker(items []any, size int) []any {
	if size <= 0 {
		return nil
	}
	chunks := make([]any, 0, (len(items)+size-1)/size)
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		chunk := make([]any, end-i)
		copy(chunk, items[i:end])
		chunks = append(chunks, chunk)
	}
	return chunks
}
