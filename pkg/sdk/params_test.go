package sdk_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestStringParam(t *testing.T) {
	params := map[string]any{"present": "value", "wrong-type": 42, "nil": nil}

	tests := []struct {
		key  string
		want string
	}{
		{key: "present", want: "value"},
		{key: "absent", want: ""},
		// A value of the wrong type reads as absent rather than as its
		// rendering, so a module never silently acts on "42" when the
		// author wrote a number where a string belonged.
		{key: "wrong-type", want: ""},
		{key: "nil", want: ""},
	}
	for _, tc := range tests {
		if got := sdk.StringParam(params, tc.key); got != tc.want {
			t.Errorf("StringParam(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestBoolParam(t *testing.T) {
	params := map[string]any{"yes": true, "no": false, "stringy": "true", "numeric": 1}

	tests := []struct {
		key  string
		want bool
	}{
		{key: "yes", want: true},
		{key: "no", want: false},
		{key: "absent", want: false},
		// The string "true" is deliberately NOT accepted. YAML already
		// decodes an unquoted true into a bool, so a string here means the
		// author quoted it, and accepting it would eventually mean
		// honoring a quoted "false" as true-ish. One of the parameters read
		// this way turns off host key verification.
		{key: "stringy", want: false},
		{key: "numeric", want: false},
	}
	for _, tc := range tests {
		if got := sdk.BoolParam(params, tc.key); got != tc.want {
			t.Errorf("BoolParam(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestBoolParamOr covers the parameters that default to true, where
// absent and explicitly-false are different answers BoolParam cannot
// tell apart.
func TestBoolParamOr(t *testing.T) {
	params := map[string]any{"off": false, "on": true, "bad": "yes", "nil": nil}

	tests := []struct {
		name     string
		key      string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{name: "absent takes the fallback", key: "absent", fallback: true, want: true},
		{name: "absent takes a false fallback too", key: "absent", fallback: false, want: false},
		{name: "an explicit false beats a true fallback", key: "off", fallback: true, want: false},
		{name: "an explicit true is honored", key: "on", fallback: false, want: true},
		{name: "nil is absent", key: "nil", fallback: true, want: true},
		// Refused rather than treated as absent: a task that wrote a value
		// meant something by it, and quietly using the default would run
		// the opposite of what was asked.
		{name: "a non-boolean is refused", key: "bad", fallback: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sdk.BoolParamOr(params, tc.key, tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected a refusal for %q", tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("BoolParamOr(%q, %v) = %v, want %v", tc.key, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestStringSlice(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]any
		wantValues  []string
		wantPresent bool
		wantErr     string
	}{
		{
			name:        "absent",
			params:      map[string]any{},
			wantPresent: false,
		},
		{
			// An explicit empty list is PRESENT, which is what lets a
			// caller tell "the author wrote an empty argv" apart from "the
			// author wrote no argv at all". They deserve different errors.
			name:        "an empty list is present",
			params:      map[string]any{"argv": []any{}},
			wantValues:  []string{},
			wantPresent: true,
		},
		{
			name:        "strings",
			params:      map[string]any{"argv": []any{"a", "b"}},
			wantValues:  []string{"a", "b"},
			wantPresent: true,
		},
		{
			name:        "not a list at all",
			params:      map[string]any{"argv": "a b"},
			wantPresent: true,
			wantErr:     "must be a list of strings",
		},
		{
			// The one that matters across tiers: a bare 8080 in YAML
			// arrives as an int on the Crawl tier and a float64 across the
			// Runner's JSON subprocess boundary, so rendering it would
			// produce two different command lines depending on which tier
			// ran the task.
			name:        "a number is refused by index",
			params:      map[string]any{"argv": []any{"listen", 8080}},
			wantPresent: true,
			wantErr:     "argv[1] is int, not a string",
		},
		{
			name:        "nil is absent",
			params:      map[string]any{"argv": nil},
			wantPresent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, present, err := sdk.StringSlice(tc.params, "argv")
			if present != tc.wantPresent {
				t.Errorf("present = %v, want %v", present, tc.wantPresent)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.wantValues) {
				t.Fatalf("values = %v, want %v", got, tc.wantValues)
			}
			for i := range got {
				if got[i] != tc.wantValues[i] {
					t.Errorf("values[%d] = %q, want %q", i, got[i], tc.wantValues[i])
				}
			}
		})
	}
}

func TestIntParam(t *testing.T) {
	params := map[string]any{
		"int-form":   1000,
		"int64-form": int64(1001),
		"float-form": float64(1002),
		"fractional": 1003.5,
		"stringy":    "1004",
		"nil":        nil,
	}

	tests := []struct {
		name        string
		key         string
		want        int
		wantPresent bool
		wantErr     string
	}{
		{name: "absent", key: "absent", wantPresent: false},
		{name: "nil is absent", key: "nil", wantPresent: false},
		{name: "int from YAML on the Crawl tier", key: "int-form", want: 1000, wantPresent: true},
		{name: "int64 from a wide-integer decoder", key: "int64-form", want: 1001, wantPresent: true},
		{name: "float64 across the Runner's JSON boundary", key: "float-form", want: 1002, wantPresent: true},
		{name: "a fractional float is refused, not truncated", key: "fractional", wantPresent: true, wantErr: "not a whole number"},
		{name: "a string is refused rather than parsed", key: "stringy", wantPresent: true, wantErr: "not a whole number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, present, err := sdk.IntParam(params, tc.key)
			if present != tc.wantPresent {
				t.Errorf("present = %v, want %v", present, tc.wantPresent)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRequiredStringParam(t *testing.T) {
	got, err := sdk.RequiredStringParam(map[string]any{"path": "/etc/hosts"}, "path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/etc/hosts" {
		t.Errorf("got %q, want %q", got, "/etc/hosts")
	}

	// Both absent and empty are refusals, and the message names the key,
	// because this is the error a runbook author sees and the key is the
	// only thing they can act on.
	for _, params := range []map[string]any{nil, {"path": ""}, {"other": "x"}} {
		if _, err := sdk.RequiredStringParam(params, "path"); err == nil {
			t.Errorf("params %v: expected a refusal", params)
		} else if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %v, want it to name the key", params, err)
		}
	}
}
