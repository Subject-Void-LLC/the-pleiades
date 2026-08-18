package file

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// maxModeDigits is how many octal digits a mode may carry: three for the
// permission bits and an optional fourth leading digit for setuid,
// setgid and the sticky bit.
const maxModeDigits = 4

// This file holds the one copy of the mode/owner/group parameter rules
// every method in this package that sets file attributes needs.
//
// There used to be three copies, one each in copy.go and
// permissions.go and a partial one in directory.go, and file.touch had
// none at all. That last part is what made this worth consolidating
// rather than leaving alone: touch read its mode with sdk.StringParam,
// which reports a non-string as absent, so `mode: 0600` written without
// quotes (the single most common mistake with Ansible's file modules,
// since YAML reads the leading zero as octal and hands over the number
// 384) was silently dropped. The task then touched the file, reported
// success, and left the mode alone. A refusal is the only acceptable
// answer to that, and the way to make sure the next method added here
// gets it is for there to be exactly one place it lives.
//
// file.directory deliberately still has its own directoryMode. Its rule
// is not the same one: it requires three or four digits where these
// accept one to four, and unifying that would change what runbooks it
// accepts as a side effect of removing duplication, which is not a
// trade worth making silently.

// attributeParams reads and validates the mode, owner and group
// parameters shared by the attribute-setting methods, returning the
// remotefile.Attributes they describe.
//
// An absent field stays empty, which remotefile.Apply reads as "leave
// this alone": that is what lets a task set a mode without also having
// an opinion about the owner.
func attributeParams(params map[string]any, modeKey, ownerKey, groupKey string) (remotefile.Attributes, error) {
	var attrs remotefile.Attributes
	var err error

	if attrs.Mode, err = textParam(params, modeKey); err != nil {
		return remotefile.Attributes{}, err
	}
	if attrs.Owner, err = textParam(params, ownerKey); err != nil {
		return remotefile.Attributes{}, err
	}
	if attrs.Group, err = textParam(params, groupKey); err != nil {
		return remotefile.Attributes{}, err
	}

	if err := checkMode(modeKey, attrs.Mode); err != nil {
		return remotefile.Attributes{}, err
	}
	if err := checkName(ownerKey, attrs.Owner); err != nil {
		return remotefile.Attributes{}, err
	}
	if err := checkName(groupKey, attrs.Group); err != nil {
		return remotefile.Attributes{}, err
	}
	return attrs, nil
}

// textParam reads one string parameter, refusing a value that arrived as
// something other than text.
//
// sdk.StringParam is the usual reader and it treats a non-string as
// absent, which is the wrong answer here twice over. `mode: 0644`
// without quotes is the most common mistake anyone makes with Ansible's
// file modules: YAML reads the leading zero as octal and hands over the
// number 420. And `content: 8080` is a number that would render as
// "8080" on the Walk tier and could arrive as a float64 across the
// Runner's task subprocess boundary on the Crawl tier, so a method that
// stringified it would write different bytes depending on which tier ran
// it. Both are refused by name instead.
func textParam(params map[string]any, key string) (string, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s is %T, not text: quote it in the runbook, since YAML reads an unquoted 0644 as the number 420 and an unquoted 8080 as an integer rather than as the digits you wrote", key, raw)
	}
	return text, nil
}

// checkMode refuses a mode this package cannot compare, which is any
// mode that is not plain octal digits.
//
// A symbolic mode (u+x, go-w) is what this rejects, and rejecting it is
// the honest option rather than the limited one. Deciding whether u+x is
// already applied means resolving it against the current bits, and the
// alternative that needs no resolution, applying it every time, is a
// method that reports changed forever. remotefile.NormalizeMode compares
// "0644" against stat's "644" by padding; there is nothing it can pad
// "u+x" into.
func checkMode(key, mode string) error {
	if mode == "" || isOctal(mode) {
		return nil
	}
	return fmt.Errorf("%s %q must be one to four octal digits such as \"0644\": a symbolic mode cannot be compared against what the device reports, so the task would report changed on every run",
		key, mode)
}

// checkName refuses a numeric owner or group.
//
// chown itself is the reason. An all-digit argument is read by chown as a
// numeric id, never as a name, while the device reports names back
// through stat, so "1000" and "alice" would compare unequal on every
// single run even when they are the same account. Resolving one to the
// other would need a passwd lookup on the device that this namespace has
// no primitive for, so the refusal says what to write instead.
func checkName(key, name string) error {
	if name == "" || !isDigits(name) {
		return nil
	}
	return fmt.Errorf("%s %q is a numeric id: name the account instead, since the device reports names and a numeric id would compare unequal on every run", key, name)
}

// isOctal reports whether text is one to maxModeDigits octal digits,
// which is the only mode form these methods accept.
func isOctal(text string) bool {
	return len(text) <= maxModeDigits && digitsOnly(text, '7')
}

// isDigits reports whether text is entirely decimal digits, which is how
// an owner or group given as a numeric id is recognized.
func isDigits(text string) bool {
	return digitsOnly(text, '9')
}

// digitsOnly reports whether text is non-empty and made only of digits
// from '0' to max.
func digitsOnly(text string, max byte) bool {
	if text == "" {
		return false
	}
	for i := range len(text) {
		if text[i] < '0' || text[i] > max {
			return false
		}
	}
	return true
}
