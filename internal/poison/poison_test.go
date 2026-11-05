// Tests of the checks the Controller's consumers share. The server-issued
// errors Unstorable reads are checked against a real PostgreSQL in
// internal/facts/entstore's TestSubscriber_ADataExceptionIsAcknowledgedAndOthersRedelivered.
package poison_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/Subject-Void-LLC/the-pleiades/internal/poison"
)

// TestCheckID names the reason an ID cannot name a job or device, and
// passes every ID the Controller or an operator writes, however long.
func TestCheckID(t *testing.T) {
	for id, want := range map[string]error{
		"01a1ada4-5cad-7aad-8647-6cb6b72c4927":            nil,
		"catalyst-center:https://dnac.example.com/api/v1": nil,
		"router-ü":                nil,
		strings.Repeat("d", 4096): nil,
		"":                        poison.ErrEmptyID,
		"web\x001":                poison.ErrControlCharacter,
		"web\n1":                  poison.ErrControlCharacter,
		"web\u00851":              poison.ErrControlCharacter,
		"web\xff":                 poison.ErrNotUTF8,
	} {
		if got := poison.CheckID(id); !errors.Is(got, want) || (want == nil) != (got == nil) {
			t.Errorf("CheckID(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestUnstorable_OnlyADataException: class 22 is acknowledged with its
// code, wrapped or not; class 23 and an error not from the server are not.
func TestUnstorable_OnlyADataException(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"a NUL in text":      {&pq.Error{Code: "22021"}, "22021"},
		"wrapped":            {fmt.Errorf("facts: %w", &pq.Error{Code: "22003"}), "22003"},
		"a unique violation": {&pq.Error{Code: "23505"}, ""},
		"a code too short":   {&pq.Error{Code: "22"}, ""},
		"not the server's":   {errors.New("driver: bad connection"), ""},
		"no error":           {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			code, ok := poison.Unstorable(tc.err)
			if code != tc.code || ok != (tc.code != "") {
				t.Fatalf("Unstorable = %q, %v, want %q", code, ok, tc.code)
			}
		})
	}
}

// TestCleanText replaces each NUL and keeps every other character.
func TestCleanText(t *testing.T) {
	if got := poison.CleanText("plain"); got != "plain" {
		t.Errorf("CleanText changed text with no NUL: %q", got)
	}
	got := poison.CleanText("a\x00b\x00")
	want := "a" + string(utf8.RuneError) + "b" + string(utf8.RuneError)
	if got != want {
		t.Errorf("CleanText = %q, want %q", got, want)
	}
}

// TestDescribe never repeats a server's message for a data exception, and
// keeps any other error's text.
func TestDescribe(t *testing.T) {
	refused := &pq.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"hunter2\""}
	if got := poison.Describe(refused); strings.Contains(got, "hunter2") || !strings.Contains(got, "22P02") {
		t.Errorf("Describe(data exception) = %q, want its code and not its message", got)
	}
	if got := poison.Describe(errors.New("journal: unstorable outcome")); got != "journal: unstorable outcome" {
		t.Errorf("Describe(other) = %q", got)
	}
}
