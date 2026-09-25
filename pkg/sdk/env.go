// Runbook values passed to a script as environment variables.
package sdk

import (
	"fmt"
	"sort"
)

// EnvPrefix begins the name of every environment variable a task's env
// parameter sets. It keeps a runbook value from replacing a variable the
// device relies on (PATH, TEMP, SystemRoot), and lets a script tell its
// inputs apart from the environment it inherited: a task writing
// env: {NAME: x} is read in the script as $env:PLEIADES_NAME in
// PowerShell or !PLEIADES_NAME! in cmd.exe (never %PLEIADES_NAME%, which
// cmd.exe expands before parsing). The engine's winrm_exec action and
// exec.winrm.shell
// both use this, so one runbook value is spelled the same way in both.
const EnvPrefix = "PLEIADES_"

// EnvParam reads params[key], a map of names to scalar values, as
// environment variables named EnvPrefix plus each name. An absent or nil
// parameter is no variables. A value that is not a string, number or
// boolean is refused rather than flattened into text nobody chose, and a
// name is checked by whatever sends it (pkg/winrmexec for WinRM), not
// here.
//
// The environment is visible to other processes on the device, so this
// is for data a script reads, never for a secret.
func EnvParam(params map[string]any, key string) (map[string]string, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return nil, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parameter %q must be a map of names to values, got %T", key, raw)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make(map[string]string, len(values))
	for _, name := range names {
		switch v := values[name].(type) {
		case string, bool, int, int64, float64:
			env[EnvPrefix+name] = fmt.Sprint(v)
		default:
			return nil, fmt.Errorf("parameter %q: %s must be a string, number or boolean, got %T", key, name, values[name])
		}
	}
	return env, nil
}
