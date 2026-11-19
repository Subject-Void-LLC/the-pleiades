// Package facts: the files subset (Phase 41c). For each path a task names in
// files, one fact, file.<path> = {state, type, size, uid, gid, owner, group,
// mode, sha256, link_target, readable}: a digest and metadata, never the
// content. A path is absent only when the system says it does not exist; one
// the login cannot reach (a directory it may not search, a file Windows will
// not open) is unreadable, never absent, since "gone" and "hidden from me" are
// different answers and drift data must not confuse them.
//
// This is the one subset whose command holds what the task wrote, so every
// path is single-quoted (remoteexec.QuoteArg) on a POSIX device and carried
// as base64 JSON on Windows, and the device never echoes a path back: each
// answer is matched to its path by position, so a path holding a newline or
// a marker cannot shift another's answer.
package facts

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gatherParamFiles names the paths the files subset reads.
const gatherParamFiles = "files"

// gatherMaxFiles bounds how many paths one task may name, so a task cannot
// turn a fact gather into a crawl of the file system.
const gatherMaxFiles = 64

// fileSectionMark begins each path's answer.
const fileSectionMark = "--pleiades-file--"

// The states a path can be in.
const (
	fileStatePresent    = "present"
	fileStateAbsent     = "absent"
	fileStateUnreadable = "unreadable"
)

