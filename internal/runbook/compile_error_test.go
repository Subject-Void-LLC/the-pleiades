// Tests that the directory source returns a runbook it read and could not
// build as a CompileError and nothing else as one, and that the message a
// caller may show is the builder's own, on one line, escaped and bounded,
// naming no path.
package runbook_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// TestDirSource_ARunbookThatDoesNotBuildIsACompileError walks the ways a file
// can fail to build through the real source and builder: each is a
// CompileError whose message carries the builder's text and never the
// directory, from Get and GetDAG alike.
func TestDirSource_ARunbookThatDoesNotBuildIsACompileError(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"a handler taking every lock": {
			body: "id: x\ntasks:\n  - name: step\n    noop: {}\n    notify: restart\nhandlers:\n  - name: restart\n    lock_acquisition: all_at_plan_time\n    noop: {}\n",
			want: `handler "restart"`,
		},
		"malformed YAML": {
			body: "this: [is not: a runbook\n",
			want: "yaml",
		},
		"a list of tasks": {
			body: "- name: step\n  noop: {}\n",
			want: "list of tasks",
		},
		"an import with no directory to read from": {
			body: "id: x\ntasks:\n  - name: pull\n    import_tasks:\n      file: /etc/passwd\n",
			want: "import_tasks",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			src, err := runbook.NewDirSource(dir)
			if err != nil {
				t.Fatal(err)
			}
			_, getErr := src.Get(t.Context(), "x")
			_, dagErr := src.GetDAG(t.Context(), "x")
			for _, err := range []error{getErr, dagErr} {
				var notCompiled *runbook.CompileError
				if !errors.As(err, &notCompiled) {
					t.Fatalf("%v is not a CompileError", err)
				}
				if notCompiled.ID != "x" || errors.Is(err, runbook.ErrNotFound) {
					t.Errorf("CompileError names %q and is ErrNotFound %v", notCompiled.ID, errors.Is(err, runbook.ErrNotFound))
				}
				message := notCompiled.Message()
				if !strings.HasPrefix(message, `runbook "x" does not compile: `) || !strings.Contains(message, tc.want) {
					t.Errorf("Message = %q, want the builder's text saying %q", message, tc.want)
				}
				if strings.Contains(message, dir) || strings.Contains(err.Error(), dir) {
					t.Errorf("the error names the runbook directory: %q / %q", message, err)
				}
				if !strings.HasPrefix(err.Error(), `failed to compile runbook "x": `) {
					t.Errorf("Error = %q, want the source's own wording kept", err)
				}
			}
		})
	}
}

// TestDirSource_ALookupOrStorageErrorIsNotACompileError: a missing runbook, an
// id the source refuses and a file it cannot read are each some other error.
func TestDirSource_ALookupOrStorageErrorIsNotACompileError(t *testing.T) {
	dir := t.TempDir()
	unreadable := writeRunbook(t, dir, "unreadable", "noop")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", "../escape", "unreadable"} {
		_, err := src.Get(t.Context(), id)
		if err == nil {
			// Root reads a file whatever its mode.
			t.Logf("the source read %q, so this case proves nothing here", id)
			continue
		}
		var notCompiled *runbook.CompileError
		if errors.As(err, &notCompiled) {
			t.Errorf("Get(%q) = %v, a CompileError; want a lookup or storage error", id, err)
		}
	}
}

// TestCompileError_UnwrapsToTheBuildersError: a caller testing for a build
// error of its own (a file that is a list of tasks) still reaches it.
func TestCompileError_UnwrapsToTheBuildersError(t *testing.T) {
	err := error(&runbook.CompileError{ID: "x", Err: engine.ErrTaskList})
	if !errors.Is(err, engine.ErrTaskList) {
		t.Errorf("errors.Is(%v, ErrTaskList) = false", err)
	}
}

