// Run settings: values a run gives a method's parameters for every task that
// leaves them out.
//
// A parameter that declares RunSetting can be set for a whole run, not only on
// each task: file.remove's check_forbidden, which a rollback check of a
// recorded undo has no task to set it on. A run names one <method>.<parameter>
// (file.remove.check_forbidden), and the value is one of the parameter's
// declared Choices. The task's own value wins over the run's, and the
// parameter's Default applies when neither gives one. The engine fills them in
// (Fill) after a task's parameters render, so a method reads a run setting as
// an ordinary parameter, in process, in a Runner's child or in an external
// program alike.
//
// The layers a run setting resolves over are each tier's own: on the
// Controller, the method_settings launch field (the template's default, a
// saved configuration, the launch, most specific winning for each setting; a
// rollback adds its request's own over the job's); on the command line,
// --setting. A System, Organization or Project level joins beneath the
// template when those settings exist.
package collection

import (
	"fmt"
	"sort"
	"strings"
)

// RunSettings are a run's method settings, keyed <method>.<parameter>.
type RunSettings map[string]string

// SplitRunSettingKey splits key at its last dot into the method it names and
// the parameter, reporting false when either side is empty. A method's name
// holds dots; a parameter's never does.
func SplitRunSettingKey(key string) (method, param string, ok bool) {
	i := strings.LastIndex(key, ".")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// RunSettingParam returns the parameter key names, which must be one a method
// this binary knows declares as a run setting (ManifestOf: a registered method,
// or one of the built-in catalog a Controller installs).
func RunSettingParam(key string) (Param, error) {
	method, name, ok := SplitRunSettingKey(key)
	if !ok {
		return Param{}, fmt.Errorf("run setting %q is not <method>.<parameter>, such as file.remove.check_forbidden", key)
	}
	m, found := ManifestOf(method)
	if !found {
		return Param{}, fmt.Errorf("run setting %q names %q, which is not a method this build knows", key, method)
	}
	var settings []string
	for _, p := range m.Doc.Params {
		if !p.RunSetting {
			continue
		}
		if p.Name == name {
			return p, nil
		}
		settings = append(settings, method+"."+p.Name)
	}
	if len(settings) == 0 {
		return Param{}, fmt.Errorf("run setting %q: %s declares no parameter a run may set", key, method)
	}
	return Param{}, fmt.Errorf("run setting %q: %s declares no run setting %q; its run settings are %s", key, method, name, strings.Join(settings, ", "))
}

// CheckRunSetting returns an error when key names no run setting or value is
// not one of its choices.
func CheckRunSetting(key, value string) error {
	p, err := RunSettingParam(key)
	if err != nil {
		return err
	}
	if !p.choiceAllowed(value) {
		// The value is not echoed: a run setting is one word of a closed set,
		// so the set is all a reader needs, and a value pasted into the wrong
		// place is not printed back.
		return fmt.Errorf("run setting %q must be one of %s", key, p.ChoiceList())
	}
	return nil
}

// Check returns an error naming the first of s's settings, in key order, that
// CheckRunSetting refuses.
func (s RunSettings) Check() error {
	for _, key := range s.keys() {
		if err := CheckRunSetting(key, s[key]); err != nil {
			return err
		}
	}
	return nil
}

// Fill returns params with each of method's run settings that s holds and the
// task leaves out (absent, or null) set to s's value. params itself is never
// changed: when anything is filled the answer is a copy and true, and otherwise
// params and false.
// doc is method's documentation, whose RunSetting parameters say which keys a
// run may fill; any other key of s is not this method's and is left alone.
func (s RunSettings) Fill(method string, doc Doc, params map[string]any) (map[string]any, bool) {
	if len(s) == 0 {
		return params, false
	}
	out, copied := params, false
	for _, p := range doc.Params {
		if !p.RunSetting {
			continue
		}
		value, set := s[method+"."+p.Name]
		if !set {
			continue
		}
		if given, present := params[p.Name]; present && given != nil {
			continue
		}
		if !copied {
			out = make(map[string]any, len(params)+1)
			for k, v := range params {
				out[k] = v
			}
			copied = true
		}
		out[p.Name] = value
	}
	return out, copied
}

// keys returns s's keys sorted, so a refusal names the same one every time.
func (s RunSettings) keys() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkRunSetting refuses a run setting a run could not set safely: one that
// is not an optional text parameter with a closed set of choices and a
// default, or that takes secret references, is an alias or has one, holds a
// dot, or selects a call shape. A run setting crosses
// the Controller's job record and the dispatch as plain text, so it must be
// one of a set of words; and a task giving an alias would leave the run's
// value filling the parameter the alias stands for.
func checkRunSetting(d Descriptor, p Param) error {
	if !p.RunSetting {
		return nil
	}
	switch {
	case p.Type != ParamTypeString:
		return fmt.Errorf("collection: %q declares run setting %q of type %s; a run setting is text", d.Name, p.Name, p.Type)
	case len(p.Choices) == 0:
		return fmt.Errorf("collection: %q declares run setting %q with no choices; a run setting is one of a closed set", d.Name, p.Name)
	case p.Required:
		return fmt.Errorf("collection: %q declares run setting %q required; a run setting fills a parameter a task may leave out", d.Name, p.Name)
	case p.SecretRefs:
		return fmt.Errorf("collection: %q declares run setting %q taking secret references; a run setting is never a secret", d.Name, p.Name)
	case p.AliasOf != "":
		return fmt.Errorf("collection: %q declares run setting %q, an alias of %q; mark %q instead", d.Name, p.Name, p.AliasOf, p.AliasOf)
	case p.Default == "":
		return fmt.Errorf("collection: %q declares run setting %q with no default; a run setting states the value a task gets when nothing sets it, which is also what a Runner older than run settings gives it, so it must be the stricter choice", d.Name, p.Name)
	case strings.Contains(p.Name, "."):
		// A setting is named <method>.<parameter>, split at the last dot. (A
		// parameter named target, the task's own, is refused for every
		// parameter, run setting or not.)
		return fmt.Errorf("collection: %q declares run setting %q; a run setting's name holds no dot", d.Name, p.Name)
	}
	for _, shape := range d.Manifest.Shapes {
		if shape.Param == p.Name {
			return fmt.Errorf("collection: %q declares run setting %q, which selects a call shape; validation reads a task's own parameters, so a shape chosen by a run setting would be checked as another", d.Name, p.Name)
		}
	}
	for _, other := range d.Manifest.Doc.Params {
		if other.AliasOf == p.Name {
			return fmt.Errorf("collection: %q declares run setting %q, which parameter %q is an alias of; a run setting has one name", d.Name, p.Name, other.Name)
		}
	}
	return nil
}
