// Package playbook: ansible_distribution_major_version | int, translated as
// Jinja evaluates it.
//
// Ansible's major version is a string ("12", or a word such as Debian
// testing's forky/sid), and Jinja's int filter turns it into a number: "12"
// is 12, and a string that is no number is the filter's default, 0. The
// native os.version_major holds the integer where Ansible's string is a whole
// number and is absent where it is not, so the read is the native major where
// the device has one and 0 where it has none, and a comparison of that with
// a whole number decides what Jinja's does on every host the fact fixtures
// measure (and, from Ansible's source, on the rest). Only that is
// translated: the six comparisons (==, !=, <, <=, >, >=) with an integer
// literal. An ordering against text raises in Jinja and an equality with text
// is never true there, so neither is translated; a fraction is no
// version's major, and a membership, a truth or any filter after | int is
// left to a person (the last refused in whencel_ops.go's filter). int with
// arguments (a default, a base) never reaches here, since the condition
// parser refuses it (when.go's test), and neither does a filter before int:
// the parser refuses every filter but bool, int and length, and filter
// refuses those three on a fact itself.
package playbook

import (
	"fmt"
	"strconv"
)

// ansibleMajorVersion is the Ansible fact whose | int this file translates.
const ansibleMajorVersion = "ansible_distribution_major_version"

// majorIntTerm translates the filter e applied to the fact read in when it is
// ansible_distribution_major_version | int: the native major where the device
// has one, 0 where it has none. It reports false for any other filter on any
// other fact, which the caller refuses.
func majorIntTerm(e *expr, in term) (term, bool) {
	if e.name != "int" || in.fact != ansibleMajorVersion {
		return term{}, false
	}
	present := strconv.Quote(in.field) + " in nodes" + celKey(in.register) + "[device.id]"
	cel := "((" + present + ") ? " + in.cel + " : 0)"
	return term{cel: cel, typ: "int", register: in.register, field: in.field, majorInt: true}, true
}

// majorIntCompare writes the comparison op between open, the integer read of
// majorIntTerm, and the constant fixed, open on the left when openLeft, or
// refuses it.
func majorIntCompare(open, fixed term, op string, openLeft bool) (term, *celError) {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
	default:
		return term{}, &celError{"when.fact", fmt.Sprintf("%s | int is compared by %s, which is not translated; compare it with one whole number, or write the condition on os.version_major", ansibleMajorVersion, op)}
	}
	n, ok := fixed.value.(int64)
	if !ok {
		// Jinja raises comparing an integer with text, and the parser gives
		// a number written with a point or an exponent as a float64.
		kind := kindOf(fixed.value)
		if _, fraction := fixed.value.(float64); fraction {
			kind = "a number written with a point"
		}
		return term{}, &celError{"when.fact", fmt.Sprintf("%s | int is compared with %s; only a whole number written in the condition is translated", ansibleMajorVersion, kind)}
	}
	lit := strconv.FormatInt(n, 10)
	if openLeft {
		return term{cel: open.cel + " " + op + " " + lit, typ: "bool"}, nil
	}
	return term{cel: lit + " " + op + " " + open.cel, typ: "bool"}, nil
}
