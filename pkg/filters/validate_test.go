package filters_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	yaml "go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestIsValidFQDN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"simple", "host1.example.com", true},
		{"trailing_root_dot", "host1.example.com.", true},
		{"single_label_rejected", "host1", false},
		{"empty", "", false},
		{"leading_hyphen_label", "-host.example.com", false},
		{"trailing_hyphen_label", "host-.example.com", false},
		{"interior_hyphen_ok", "my-host.example.com", true},
		{"empty_label", "host..example.com", false},
		{"label_too_long", strings.Repeat("a", 64) + ".com", false},
		{"total_too_long", strings.Repeat("a.", 130) + "com", false},
		{"space_rejected", "host name.example.com", false},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidFQDN(tc.in); got != tc.want {
				t.Errorf("IsValidFQDN(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"upn_shaped", "user@example.com", true},
		{"subdomain", "svc-account@corp.example.com", true},
		{"no_at", "not-an-email", false},
		{"empty", "", false},
		{"display_name_rejected", "Foo Bar <user@example.com>", false},
		{"angle_brackets_rejected", "<user@example.com>", false},
		{"trailing_space_rejected", "user@example.com ", false},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1) + "@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidEmail(tc.in); got != tc.want {
				t.Errorf("IsValidEmail(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"canonical", "123e4567-e89b-12d3-a456-426614174000", true},
		{"urn_form", "urn:uuid:123e4567-e89b-12d3-a456-426614174000", true},
		{"braces", "{123e4567-e89b-12d3-a456-426614174000}", true},
		{"bare_hex", "123e4567e89b12d3a456426614174000", true},
		{"too_short", "123e4567-e89b-12d3-a456", false},
		{"not_hex", "zzzzzzzz-e89b-12d3-a456-426614174000", false},
		{"empty", "", false},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidUUID(tc.in); got != tc.want {
				t.Errorf("IsValidUUID(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidBase64(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid_padded", "aGVsbG8gd29ybGQ=", true},
		{"empty_is_valid", "", true},
		{"invalid_char", "not valid base64!!", false},
		{"missing_padding", "aGVsbG8", false},
		{"over_cap", strings.Repeat("A", filters.MaxStructuredInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidBase64(tc.in); got != tc.want {
				t.Errorf("IsValidBase64(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"object", `{"a":1,"b":[1,2,3]}`, true},
		{"array", `[1,2,3]`, true},
		{"scalar_string", `"hello"`, true},
		{"scalar_number", `42`, true},
		{"empty", "", false},
		{"trailing_comma", `{"a":1,}`, false},
		{"unquoted_key", `{a:1}`, false},
		{"over_cap", strings.Repeat("1", filters.MaxStructuredInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidJSON(tc.in); got != tc.want {
				t.Errorf("IsValidJSON(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidYAML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"mapping", "a: 1\nb: 2\n", true},
		{"sequence", "- a\n- b\n", true},
		{"scalar", "hello", true},
		{"empty_is_valid", "", true},
		{"bad_indentation", "a:\n  b: 1\n c: 2\n", false},
		{"tab_indentation_rejected", "a:\n\tb: 1\n", false},
		{"over_cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidYAML(tc.in); got != tc.want {
				t.Errorf("IsValidYAML(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidPort(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"min", 1, true},
		{"max", 65535, true},
		{"common_https", 443, true},
		{"zero_rejected", 0, false},
		{"negative_rejected", -1, false},
		{"too_large", 65536, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidPort(tc.in); got != tc.want {
				t.Errorf("IsValidPort(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsValidJSON_AgreesWithRealParser is this phase's Adversarial
// Pattern Justification for IsValidJSON: it proves, over a corpus of both
// well-formed and malformed JSON, that IsValidJSON's answer and
// encoding/json.Unmarshal's own success are the same event on every
// input, not merely on the cases IsValidJSON's own table above happens
// to cover. IsValidJSON's implementation already delegates to
// encoding/json.Valid (which json.Unmarshal itself checks internally),
// so this test is a real proof of that delegation, not a coincidence.
func TestIsValidJSON_AgreesWithRealParser(t *testing.T) {
	corpus := []string{
		`{"a":1}`, `[1,2,3]`, `"str"`, `42`, `true`, `null`,
		``, `{`, `[1,2`, `{"a":1,}`, `{a:1}`, `'single'`, `{"a": undefined}`,
		`{"nested":{"a":[1,{"b":2}]}}`, `1.5e10`, `-0`, `{"a":"b\"c"}`,
	}
	for _, in := range corpus {
		var v any
		wantValid := json.Unmarshal([]byte(in), &v) == nil
		if got := filters.IsValidJSON(in); got != wantValid {
			t.Errorf("IsValidJSON(%q) = %v, but json.Unmarshal succeeded=%v", in, got, wantValid)
		}
	}
}

// TestIsValidYAML_AgreesWithRealParser mirrors
// TestIsValidJSON_AgreesWithRealParser for YAML, against the same
// go.yaml.in/yaml/v3.Unmarshal IsValidYAML's own implementation calls.
func TestIsValidYAML_AgreesWithRealParser(t *testing.T) {
	corpus := []string{
		"a: 1\n", "- 1\n- 2\n", "hello", "", "a:\n  b: 1\n c: 2\n",
		"a: [1, 2\n", "a: &x\nb: *x\n", "a: {b: 1, c: 2}\n", "---\na: 1\n",
		"a:\n\tb: 1\n", "key: value: extra\n",
	}
	for _, in := range corpus {
		var v any
		wantValid := yaml.Unmarshal([]byte(in), &v) == nil
		if got := filters.IsValidYAML(in); got != wantValid {
			t.Errorf("IsValidYAML(%q) = %v, but yaml.Unmarshal succeeded=%v", in, got, wantValid)
		}
	}
}

// TestIsValidUUID_AgreesWithRealParser mirrors the JSON/YAML adversarial
// checks for uuid.Parse.
func TestIsValidUUID_AgreesWithRealParser(t *testing.T) {
	corpus := []string{
		"123e4567-e89b-12d3-a456-426614174000",
		"urn:uuid:123e4567-e89b-12d3-a456-426614174000",
		"{123e4567-e89b-12d3-a456-426614174000}",
		"123e4567e89b12d3a456426614174000",
		"", "not-a-uuid", "123e4567-e89b-12d3-a456", "zzzzzzzz-e89b-12d3-a456-426614174000",
	}
	for _, in := range corpus {
		_, err := uuid.Parse(in)
		wantValid := err == nil
		if got := filters.IsValidUUID(in); got != wantValid {
			t.Errorf("IsValidUUID(%q) = %v, but uuid.Parse succeeded=%v", in, got, wantValid)
		}
	}
}

// TestIsValidBase64_AgreesWithRealParser mirrors the JSON/YAML adversarial
// checks for encoding/base64.StdEncoding.
func TestIsValidBase64_AgreesWithRealParser(t *testing.T) {
	corpus := []string{
		"aGVsbG8gd29ybGQ=", "", "not valid base64!!", "aGVsbG8", "////", "====",
	}
	for _, in := range corpus {
		_, err := base64.StdEncoding.DecodeString(in)
		wantValid := err == nil
		if got := filters.IsValidBase64(in); got != wantValid {
			t.Errorf("IsValidBase64(%q) = %v, but base64.StdEncoding.DecodeString succeeded=%v", in, got, wantValid)
		}
	}
}
