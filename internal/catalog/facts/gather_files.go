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

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gatherParamFiles names the paths the files subset reads.
const gatherParamFiles = "files"

// gatherMaxFiles bounds how many paths one task may name, so a task cannot
// turn a fact gather into a crawl of the file system.
const gatherMaxFiles = 64

// gatherMaxFileKeyBytes is the longest a file fact's key may be: the fact
// store's own key bound (internal/facts.MaxKeyBytes).
const gatherMaxFileKeyBytes = 256

// fileSectionMark begins each path's answer.
const fileSectionMark = "--pleiades-file--"

// The states a path can be in.
const (
	fileStatePresent    = "present"
	fileStateAbsent     = "absent"
	fileStateUnreadable = "unreadable"
)

// gatherFilePaths reads and checks files: at most gatherMaxFiles paths, each
// absolute (/ on a POSIX device, a drive letter on Windows), valid UTF-8,
// holding no NUL, and short enough that its fact key fits the store.
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
		case !strings.HasPrefix(p, "/") && !windowsAbsolute(p):
			return nil, fmt.Errorf("%s path %q is not absolute (/... on a POSIX device, C:\\... on Windows)", gatherParamFiles, p)
		case len(itemKey(gatherFamilyFile, p)) > gatherMaxFileKeyBytes:
			return nil, fmt.Errorf("%s path %q is too long to name a fact (%d bytes once encoded, at most %d)", gatherParamFiles, p, len(itemKey(gatherFamilyFile, p)), gatherMaxFileKeyBytes)
		case seen[p]:
			return nil, fmt.Errorf("%s names %q twice", gatherParamFiles, p)
		}
		seen[p] = true
	}
	return paths, nil
}

// windowsAbsolute reports whether p is a drive-rooted Windows path (C:\...).
// A UNC path (\\server\share) is not taken: it would reach another machine
// through this one.
func windowsAbsolute(p string) bool {
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// posixFileProbe is the files probe of a POSIX platform: statFormat is its
// stat's options and format, printing type, size, uid, gid, mode, owner and
// group separated by colons (which an account name cannot hold).
func posixFileProbe(statFormat string) gatherProbe {
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
			return `for p in ` + strings.Join(quoted, " ") + `; do echo '` + fileSectionMark + `'; ` +
				`if e=$(LC_ALL=C stat ` + statFormat + ` -- "$p" 2>&1); then printf 'ok:%s\n' "$e"; ` +
				`if [ -f "$p" ] && [ ! -L "$p" ]; then if [ -r "$p" ]; then printf 'sha:%s\n' "$(` + posixSHA256 + ` < "$p")"; else echo 'sha-unreadable'; fi; fi; ` +
				`if [ -L "$p" ]; then printf 'link:%s\n' "$(readlink -- "$p" | od -An -tx1 | tr -d ' \n')"; fi; ` +
				`else case $e in *': No such file or directory'|*': Not a directory') echo absent;; *) echo unreadable;; esac; fi; done`
		},
		parse: parseFiles,
	}
}

// posixSHA256 hashes stdin with whichever SHA-256 tool the device has:
// sha256sum (GNU, BusyBox, FreeBSD 15, macOS 27), FreeBSD's sha256 -q, or
// shasum on an older Mac. Each prints the hex digest first.
const posixSHA256 = `if command -v sha256sum >/dev/null 2>&1; then sha256sum; elif command -v sha256 >/dev/null 2>&1; then sha256 -q; else shasum -a 256; fi`

// The stat formats: GNU and BusyBox stat -c, and BSD stat -f (FreeBSD and
// macOS), whose %Mp%Lp is the four-digit mode with setuid, setgid and sticky.
const (
	statFormatGNU = `-c '%F:%s:%u:%g:%a:%U:%G'`
	statFormatBSD = `-f '%HT:%z:%u:%g:%Mp%Lp:%Su:%Sg'`
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
			mode, err3 := strconv.ParseUint(fields[4], 8, 32)
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
		case strings.HasPrefix(line, "sha:"):
			// A file the login could read but whose digest did not come
			// back (a read error part way, no SHA-256 tool at all) has no
			// sha256 and no readable: what is unknown is left out.
			digest := strings.ToLower(gatherFirstField(strings.TrimPrefix(line, "sha:")))
			if b, err := hex.DecodeString(digest); err == nil && len(b) == 32 {
				value["sha256"], value["readable"] = digest, true
			}
		case line == "sha-unreadable":
			value["readable"] = false
		case strings.HasPrefix(line, "link:"):
			if b, err := hex.DecodeString(strings.TrimPrefix(line, "link:")); err == nil {
				value["link_target"] = strings.TrimSuffix(string(b), "\n")
			}
		}
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
		facts[i] = gatherFact{key: itemKey(gatherFamilyFile, p), value: value}
	}
	return facts
}
