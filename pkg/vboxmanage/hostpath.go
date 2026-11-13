// How a prune and a VM's delete compare paths on a Windows host: from a
// drive's root, by ASCII case, with either separator, and never through a
// ".." segment, a stream, a UNC path or a link.
package vboxmanage

import "strings"

// normPath is path as a prune compares it: from its drive's root, with
// VirtualBox's either separator written as a backslash and ASCII letters in
// lower case. ok is false for a path no prune may name: one not from a
// drive's root (a UNC path, a \\?\ path, a relative one), one naming a
// stream with a second colon, and one holding a segment of only dots and
// spaces, which Windows reads as "this folder" or "the folder above" rather
// than as a name ("..", and ".. " or "..." as well), or an empty one.
//
// Only ASCII letters are folded. Windows compares names through its own
// upper-case table, under which the Kelvin sign (U+212A) and K are
// different letters, while Go's strings.ToLower makes both k; folding more
// than ASCII would count a sibling folder spelled that way as inside the
// machine folder. A name differing outside ASCII is another name here, and
// so is never removed as one inside.
func normPath(p string) (string, bool) {
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) < 4 || !isASCIILetter(p[0]) || p[1] != ':' || p[2] != '\\' || strings.Contains(p[2:], ":") {
		return "", false
	}
	segments := strings.Split(strings.TrimRight(p[3:], `\`), `\`)
	for _, s := range segments {
		if strings.Trim(s, ". ") == "" {
			return "", false
		}
	}
	return asciiLower(p[:3] + strings.Join(segments, `\`)), true
}

// isASCIILetter reports whether c is an ASCII letter, as a drive is named.
func isASCIILetter(c byte) bool {
	return ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')
}

// asciiLower is s with its ASCII letters in lower case and every other
// byte as it was.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// Within reports whether path is dir or inside it, on a Windows host, as a
// prune and a VM's delete decide what is the VM's to delete: a path from a
// drive's root, compared by ASCII case, with either separator. A path with a
// segment of only dots and spaces (".."), a stream (a second colon), a UNC
// or \\?\ prefix, or no drive is inside nothing, and nothing is inside it,
// however its text begins. VirtualBox writes the paths it reports in full,
// so a real one is never refused; one that is not is never trusted.
func Within(dir, path string) bool { return within(dir, path) }

// within is Within.
func within(dir, path string) bool {
	d, okDir := normPath(dir)
	p, okPath := normPath(path)
	return okDir && okPath && (p == d || strings.HasPrefix(p, d+`\`))
}

// samePath reports whether a and b name one place, as within compares.
func samePath(a, b string) bool {
	return within(a, b) && within(b, a)
}

// mayHold reports whether p is dir or inside it, or cannot be compared with
// it, so might be. It is for the paths that keep what is under them (a
// VM's folder, a disk that stays, a link): one nobody can place keeps
// everything, since \\localhost\G$\Lab\web names a folder inside G:\Lab
// as surely as G:\Lab\web does.
func mayHold(dir, p string) bool {
	_, okDir := normPath(dir)
	_, okPath := normPath(p)
	return !okDir || !okPath || within(dir, p)
}

// anyHolds reports whether any of dirs may hold p (mayHold).
func anyHolds(dirs []string, p string) bool {
	for _, d := range dirs {
		if mayHold(d, p) {
			return true
		}
	}
	return false
}

// holdsAny reports whether dir may hold any of paths (mayHold).
func holdsAny(dir string, paths []string) bool {
	for _, p := range paths {
		if mayHold(dir, p) {
			return true
		}
	}
	return false
}
