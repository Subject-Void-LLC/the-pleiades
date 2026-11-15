// What adhoc says when its hosts are missing: the hosts come first, and a
// call that needs no device takes "-" as its hosts.
package main

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// hostsFirstHint is the sentence adhoc adds to a refusal whose hosts are
// missing, given the positionals it was handed. It names "-", the hosts a
// call with no device takes, unless the method named first needs a device
// on every call, when it asks for one instead: "-" would only be refused a
// second time. A method that needs no device at all never reaches here,
// since withNoHosts already put "-" before it.
func hostsFirstHint(positionals []string) string {
	method := "<method>"
	if len(positionals) > 0 {
		method = positionals[0]
	}
	rest := ""
	if len(positionals) > 1 {
		rest = " " + strings.Join(positionals[1:], " ")
	}
	if d, ok := collection.Lookup(method); ok && d.Manifest.ExecutionContext.Device == collection.DeviceRequired {
		return fmt.Sprintf("adhoc takes the hosts first, then the method: %s acts on a device, so name the device or tag it runs on before it (pleiades adhoc <device> %s%s)", method, method, rest)
	}
	return fmt.Sprintf("adhoc takes the hosts first, then the method: a device, a tag, a comma list, or - for a call with no device (pleiades adhoc - %s%s)", method, rest)
}

// parameterAsMethod is adhoc's refusal when the token in the method's place
// is a key=value parameter, which no method name holds. When the hosts'
// place holds a registered method, the hosts were left out and the method
// was read as them, so the refusal says the hosts come first. It is nil when
// the method's place holds no "=".
func parameterAsMethod(hosts, method string, rest []string) error {
	if !strings.Contains(method, "=") {
		return nil
	}
	given := strings.Join(append([]string{method}, rest...), " ")
	if hosts == noHosts {
		return fmt.Errorf("%q stands where the method goes, but it is a parameter: name the method after the - (pleiades adhoc - <method> %s)", method, given)
	}
	if _, ok := collection.Lookup(hosts); ok {
		return fmt.Errorf("%q stands where the method goes, but it is a parameter, and %s is a method; %s", method, hosts, hostsFirstHint(append([]string{hosts, method}, rest...)))
	}
	// An external method is not registered until the run loads it, so a
	// name not known here may still be the method.
	return fmt.Errorf("%q stands where the method goes, but it is a parameter: name the method after the hosts (pleiades adhoc %s <method> %s), or, when %s is the method, the hosts before it, - for a call with no device", method, hosts, given, hosts)
}