// gatherFilePaths reads and checks files: at most gatherMaxFiles paths, each
// absolute (/ on a POSIX device, a drive letter on Windows, which
// gatherFilesFor holds to the platform once the device names it), valid
// UTF-8, holding no NUL, not starting with two separators of either kind
// (\\server\share, //server/share and \\?\ reach another machine or the
// device namespace on Windows, which reads / as \), and short enough that
// its fact key fits the store.
func gatherFilePaths(params map[string]any) ([]string, error) {
	paths, _, err := sdk.StringSlice(params, gatherParamFiles)
	if err != nil {
		return nil, err
	}
	if len(paths) > gatherMaxFiles {
		return nil, fmt.Errorf("%s names %d paths; one task may name at most %d", gatherParamFiles, len(paths), gatherMaxFiles)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		switch {
		case !utf8.ValidString(p) || strings.ContainsRune(p, 0):
			return nil, fmt.Errorf("%s path %q holds a NUL or is not UTF-8", gatherParamFiles, p)
		case len(p) >= 2 && strings.ContainsRune(`/\`, rune(p[0])) && strings.ContainsRune(`/\`, rune(p[1])):
			return nil, fmt.Errorf("%s path %q starts with two separators, which names another machine or a device on Windows", gatherParamFiles, p)
		case !strings.HasPrefix(p, "/") && !windowsAbsolute(p):
			return nil, fmt.Errorf("%s path %q is not absolute (/... on a POSIX device, C:\\... on Windows)", gatherParamFiles, p)
		case windowsAbsolute(p) && windowsDeviceName(p):
			return nil, fmt.Errorf("%s path %q names a Windows device (CON, NUL, COM1 and the like), not a file", gatherParamFiles, p)
		case len(itemKey(gatherFamilyFile, p)) > collection.MaxFactKeyBytes:
			return nil, fmt.Errorf("%s path %q is too long to name a fact (%d bytes once encoded, at most %d)", gatherParamFiles, p, len(itemKey(gatherFamilyFile, p)), collection.MaxFactKeyBytes)
		case seen[p]:
			return nil, fmt.Errorf("%s names %q twice", gatherParamFiles, p)
		}
		seen[p] = true
	}
	return paths, nil
}

// windowsAbsolute reports whether p is a drive-rooted Windows path (C:\...
// or C:/...). A UNC path (\\server\share) is not one: it would reach another
// machine through this one.
func windowsAbsolute(p string) bool {
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// windowsDeviceNames are the names Windows reads as a device in any
// directory and with any extension: reading one would open a console, a
// serial port or the null device, not a file.
var windowsDeviceNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

// windowsDeviceName reports whether p's last element, before any extension
// and trailing spaces or dots, is a Windows device name.
func windowsDeviceName(p string) bool {
	base := p[strings.LastIndexAny(p, `/\`)+1:]
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	if windowsDeviceNames[base] {
		return true
	}
	// COM0 to COM9 and LPT0 to LPT9, the superscript digits too.
	for _, prefix := range []string{"COM", "LPT"} {
		if rest, ok := strings.CutPrefix(base, prefix); ok && (len(rest) == 1 && rest[0] >= '0' && rest[0] <= '9' || rest == "\u00b9" || rest == "\u00b2" || rest == "\u00b3") {
			return true
		}
	}
	return false
}

// gatherFilesFor holds paths to platform's own absolute form, now that the
// device has named it: a drive-rooted path on Windows, / anywhere else. A
// path written for the other kind of system would be read relative to the
// login's home, or not at all.
func gatherFilesFor(paths []string, platform string) error {
	for _, p := range paths {
		if (platform == platformWindows) != windowsAbsolute(p) {
			return fmt.Errorf("%s path %q is not an absolute path on %s", gatherParamFiles, p, platform)
		}
	}
	return nil
}

// posixFileProbe is the files probe of a POSIX platform: statFormat is its
// stat's options and format, printing type, size, uid, gid, mode, owner and
// group separated by colons (which an account name cannot hold), and
// modeFormat the format printing a mode alone.
//
// # A plain digest only of what anyone may read
//
// A stored digest is read by anyone who may read the device's facts, and
// the SHA-256 of a small secret (a password or key file) can be guessed
// offline. So a regular file keeps a plain sha256 only when it is
// world-readable: its mode lets others read it and every directory above it
// lets others search it. A directory above it that is a symbolic link makes it
// private (owner decision 2026-11-19): the walk reads the path as written, so
// a link leads to directories it never reads, any of which may keep others
// out, and a plain digest there was an oracle for a file only this login
// could reach. Any other file's digest is handed over as
// sha256_raw, which the engine keys or seals before anything keeps or shows it
// (internal/facts' keyed digests), and its size (a secret's length) is not
// kept. The test runs on the device, in sh arithmetic over stat's octal
// modes, each checked to be octal first so a bad answer counts as private
// rather than ends the shell.
func posixFileProbe(f posixStat) gatherProbe {
	statFormat, modeFormat := f.stat, f.mode
	return gatherProbe{
		subset: gatherSubsetFiles,
		family: gatherFamilyFile,
		commandFor: func(paths []string) string {
			quoted := make([]string, len(paths))
			for i, p := range paths {
				quoted[i] = remoteexec.QuoteArg(p)
			}
			// stat reads the path itself (a link, not what it names). The
			// digest is only of a regular file this login can read, read
			// from stdin so no tool prints the path; a link's target is
			// printed as hex, so it cannot break a line. An error that ends
			// in "No such file or directory" or "Not a directory" (the
			// errno text, which comes last whatever the path holds) is
			// absent; every other error is unreadable.
			return posixACLRestricts + posixAttachedListing + `for p in ` + strings.Join(quoted, " ") + `; do echo '` + fileSectionMark + `'; ` +
				`if e=$(LC_ALL=C stat ` + statFormat + ` -- "$p" 2>&1); then printf 'ok:%s\n' "$e"; ` +
				`if [ -f "$p" ] && [ ! -L "$p" ]; then w=1; q=$p; m=$(LC_ALL=C stat ` + modeFormat + ` -- "$p" 2>/dev/null); i=$(LC_ALL=C stat ` + f.id + ` -- "$p" 2>/dev/null); ` +
				`case $i in *:*:1) ;; *) w=0;; esac; ` +
				`case $m in ''|*[!0-7]*) w=0;; *) [ $((0$m & 4)) -ne 0 ] || w=0;; esac; if [ $w = 1 ] && ar "$p" r; then w=0; fi; ` +
				`while [ $w = 1 ]; do q=${q%/*}; [ -n "$q" ] || break; if [ -L "$q" ]; then w=0; break; fi; a=$(LC_ALL=C stat -L ` + modeFormat + ` -- "$q" 2>/dev/null); ` +
				`case $a in ''|*[!0-7]*) w=0;; *) [ $((0$a & 1)) -ne 0 ] || w=0;; esac; if [ $w = 1 ] && ar "$q" x; then w=0; fi; done; ` +
				`if [ $w = 1 ]; then t=sha; else t=raw; fi; if [ -r "$p" ]; then { [ "$(` + f.fd + `)" = "${i%:*}" ] || t=raw; printf '%s:%s\n' "$t" "$(` + posixSHA256 + ` <&3)"; } 3<"$p" 2>/dev/null || echo "$t-unreadable"; else echo "$t-unreadable"; fi; fi; ` +
				`if [ -L "$p" ]; then printf 'link:%s\n' "$(readlink -- "$p" | od -An -tx1 | tr -d ' \n')"; else ax "$p"; fi; ` +
				`else case $e in *': No such file or directory'|*': Not a directory') echo absent;; *) echo unreadable;; esac; fi; done`
		},
		parse: parseFiles,
	}
}

// posixACLRestricts defines ar PATH PERM, which succeeds when an access
// control list on PATH denies anyone, or withholds PERM (r for the file, x for
// each directory above it) from a user or group it names: such a file is not
// readable by everyone, whatever its mode says (owner decision 2026-11-19).
// Every way of reading it fails closed: a listing or getfacl that fails is a
// restriction. On macOS an entry is a line after the path's own in ls -le
// ("0: group:everyone deny read", measured on macOS 27.2.1), and any deny
// restricts. Elsewhere getfacl reads it: an NFSv4 entry ending in :deny
// restricts and one ending in :allow does not, and a POSIX.1e entry naming a
// user or group (user:bob:---), or the mask, restricts when its permissions,
// the effective ones where getfacl prints them, lack PERM. With no getfacl, a
// GNU ls that marks an ACL with + after the mode restricts. BusyBox's ls marks
// none, so on a BusyBox system without getfacl an ACL is not seen.
const posixACLRestricts = `o=$(uname -s 2>/dev/null); ar(){ ` +
	`if [ "$o" = Darwin ]; then l=$(LC_ALL=C ls -lde -- "$1" 2>/dev/null) || return 0; ` +
	`printf '%s\n' "$l" | { read -r _; while IFS= read -r e; do case $e in *' deny '*) exit 3;; esac; done; }; [ $? = 3 ] && return 0; return 1; fi; ` +
	`if command -v getfacl >/dev/null 2>&1; then l=$(LC_ALL=C getfacl -- "$1" 2>/dev/null) || return 0; ` +
	`printf '%s\n' "$l" | while IFS= read -r e; do case $e in '#'*|'') ;; *:deny) exit 3;; *:allow) ;; ` +
	`user:?*:*|group:?*:*|mask:*) case $e in *'#effective:'*) e=${e##*#effective:};; *) e=${e##*:};; esac; e=${e%%[	 ]*}; ` +
	`case $e in *"$2"*) ;; *) exit 3;; esac;; esac; done; [ $? = 3 ] && return 0; return 1; fi; ` +
	`case $(LC_ALL=C ls -ld -- "$1" 2>/dev/null) in ??????????+*) return 0;; esac; return 1; }; `

// posixAttachedListing defines ax PATH, which prints what PATH carries beside
// its content (owner decision 2026-11-19): its extended attributes, among them
// a resource fork on macOS. One line says what could be read: xs:unlisted when
// the device has no tool to list them, xs:unreadable when listing failed,
// else xn: the names as the tool listed them, hex-encoded so no name can break
// a line, and xd: one SHA-256 over every name and value, the listing piped
// straight into the digest so no value, however large, crosses the wire or
// sits in a shell variable. Measured formats: macOS 27.2.1's xattr lists one
// name a line and xattr -lx prints each with its value in hex; Linux's getfattr
// -d -m - -e hex (the attr package) prints name=0x... under a "# file:" line;
// FreeBSD's lsextattr -q user lists the user namespace's names, and
// getextattr -qx each value. A failed listing never makes a file private: what
// it carries beside its content is not who may read it.
const posixAttachedListing = `ax(){ ` +
	`if [ "$o" = Darwin ]; then n=$(xattr -- "$1" 2>/dev/null) || { echo xs:unreadable; return; }; ` +
	`d=$(xattr -lx -- "$1" 2>/dev/null | { ` + posixSHA256 + `; }); ` +
	`elif [ "$o" = FreeBSD ]; then n=$(lsextattr -q user -- "$1" 2>/dev/null) || { echo xs:unreadable; return; }; ` +
	`d=$(for a in $n; do printf '%s\n' "$a"; getextattr -qx user "$a" "$1" 2>/dev/null; done | { ` + posixSHA256 + `; }); ` +
	`elif command -v getfattr >/dev/null 2>&1; then l=$(getfattr -m - -- "$1" 2>/dev/null) || { echo xs:unreadable; return; }; n=$(printf '%s\n' "$l" | grep -v '^#'); ` +
	`d=$(getfattr -d -m - -e hex -- "$1" 2>/dev/null | grep -v '^#' | { ` + posixSHA256 + `; }); ` +
	`else echo xs:unlisted; return; fi; ` +
	`printf 'xn:%s\n' "$(printf '%s' "$n" | od -An -tx1 | tr -d ' \n')"; printf 'xd:%s\n' "$d"; }; `

// posixSHA256 hashes stdin with whichever SHA-256 tool the device has:
// sha256sum (GNU, BusyBox, FreeBSD 15, macOS 27), FreeBSD's sha256 -q, or
// shasum on an older Mac. Each prints the hex digest first.
const posixSHA256 = `if command -v sha256sum >/dev/null 2>&1; then sha256sum; elif command -v sha256 >/dev/null 2>&1; then sha256 -q; else shasum -a 256; fi`

// posixStat is one platform's stat formats for the files probe: stat, printing
// type, size, uid, gid, mode, owner and group; mode, the mode alone; id, the
// file's device, inode and link count; and fd, a command printing the device
// and inode of what descriptor 3 holds open.
//
// id and fd close two ways round the world-readable walk, which reads the
// path as written. A file with more than one link may also sit in a directory
// that keeps others out, which no walk of this path sees, so it is private
// (the hard link's twin of a linked directory). And the file is opened once,
// the digest read from that descriptor, and the descriptor must hold the
// file the walk judged: a path swapped between the check and the read (a
// rename by someone who may write a directory on it) hands its digest over
// raw rather than plain.
type posixStat struct{ stat, mode, id, fd string }

// The two platforms' formats: GNU and BusyBox stat -c, reading the open
// descriptor through /proc/self/fd, and BSD stat -f (FreeBSD and macOS), whose
// %Mp%Lp is the four-digit mode with setuid, setgid and sticky and which,
// given no file, reads its standard input.
var (
	gnuStat = posixStat{stat: statFormatGNU, mode: modeFormatGNU, id: `-c '%d:%i:%h'`, fd: `LC_ALL=C stat -L -c '%d:%i' /proc/self/fd/3 2>/dev/null`}
	bsdStat = posixStat{stat: statFormatBSD, mode: modeFormatBSD, id: `-f '%d:%i:%l'`, fd: `LC_ALL=C stat -f '%d:%i' <&3 2>/dev/null`}
)

// The stat formats: GNU and BusyBox stat -c, and BSD stat -f (FreeBSD and
// macOS), whose %Mp%Lp is the four-digit mode with setuid, setgid and sticky.
const (
	statFormatGNU = `-c '%F:%s:%u:%g:%a:%U:%G'`
	statFormatBSD = `-f '%HT:%z:%u:%g:%Mp%Lp:%Su:%Sg'`
	modeFormatGNU = `-c '%a'`
	modeFormatBSD = `-f '%Mp%Lp'`
)

// fileTypes maps each stat's word for a file's type (GNU's lower case, BSD's
// title case) to one vocabulary.
var fileTypes = map[string]string{
	"regular file": "file", "regular empty file": "file", "Regular File": "file",
	"directory": "directory", "Directory": "directory",
	"symbolic link": "link", "Symbolic Link": "link",
	"fifo": "fifo", "Fifo File": "fifo",
	"socket": "socket", "Socket": "socket",
	"character special file": "char", "Character Device": "char",
	"block special file": "block", "Block Device": "block",
}

// parseFiles reads posixFileProbe's answers, one section per path in the
// order the task named them. The keys are assigned by Gather (filesFacts),
// which knows the paths; this returns one value per section.
func parseFiles(out string) []gatherFact {
	sections := strings.Split(out, fileSectionMark)
	if len(sections) < 2 {
		return nil
	}
	facts := make([]gatherFact, 0, len(sections)-1)
	for _, section := range sections[1:] {
		facts = append(facts, gatherFact{value: parseFileSection(section)})
	}
	return facts
}

// parseFileSection reads one path's answer: absent, unreadable, or ok: with
// stat's seven fields, then its digest and link target when it has them.
func parseFileSection(section string) map[string]any {
	value := map[string]any{"state": fileStateUnreadable}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == fileStateAbsent:
			value["state"] = fileStateAbsent
		case strings.HasPrefix(line, "ok:"):
			fields := strings.Split(strings.TrimPrefix(line, "ok:"), ":")
			if len(fields) < 7 {
				continue
			}
			// Every number must read as one, or the line is not stat's
			// answer and the path stays unreadable.
			kind, known := fileTypes[fields[0]]
			size, err0 := strconv.ParseUint(fields[1], 10, 63)
			uid, err1 := strconv.ParseUint(fields[2], 10, 32)
			gid, err2 := strconv.ParseUint(fields[3], 10, 32)
			// A mode is twelve bits: permissions, setuid, setgid, sticky.
			mode, err3 := strconv.ParseUint(fields[4], 8, 12)
			if !known || err0 != nil || err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			value["state"], value["type"], value["uid"], value["gid"] = fileStatePresent, kind, int(uid), int(gid)
			value["mode"] = fmt.Sprintf("%04o", mode)
			// An account or group name holds no colon, so the last two
			// fields are always the names, whatever came before.
			value["owner"], value["group"] = fields[len(fields)-2], fields[len(fields)-1]
			// A directory's size is its file system's bookkeeping, which
			// moves as entries come and go; it is not reported.
			if kind != "directory" {
				value["size"] = int64(size)
			}
		case strings.HasPrefix(line, "sha:"), strings.HasPrefix(line, "raw:"):
			// sha: a world-readable file's digest, kept plain; raw: any other
			// file's, handed over under the field nothing keeps unkeyed. A
			// file the login could read but whose digest did not come back (a
			// read error part way, no SHA-256 tool at all) has neither, and
			// no readable: what is unknown is left out.
			world := strings.HasPrefix(line, "sha:")
			field := "sha256"
			if !world {
				field = collection.RawDigestField
			}
			digest := strings.ToLower(gatherFirstField(line[len("sha:"):]))
			if b, err := hex.DecodeString(digest); err == nil && len(b) == 32 {
				value[field], value["readable"] = digest, true
			}
			value["world_readable"] = world
		case line == "sha-unreadable", line == "raw-unreadable":
			value["readable"], value["world_readable"] = false, line == "sha-unreadable"
		case strings.HasPrefix(line, "xs:"):
			value[attachedStateField] = strings.TrimPrefix(line, "xs:")
		case strings.HasPrefix(line, "xn:"):
			if b, err := hex.DecodeString(strings.TrimPrefix(line, "xn:")); err == nil {
				value[attachedNamesField] = string(b)
				value[attachedStateField] = attachedListed
			}
		case strings.HasPrefix(line, "xd:"):
			if d := strings.ToLower(gatherFirstField(line[len("xd:"):])); len(d) == 64 {
				if _, err := hex.DecodeString(d); err == nil {
					value[attachedDigestField] = d
				}
			}
		case strings.HasPrefix(line, "link:"):
			if b, err := hex.DecodeString(strings.TrimPrefix(line, "link:")); err == nil {
				value["link_target"] = strings.TrimSuffix(string(b), "\n")
			}
		}
	}
	// A private file's size is a secret's length, and is not kept, nor any
	// plain digest of it.
	if value["world_readable"] == false {
		delete(value, "size")
		delete(value, "sha256")
	}
	return value
}

// filesFacts names each of the probe's answers by the path at its position.
// An answer count that does not match the paths is a device that printed
// something this did not ask for: every path is then unreadable rather than
// matched to the wrong answer.
func filesFacts(paths []string, answers []gatherFact) []gatherFact {
	facts := make([]gatherFact, len(paths))
	for i, p := range paths {
		value := map[string]any{"state": fileStateUnreadable}
		if len(answers) == len(paths) {
			if v, ok := answers[i].value.(map[string]any); ok {
				value = v
			}
		}
		attached, ok := takeAttached(value, attachedXattrs)
		facts[i] = gatherFact{key: itemKey(gatherFamilyFile, p), value: value}
		if ok {
			facts = append(facts, gatherFact{key: itemKey(gatherFamilyFileAttached, p), value: attached})
		}
	}
	return facts
}

// The internal fields a files probe's answer carries what a path holds beside
// its content in, which takeAttached moves into the path's file_attached.
// fact, and the words that fact's state and kind take.
const (
	attachedStateField  = "_attached_state"
	attachedNamesField  = "_attached_names"
	attachedDigestField = "_attached_sha256"
	attachedKindField   = "_attached_kind"

	attachedListed   = "listed"
	attachedXattrs   = "xattrs"
	attachedStreams  = "streams"
	maxAttachedNames = 256
)

// takeAttached removes what value says the path carries beside its content
// and returns it as the file_attached. fact's value, of kind (xattrs, or the
// kind the answer names itself): state, the names (split on the lines and
// tabs the tools list them on, or the list a Windows answer gives, at most
// maxAttachedNames, with names_truncated past that), and the digest under the
// raw field, so the engine keys it whatever the file's mode, since a value may
// be private when the file is not. A listed answer without a digest is
// unreadable: what was not read is never shown as read.
func takeAttached(value map[string]any, kind string) (map[string]any, bool) {
	state, _ := value[attachedStateField].(string)
	digest, _ := value[attachedDigestField].(string)
	if k, ok := value[attachedKindField].(string); ok && k != "" {
		kind = k
	}
	var listed []any
	switch names := value[attachedNamesField].(type) {
	case string:
		for _, n := range strings.FieldsFunc(names, func(r rune) bool { return r == '\n' || r == '\t' }) {
			listed = append(listed, n)
		}
	case []any:
		listed = names
	}
	for _, f := range []string{attachedStateField, attachedNamesField, attachedDigestField, attachedKindField} {
		delete(value, f)
	}
	if state == "" {
		return nil, false
	}
	out := map[string]any{"kind": kind, "state": state}
	if state != attachedListed {
		return out, true
	}
	if digest == "" {
		out["state"] = fileStateUnreadable
		return out, true
	}
	if len(listed) > maxAttachedNames {
		listed, out["names_truncated"] = listed[:maxAttachedNames], true
	}
	if listed == nil {
		listed = []any{}
	}
	out["names"], out[collection.RawDigestField] = listed, digest
	return out, true
}
