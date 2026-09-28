// Tests for writeJSON: a document that decodes to what it was given and
// holds nothing a terminal acts on.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

func TestWriteJSONIsInertAndExact(t *testing.T) {
	// A C1 CSI, DEL, a right-to-left override, an escape sequence, and
	// characters HTML escaping would have rewritten.
	text := "red\u009b31m del\x7f rtl\u202eevil esc\x1b[2J >=1.0.0 & <b> café 🙂"
	var buf bytes.Buffer
	if err := writeJSON(&buf, map[string]string{"stdout": text}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, r := range out {
		if termsafe.Unsafe(r) && r != '\n' {
			t.Fatalf("the document holds %U raw:\n%s", r, out)
		}
	}
	for _, want := range []string{`\u009b`, `\u007f`, `\u202e`, `\u001b`, ">=1.0.0 & <b>", "café 🙂"} {
		if !strings.Contains(out, want) {
			t.Errorf("the document lacks %s:\n%s", want, out)
		}
	}
	var back map[string]string
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || back["stdout"] != text {
		t.Fatalf("decoded %q, %v; want %q", back["stdout"], err, text)
	}
	if !strings.HasSuffix(out, "}\n") || !strings.Contains(out, "\n  \"stdout\"") {
		t.Errorf("not indented or not newline-terminated:\n%q", out)
	}
}

func TestWriteJSONRefusesWhatJSONCannotWrite(t *testing.T) {
	if err := writeJSON(&bytes.Buffer{}, map[string]any{"c": make(chan int)}); err == nil {
		t.Fatal("a channel was written")
	}
}

// FuzzWriteJSON holds writeJSON to its two promises for any string: the
// document decodes to the valid-UTF-8 form of the string, and it holds no
// character a terminal acts on.
func FuzzWriteJSON(f *testing.F) {
	for _, seed := range []string{"plain", "\u009b[31m", "\x7f", "\u202e", "\xff\xfe", "\r\n\t", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		var buf bytes.Buffer
		if err := writeJSON(&buf, text); err != nil {
			t.Fatal(err)
		}
		for _, r := range buf.String() {
			if termsafe.Unsafe(r) && r != '\n' {
				t.Fatalf("%U raw in %q", r, buf.String())
			}
		}
		var back string
		if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
			t.Fatalf("does not decode: %v: %q", err, buf.String())
		}
		if want := strings.ToValidUTF8(text, "\uFFFD"); utf8.ValidString(text) && back != want {
			t.Fatalf("decoded %q, want %q", back, want)
		}
	})
}
