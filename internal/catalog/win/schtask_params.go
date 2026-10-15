// win.schtask's parameters, each held to its form before WinRM is reached.
package win

import (
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// spec is a call: what to do with which task, and for state present, the
// task as it should be.
type spec struct {
	state, path, name                  string
	execute, arguments, workingDir     string
	trigger, at, user, logon, runLevel string
	description, xml                   string
	enabled                            bool
}

// parse reads a call, holding every value to its form.
func parse(params map[string]any) (spec, error) {
	var s spec
	full, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return s, err
	}
	parts := strings.Split(strings.TrimPrefix(full, `\`), `\`)
	for _, p := range parts {
		if !segment.MatchString(p) {
			return s, fmt.Errorf("%s %q is not a task path: folders and a name of letters, digits, space, '.', '_' and '-', separated by \\", paramName, full)
		}
	}
	s.name, s.path = parts[len(parts)-1], `\`+strings.Join(parts[:len(parts)-1], `\`)
	if !strings.HasSuffix(s.path, `\`) {
		s.path += `\`
	}
	s.state = sdk.StringParam(params, paramState)
	if s.state == "" {
		s.state = "present"
	}
	switch s.state {
	case "query", "absent":
		return s, nil
	case "present":
	default:
		return s, fmt.Errorf("%s %q is not present, absent or query", paramState, s.state)
	}
	if s.xml = sdk.StringParam(params, paramXML); s.xml != "" {
		if len(s.xml) > maxAnswer || !strings.Contains(s.xml, "<Task") {
			return s, fmt.Errorf("%s is not a scheduled task's XML definition", paramXML)
		}
		return s, nil
	}
	if s.execute, err = sdk.RequiredStringParam(params, paramExecute); err != nil {
		return s, fmt.Errorf("%w (or give %s, a task's XML definition)", err, paramXML)
	}
	s.arguments, s.workingDir, s.description = sdk.StringParam(params, paramArguments), sdk.StringParam(params, paramWorkingDir), sdk.StringParam(params, paramDescription)
	for _, f := range []struct {
		name, value string
		max         int
	}{{paramExecute, s.execute, 260}, {paramArguments, s.arguments, 8000}, {paramWorkingDir, s.workingDir, 260}, {paramDescription, s.description, 1000}} {
		if len(f.value) > f.max || strings.ContainsAny(f.value, "\x00\r\n") {
			return s, fmt.Errorf("%s is over %d characters or holds a line break", f.name, f.max)
		}
	}
	s.trigger, s.at = sdk.StringParam(params, paramTrigger), sdk.StringParam(params, paramAt)
	switch s.trigger {
	case "at_startup", "at_logon":
		if s.at != "" {
			return s, fmt.Errorf("%s goes with daily or once, not %s", paramAt, s.trigger)
		}
	case "daily":
		if !clockTime.MatchString(s.at) {
			return s, fmt.Errorf("a daily trigger's %s is a time, HH:MM", paramAt)
		}
	case "once":
		if _, err := time.Parse(time.RFC3339, s.at); err != nil {
			return s, fmt.Errorf("a once trigger's %s is an RFC 3339 time", paramAt)
		}
	default:
		return s, fmt.Errorf("%s is one of %v", paramTrigger, triggers)
	}
	s.user = sdk.StringParam(params, paramRunAs)
	if s.user == "" {
		s.user = "SYSTEM"
	}
	if !account.MatchString(s.user) {
		return s, fmt.Errorf("%s %q is not an account name", paramRunAs, s.user)
	}
	s.logon = "s4u"
	if serviceAccounts[strings.ToUpper(s.user)] {
		s.logon, s.user = "service", strings.ToUpper(s.user)
	}
	switch s.runLevel = sdk.StringParam(params, paramRunLevel); s.runLevel {
	case "", "limited":
		s.runLevel = "Limited"
	case "highest":
		s.runLevel = "Highest"
	default:
		return s, fmt.Errorf("%s is limited or highest", paramRunLevel)
	}
	if s.enabled, err = sdk.BoolParamOr(params, paramEnabled, true); err != nil {
		return s, err
	}
	return s, nil
}
