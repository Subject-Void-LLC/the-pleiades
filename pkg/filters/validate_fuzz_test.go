package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzIsValidFQDN proves IsValidFQDN never panics on arbitrary input,
// per this phase's own Fuzz/Stress Test checklist item to fuzz every
// format validator against malformed input.
func FuzzIsValidFQDN(f *testing.F) {
	seeds := []string{
		"host1.example.com", "host1.example.com.", "host1", "", "-bad.example.com",
		"bad-.example.com", "..", strings.Repeat("a", 300),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidFQDN(s)
	})
}

// FuzzIsValidEmail mirrors FuzzIsValidFQDN for IsValidEmail.
func FuzzIsValidEmail(f *testing.F) {
	seeds := []string{
		"user@example.com", "Foo Bar <user@example.com>", "", "@", "user@",
		"@example.com", "user@@example.com", `"quoted"@example.com`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidEmail(s)
	})
}

// FuzzIsValidUUID mirrors FuzzIsValidFQDN for IsValidUUID.
func FuzzIsValidUUID(f *testing.F) {
	seeds := []string{
		"123e4567-e89b-12d3-a456-426614174000", "urn:uuid:123e4567-e89b-12d3-a456-426614174000",
		"", "not-a-uuid", "{123e4567-e89b-12d3-a456-426614174000}",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidUUID(s)
	})
}

// FuzzIsValidBase64 mirrors FuzzIsValidFQDN for IsValidBase64.
func FuzzIsValidBase64(f *testing.F) {
	seeds := []string{"aGVsbG8gd29ybGQ=", "", "not base64!!", "====", "AA=="}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidBase64(s)
	})
}

// FuzzIsValidJSON mirrors FuzzIsValidFQDN for IsValidJSON.
func FuzzIsValidJSON(f *testing.F) {
	seeds := []string{`{"a":1}`, `[1,2,3]`, "", "{", `{"a":1,}`, `"unterminated`}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidJSON(s)
	})
}

// FuzzIsValidYAML mirrors FuzzIsValidFQDN for IsValidYAML.
func FuzzIsValidYAML(f *testing.F) {
	seeds := []string{"a: 1\n", "- 1\n- 2\n", "", "a:\n\tb: 1\n", "a: [1, 2\n"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		filters.IsValidYAML(s)
	})
}
