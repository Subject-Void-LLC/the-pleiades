// Tests for the secret reader.
//
// The terminal path cannot be exercised without a pseudo terminal and is
// two library calls with no logic of its own; the piped path carries every
// decision worth testing, and it is the one automation uses, which makes it
// the one most likely to be fed something unexpected.
package prompt

import (
	"strings"
	"testing"
)

func TestSecretFromReader(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"a plain line", "hunter2-and-more\n", "hunter2-and-more", false},
		{"no trailing newline", "hunter2-and-more", "hunter2-and-more", false},
		{
			// A value piped from a file written on Windows. Without the
			// trim, the stored password carries an invisible byte and every
			// later sign-in fails for a reason nobody can see.
			name:  "carriage return stripped",
			input: "hunter2-and-more\r\n",
			want:  "hunter2-and-more",
		},
		{
			// Only the first line. A caller that piped a whole file must
			// not get its second line silently appended.
			name:  "second line ignored",
			input: "the-password\nsomething else\n",
			want:  "the-password",
		},
		{
			// Interior spaces are part of a passphrase, so nothing is
			// trimmed except the line ending.
			name:  "spaces are preserved",
			input: "correct horse battery staple\n",
			want:  "correct horse battery staple",
		},
		{"empty line is refused", "\n", "", true},
		{"carriage return only is refused", "\r\n", "", true},
		{"no input at all is refused", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := secretFromReader(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("secretFromReader(%q) = %q, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("secretFromReader(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("secretFromReader(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSecretFromReader_AcceptsALongPassphrase covers the raised buffer.
//
// bufio's default line cap is 64 KiB, and a scanner that hit it would
// return an error rather than a truncated value, so this is a usability
// property rather than a security one. It is still worth a test: the
// failure would appear only for whoever actually uses a long passphrase.
func TestSecretFromReader_AcceptsALongPassphrase(t *testing.T) {
	long := strings.Repeat("a", 100_000)

	got, err := secretFromReader(strings.NewReader(long + "\n"))
	if err != nil {
		t.Fatalf("secretFromReader(100k) error = %v", err)
	}
	if got != long {
		t.Errorf("a %d byte passphrase came back as %d bytes", len(long), len(got))
	}
}

// TestSecretFromReader_NeverReturnsAValueWithAnError pins the contract the
// callers rely on: a failure yields the empty string, so a caller that
// ignored the error would store nothing rather than something partial.
func TestSecretFromReader_NeverReturnsAValueWithAnError(t *testing.T) {
	for _, input := range []string{"", "\n", "\r\n"} {
		got, err := secretFromReader(strings.NewReader(input))
		if err != nil && got != "" {
			t.Errorf("secretFromReader(%q) returned %q alongside an error", input, got)
		}
	}
}
