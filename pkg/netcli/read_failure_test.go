// Tests for a failed read's output and a question's secret answers.
package netcli

import (
	"errors"
	"io"
	"testing"
)

// TestReadFailure_KeepsOutputOutOfItsText: the output is there for the
// caller, never in the error's text, and the cause still matches.
func TestReadFailure_KeepsOutputOutOfItsText(t *testing.T) {
	err := readFailure(io.EOF, "pre-shared-key Kept-Out-1", "")
	if out, ok := LastOutput(err); !ok || out != "pre-shared-key Kept-Out-1" {
		t.Fatalf("LastOutput = %q, %v", out, ok)
	}
	if !errors.Is(err, io.EOF) || err.Error() != "netcli: EOF" {
		t.Fatalf("err = %v", err)
	}
	if _, ok := LastOutput(errors.New("plain")); ok {
		t.Error("a plain error has output")
	}
}

// TestWithoutSecretAnswers: an echoed secret answer is replaced, a written
// one is kept.
func TestWithoutSecretAnswers(t *testing.T) {
	prompts := []Prompt{{Answer: "Echoed-Secret-9", Secret: true}, {Answer: "yes"}}
	if got := withoutSecretAnswers("Confirm Echoed-Secret-9 [yes/no]?", prompts); got != "Confirm "+SecretAnswer+" [yes/no]?" {
		t.Fatalf("got %q", got)
	}
}
