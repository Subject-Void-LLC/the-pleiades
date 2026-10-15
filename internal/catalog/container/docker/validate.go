// What a docker CLI method checks about its parameters before anything
// reaches the device.
package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// restartPolicyPattern is every restart policy docker run accepts: no,
// always, unless-stopped, and on-failure with an optional retry count.
var restartPolicyPattern = regexp.MustCompile(`^(no|always|unless-stopped|on-failure(:[0-9]{1,9})?)$`)

// checkName refuses a container name no engine would give a container
// (containerengine.ValidName's grammar), before anything reaches the device.
// The grammar starts with a letter or digit, so no name can be read as an
// option, and holds no whitespace or shell metacharacter.
func checkName(name string) error {
	return containerengine.ValidName("container", name)
}

// checkRunParams refuses, before anything reaches the device, a value docker
// run would read as something other than what the runbook wrote: an image
// that is not an image reference (containerengine.ValidImage), a restart
// policy docker does not have, or a port or volume that is empty, begins
// with "-" or holds a control character. Each flag also travels in its
// equals form (--publish=VALUE), which binds the value to its flag whatever
// it holds, so the check is the first of two guards rather than the only one.
func checkRunParams(image string, ports, volumes []string, restartPolicy string) error {
	if err := containerengine.ValidImage(image); err != nil {
		return fmt.Errorf("%s: %w", paramImage, err)
	}
	if restartPolicy != "" && !restartPolicyPattern.MatchString(restartPolicy) {
		return fmt.Errorf("%s %q is not one of no, always, unless-stopped or on-failure[:N]", paramRestartPolicy, restartPolicy)
	}
	for _, list := range []struct {
		param  string
		values []string
	}{{paramPorts, ports}, {paramVolumes, volumes}} {
		for _, v := range list.values {
			if err := checkFlagValue(v); err != nil {
				return fmt.Errorf("%s: %w", list.param, err)
			}
		}
	}
	return nil
}

// checkFlagValue refuses a flag's value that is empty, begins with "-", or
// holds a control character or a NUL byte.
func checkFlagValue(v string) error {
	if v == "" {
		return fmt.Errorf("an entry is empty")
	}
	if v[0] == '-' {
		return fmt.Errorf("%q begins with \"-\", which docker would read as an option", v)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q holds a control character", v)
		}
	}
	return nil
}

// envParam reads the env param as a map of string keys to string
// values, refusing anything else the way sdk.StringSlice refuses a
// non-string list element: a value that arrived as a number or a bool
// would render differently on the Crawl tier than across the Runner's
// JSON subprocess boundary, and a silently different docker run command
// is worse than a refusal.
func envParam(params map[string]any) (map[string]string, error) {
	raw, present := params[paramEnv]
	if !present || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a map of string keys to string values", paramEnv)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%s] is %T, not a string: quote it in the runbook", paramEnv, k, v)
		}
		if err := checkEnvEntry(k, s); err != nil {
			return nil, fmt.Errorf("%s: %w", paramEnv, err)
		}
		out[k] = s
	}
	return out, nil
}

// checkEnvEntry refuses a name or value docker's env-file format cannot
// carry exactly. The values travel as an env file on standard input (see
// dockerRunArgs), one NAME=value per line, where docker skips a line
// starting with "#" and refuses a name holding whitespace, so a name that
// is empty, starts with "#", or holds whitespace, "=" or a NUL byte, and a
// value holding a line break or a NUL byte, would arrive as something other
// than what the runbook wrote.
func checkEnvEntry(name, value string) error {
	if name == "" || strings.HasPrefix(name, "#") || strings.ContainsAny(name, "= \t\r\n\v\f\x00") {
		return fmt.Errorf("variable name %q is empty, starts with #, or holds whitespace, = or a NUL byte", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("the value of %s holds a line break or a NUL byte, which an env file cannot carry", name)
	}
	return nil
}
