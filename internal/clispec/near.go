// Package clispec: finding the subcommand a mistyped word was probably meant
// to be, for a command that also takes a positional of its own (facts
// <host>), where a misspelled subcommand would otherwise be read as that
// positional.
package clispec

// NearSubcommand is the subcommand of spec that word is one edit away from
// (a letter added, dropped or changed, or two neighbors swapped), and true;
// or "" and false when word is a subcommand itself or near none. The first
// near one in spec's order wins.
func NearSubcommand(spec Command, word string) (string, bool) {
	if _, exact := Find(spec, word); exact {
		return "", false
	}
	for _, c := range spec.Subcommands {
		if oneEdit(c.Name, word) {
			return c.Name, true
		}
	}
	return "", false
}

// oneEdit reports whether a and b, which differ, are one edit apart: one
// byte inserted, deleted or substituted, or two adjacent bytes swapped.
func oneEdit(a, b string) bool {
	if a == b {
		return false
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	switch len(a) - len(b) {
	case 0:
		// One substitution, or one swap of neighbors.
		var diff []int
		for i := range len(a) {
			if a[i] != b[i] {
				diff = append(diff, i)
			}
		}
		switch len(diff) {
		case 1:
			return true
		case 2:
			i, j := diff[0], diff[1]
			return j == i+1 && a[i] == b[j] && a[j] == b[i]
		}
		return false
	case 1:
		// a is b with one byte inserted: skip the first byte they differ at.
		i := 0
		for i < len(b) && a[i] == b[i] {
			i++
		}
		return a[i+1:] == b[i:]
	}
	return false
}