// TestCompileError_MessageIsOneEscapedBoundedLine: the builder quotes what the
// author wrote, so a line break, an escape sequence and a direction override
// are shown as visible escapes, and a megabyte of text is cut, at a character
// boundary, with its length.
func TestCompileError_MessageIsOneEscapedBoundedLine(t *testing.T) {
	hostile := &runbook.CompileError{ID: "x", Err: errors.New("task \"a\x1b[2J\nb\xe2\x80\xae\": bad\r\tkey")}
	message := hostile.Message()
	for _, raw := range []string{"\x1b", "\n", "\r", "\t", "\xe2\x80\xae"} {
		if strings.Contains(message, raw) {
			t.Errorf("Message %q holds a raw %q", message, raw)
		}
	}
	for _, shown := range []string{`\x1b`, `\n`, `\r`, `\t`, `\` + `u202e`} {
		if !strings.Contains(message, shown) {
			t.Errorf("Message %q does not show %s", message, shown)
		}
	}

	long := &runbook.CompileError{ID: "x", Err: errors.New("a" + strings.Repeat("é", 1<<19))}
	message = long.Message()
	if len(message) > runbook.MaxCompileMessage || len(message) < runbook.MaxCompileMessage-8 || !strings.HasSuffix(message, "... (cut from 1048577 bytes)") {
		t.Errorf("a megabyte of text gave a %d byte message ending %q", len(message), message[len(message)-40:])
	}
	if strings.Contains(message, `\x`) || !utf8.ValidString(message) {
		t.Errorf("the cut split a character: %q", message[len(message)-60:])
	}
}

// TestCompileError_MessageIsCutBetweenEscapes: text that is all escapes is cut
// on the escaped form, so the whole sentence stays inside the bound and ends
// on a whole escape. 2048 escape characters are 8192 bytes once escaped.
func TestCompileError_MessageIsCutBetweenEscapes(t *testing.T) {
	message := (&runbook.CompileError{ID: "x", Err: errors.New(strings.Repeat("\x1b", 2048))}).Message()
	if len(message) > runbook.MaxCompileMessage {
		t.Fatalf("the message is %d bytes, over the bound of %d", len(message), runbook.MaxCompileMessage)
	}
	const prefix, note = `runbook "x" does not compile: `, "... (cut from 2048 bytes)"
	if !strings.HasPrefix(message, prefix) || !strings.HasSuffix(message, note) {
		t.Fatalf("message %q...%q lacks its prefix or its note", message[:40], message[len(message)-40:])
	}
	body := strings.TrimSuffix(strings.TrimPrefix(message, prefix), note)
	if len(body)%4 != 0 || strings.ReplaceAll(body, `\x1b`, "") != "" {
		t.Errorf("the kept text is %d bytes and is not whole escapes: ...%q", len(body), body[len(body)-12:])
	}
	if len(body) < runbook.MaxCompileMessage-len(prefix)-len(note)-4 {
		t.Errorf("only %d bytes were kept, with room for more", len(body))
	}

	// Text that fits is not cut at all.
	if short := (&runbook.CompileError{ID: "x", Err: errors.New("bad key")}).Message(); short != prefix+"bad key" {
		t.Errorf("Message = %q", short)
	}
}

// TestCompileError_MessageMasksASecretShapedLiteral: a condition that does not
// compile is quoted back by the builder, through the real source. A value
// shaped like a credential that the author wrote into it is masked, and the
// rest of the message still says what is wrong.
func TestCompileError_MessageMasksASecretShapedLiteral(t *testing.T) {
	const keyID = "AKIA" + "IOSFODNN7EXAMPLE"
	dir := t.TempDir()
	body := "id: x\ntasks:\n  - name: step\n    when: 'token == \"" + keyID + "\" &&'\n    noop: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Get(t.Context(), "x")
	var notCompiled *runbook.CompileError
	if !errors.As(err, &notCompiled) {
		t.Fatalf("Get = %v, want a CompileError", err)
	}
	if !strings.Contains(err.Error(), keyID) {
		t.Fatalf("the builder's error %q does not quote the condition, so this test proves nothing", err)
	}
	message := notCompiled.Message()
	if strings.Contains(message, keyID) {
		t.Errorf("Message %q repeats the key id", message)
	}
	if !strings.Contains(message, "does not compile") || !strings.Contains(message, "token") {
		t.Errorf("Message %q no longer says what is wrong", message)
	}
}
