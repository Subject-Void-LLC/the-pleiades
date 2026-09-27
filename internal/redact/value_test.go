// Tests for Value: each shape a method's stats take, masked as data.
package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// reported is a struct a method might put in its stats, whose fields only
// a conversion to generic data can reach.
type reported struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func TestValueMasksEveryShape(t *testing.T) {
	const secret = "hunter2-Secret"
	when := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)
	in := map[string]any{
		"stdout":   "the password is " + secret + "\n",
		"password": "anything at all, even with no known secret in it",
		"Token":    42,
		"count":    3,
		"exit":     true,
		"pin":      4242,
		"diff": map[string]any{
			"before": map[string]any{"running": true, "note": secret},
			"after":  map[string]any{"running": false},
		},
		"lines":    []string{"ok", secret},
		"list":     []any{secret, 7, map[string]any{"api_key": "k"}},
		"vms":      []map[string]any{{"name": "win-lab", "secret": "s"}},
		"env":      map[string]string{"HOME": "/root", "credential": "c", "X": secret},
		secret:     "a key that is itself a secret",
		"struct":   reported{User: "admin", Password: "pw"},
		"when":     when,
		"nothing":  nil,
		"url":      "https://admin:" + "pa55@example.test/x",
		"template": "Bearer abcdefghijklmnop",
	}
	before, _ := json.Marshal(in)

	out, ok := Value([]string{secret, "4242"}, in).(map[string]any)
	if !ok {
		t.Fatalf("a map came back as %T", out)
	}
	text, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secret, "anything at all", `"k"`, `"s"`, `"c"`, `"pw"`, "pa55", "abcdefghijklmnop", "4242"} {
		if strings.Contains(string(text), leak) {
			t.Errorf("%q survived masking: %s", leak, text)
		}
	}
	for key, want := range map[string]any{"password": Marker, "Token": Marker, "count": 3, "exit": true, "when": when, "nothing": nil} {
		if got := out[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := out["lines"].([]string); !ok {
		t.Errorf("a []string came back as %T", out["lines"])
	}
	if env, ok := out["env"].(map[string]string); !ok || env["HOME"] != "/root" || env["credential"] != Marker {
		t.Errorf("env = %#v", out["env"])
	}
	if vms, ok := out["vms"].([]map[string]any); !ok || vms[0]["name"] != "win-lab" {
		t.Errorf("vms = %#v", out["vms"])
	}
	if s, ok := out["struct"].(map[string]any); !ok || s["user"] != "admin" || s["password"] != Marker {
		t.Errorf("a struct came back as %#v", out["struct"])
	}
	if diff := out["diff"].(map[string]any); diff["before"].(map[string]any)["running"] != true {
		t.Errorf("diff = %#v", diff)
	}
	if after, _ := json.Marshal(in); string(after) != string(before) {
		t.Error("Value changed its input")
	}
}

func TestValueScalarsAndDepth(t *testing.T) {
	if got := Value(nil, "plain"); got != "plain" {
		t.Errorf("a plain string = %#v", got)
	}
	if got := Value(nil, 3.5); got != 3.5 {
		t.Errorf("a float = %#v", got)
	}
	if got := Value([]string{"12345"}, uint16(12345)); got != Text([]string{"12345"}, "12345") || got == "12345" {
		t.Errorf("a secret number = %#v", got)
	}
	// A value JSON cannot write is masked as its text.
	if got, ok := Value(nil, make(chan int)).(string); !ok || got == "" {
		t.Errorf("a channel = %#v", got)
	}
	// A value deeper than maxDepth is written as text rather than walked.
	deep := any("bottom hunter2-x")
	for range maxDepth + 5 {
		deep = []any{deep}
	}
	if text, _ := json.Marshal(Value([]string{"hunter2-x"}, deep)); strings.Contains(string(text), "hunter2-x") {
		t.Errorf("a deep value leaked: %s", text)
	}
}
