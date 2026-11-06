// Package collection_test: tests for run settings, what Register allows one
// to be, how a setting is held to its method, and how a run fills one.
package collection_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// settingDescriptor is a registerable method declaring params.
func settingDescriptor(name string, params ...collection.Param) collection.Descriptor {
	d := implemented(name, collection.Reversibility{Reversible: true})
	d.Manifest.Doc.Params = params
	return d
}

// mode is a valid run setting.
var mode = collection.Param{Name: "mode", Type: "string", Default: "refuse", Choices: []string{"refuse", "warn"}, RunSetting: true}

// TestRegister_ARunSettingIsAClosedSetOfWords holds Register to what a run
// setting may be: optional text with choices, never a secret, never an alias
// or aliased, since a run setting crosses the job record and the dispatch as
// plain text and fills one parameter by one name.
func TestRegister_ARunSettingIsAClosedSetOfWords(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	with := func(change func(*collection.Param)) collection.Param {
		p := mode
		change(&p)
		return p
	}
	refused := map[string][]collection.Param{
		"of type":                 {with(func(p *collection.Param) { p.Type, p.Choices, p.Default = "bool", nil, "" })},
		"with no choices":         {with(func(p *collection.Param) { p.Choices, p.Default = nil, "" })},
		"required":                {with(func(p *collection.Param) { p.Required, p.Default = true, "" })},
		"taking secret":           {with(func(p *collection.Param) { p.SecretRefs = true })},
		"an alias of":             {{Name: "real", Type: "string", Choices: []string{"a"}}, with(func(p *collection.Param) { p.AliasOf, p.Choices, p.Default = "real", []string{"a"}, "" })},
		"which parameter \"alt\"": {mode, {Name: "alt", Type: "string", AliasOf: "mode"}},
		"with no default":         {with(func(p *collection.Param) { p.Default = "" })},
		"holds no dot":            {with(func(p *collection.Param) { p.Name = "a.b" })},
	}
	i := 0
	for want, params := range refused {
		i++
		err := collection.Register(settingDescriptor("test.runsetting_bad_"+strings.Repeat("x", i), params...))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Register = %v, want a refusal saying %q", want, err, want)
		}
	}
	shaped := settingDescriptor("test.runsetting_shape", mode)
	shaped.Manifest.Shapes = []collection.CallShape{{Param: "mode", SupportedTransports: []string{"ssh"}}}
	if err := collection.Register(shaped); err == nil || !strings.Contains(err.Error(), "selects a call shape") {
		t.Errorf("a run setting selecting a call shape: Register = %v, want it refused", err)
	}
	if err := collection.Register(settingDescriptor("test.runsetting_good", mode)); err != nil {
		t.Fatalf("a valid run setting was refused: %v", err)
	}
}

// TestCheckRunSetting_NamesWhatIsWrong proves a run setting is held to the
// method it names, and each refusal says what to give instead.
func TestCheckRunSetting_NamesWhatIsWrong(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	other := collection.Param{Name: "path", Type: "string"}
	if err := collection.Register(settingDescriptor("test.settings.method", mode, other)); err != nil {
		t.Fatal(err)
	}
	if err := collection.Register(settingDescriptor("test.settings.none", other)); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"nodot":                     "is not <method>.<parameter>",
		"test.settings.method.":     "is not <method>.<parameter>",
		".mode":                     "is not <method>.<parameter>",
		"test.settings.absent.mode": "not a method this build knows",
		"test.settings.none.path":   "declares no parameter a run may set",
		"test.settings.method.path": "its run settings are test.settings.method.mode",
		"test.settings.method.mode": "must be one of refuse, warn",
	} {
		value := "warn"
		if key == "test.settings.method.mode" {
			value = "maybe"
		}
		if err := collection.CheckRunSetting(key, value); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckRunSetting(%q, %q) = %v, want it refused saying %q", key, value, err, want)
		}
	}
	if err := collection.CheckRunSetting("test.settings.method.mode", "warn"); err != nil {
		t.Errorf("a valid setting was refused: %v", err)
	}
	// Check names the first refusal in key order, every time.
	err := collection.RunSettings{"test.settings.method.mode": "warn", "b.x": "y", "a.x": "y"}.Check()
	if err == nil || !strings.Contains(err.Error(), `"a.x"`) {
		t.Errorf("Check = %v, want the refusal of a.x, first in key order", err)
	}
	if err := (collection.RunSettings{}).Check(); err != nil {
		t.Errorf("no settings: %v", err)
	}
}

// TestRunSettingsFill_OnlyWhatTheTaskLeftOut proves the task's own value
// wins, a null counts as left out, another method's settings and a parameter
// that is not a run setting are never filled, and the task's map is never
// changed.
func TestRunSettingsFill_OnlyWhatTheTaskLeftOut(t *testing.T) {
	doc := collection.Doc{Params: []collection.Param{mode, {Name: "path", Type: "string"}}}
	settings := collection.RunSettings{"m.x.mode": "warn", "m.x.path": "/etc", "other.mode": "refuse"}

	params := map[string]any{"path": "/tmp/a"}
	filled, changed := settings.Fill("m.x", doc, params)
	if !changed || !reflect.DeepEqual(filled, map[string]any{"path": "/tmp/a", "mode": "warn"}) {
		t.Errorf("Fill = %v, %v, want mode filled and path the task's", filled, changed)
	}
	if !reflect.DeepEqual(params, map[string]any{"path": "/tmp/a"}) {
		t.Errorf("Fill changed the task's own params: %v", params)
	}

	given := map[string]any{"mode": "refuse"}
	if got, changed := settings.Fill("m.x", doc, given); changed || got["mode"] != "refuse" {
		t.Errorf("a task's own value = %v, %v, want it kept and nothing filled", got, changed)
	}
	if got, changed := settings.Fill("m.x", doc, map[string]any{"mode": nil}); !changed || got["mode"] != "warn" {
		t.Errorf("a null = %v, %v, want it filled", got, changed)
	}
	if got, changed := settings.Fill("m.y", doc, nil); changed || got != nil {
		t.Errorf("another method = %v, %v, want nothing filled", got, changed)
	}
	if got, changed := (collection.RunSettings(nil)).Fill("m.x", doc, params); changed || !reflect.DeepEqual(got, params) {
		t.Errorf("no settings = %v, %v, want params as they are", got, changed)
	}
	if got, changed := settings.Fill("m.x", doc, nil); !changed || got["mode"] != "warn" {
		t.Errorf("a task with no params = %v, %v, want mode filled", got, changed)
	}
}

// TestSplitRunSettingKey splits at the last dot, since a method's name holds
// dots and a parameter's never does.
func TestSplitRunSettingKey(t *testing.T) {
	method, param, ok := collection.SplitRunSettingKey("file.remove.check_forbidden")
	if !ok || method != "file.remove" || param != "check_forbidden" {
		t.Errorf("= %q, %q, %v", method, param, ok)
	}
}
