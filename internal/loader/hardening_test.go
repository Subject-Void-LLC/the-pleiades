//go:build unix

// Package loader: Phase 45's hardening tests: everything a program says is
// untrusted input.
package loader

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// Phase 45's Schema/Injection Hardening, as tests: every boundary an
// external program's output crosses, against Phase 39's categories
// (deserialization, command execution, paths, log and error injection,
// secrets). A program is third-party code, and everything it says is
// untrusted input.

// esc is the escape character a terminal starts a control sequence with,
// and rlo the right-to-left override (U+202E) "Trojan Source" uses, both
// written as byte escapes so neither is ever a raw character in this
// file.
const (
	esc = "\x1b"
	rlo = "\xe2\x80\xae"
)

// TestHardening_NoRequestFieldReachesArgv covers command execution: a
// program's argument list is the one command word, whatever the task's
// params and the device carry, shell syntax and flags included.
func TestHardening_NoRequestFieldReachesArgv(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.argv.run", false, captureRequest(record))
	d := loadOne(t, dir, "loadertest.argv.run", recordingOptions(record))

	hostile := map[string]any{
		"message": "$(touch /tmp/pwned); `id`; --describe -rf /\ninvoke describe",
		"--flag":  "-x",
	}
	if _, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), hostile); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if args := readFile(t, filepath.Join(record, "args.txt")); args != "invoke\n" {
		t.Errorf("the program was started with arguments %q, want exactly the one word invoke", args)
	}
}

// TestHardening_HostileMethodNamesAreRefused covers paths and names: a
// described name with a path separator, a traversal, a NUL, a newline, an
// escape sequence or an override is refused before it is used anywhere.
func TestHardening_HostileMethodNamesAreRefused(t *testing.T) {
	for _, name := range []string{
		"acme.motd/../../etc",
		"../acme.motd",
		"acme..motd",
		"acme.motd\x00run",
		"acme.motd\nrun",
		"acme.motd" + esc + "[2J",
		"acme.motd" + rlo + "nur",
		"ACME.MOTD",
		"acme",
	} {
		m := external.DescribedMethod{Name: name, Manifest: implemented(false)}
		if _, err := validateMethod(m, "dev", nil); err == nil {
			t.Errorf("validateMethod accepted the name %q", name)
		} else if strings.Contains(err.Error(), esc) || strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), rlo) {
			t.Errorf("the refusal of %q carries the raw character: %q", name, err.Error())
		}
	}
}

// TestHardening_DescriptionTextCannotControlATerminal covers log and
// display injection in the description: a manifest string holding an
// escape sequence, a carriage return or an override, at any depth, is
// refused, naming where it was. The description is shown by `pleiades
// doc`, the Controller and the approve prompt.
func TestHardening_DescriptionTextCannotControlATerminal(t *testing.T) {
	for field, set := range map[string]func(*collection.Manifest){
		"doc.summary":             func(m *collection.Manifest) { m.Doc.Summary = "safe" + esc + "[2Jforged" },
		"doc.params[0].name":      func(m *collection.Manifest) { m.Doc.Params = []collection.Param{{Name: "p" + rlo, Type: "string"}} },
		"reversibility.notes":     func(m *collection.Manifest) { m.Reversibility.Notes = "over\rwrite" },
		"supportedTransports[0]":  func(m *collection.Manifest) { m.SupportedTransports = []string{"ssh" + esc} },
		"doc.examples[0].runbook": func(m *collection.Manifest) { m.Doc.Examples = []collection.Example{{Name: "x", RunbookYAML: "a\x07"}} },
	} {
		m := implemented(false)
		set(&m)
		_, err := validateMethod(external.DescribedMethod{Name: "acme.text.run", Manifest: m}, "dev", nil)
		if err == nil || !strings.Contains(err.Error(), "a description may not") {
			t.Errorf("%s: validateMethod = %v, want the description refused", field, err)
		}
	}
	// A newline and a tab in a long description are fine: it has paragraphs.
	m := implemented(false)
	m.Doc.Description = "First paragraph.\n\n\tSecond, indented."
	if _, err := validateMethod(external.DescribedMethod{Name: "acme.text.ok", Manifest: m}, "dev", nil); err != nil {
		t.Errorf("a multi-line description was refused: %v", err)
	}
}

// TestHardening_ProgramOutputCannotForgeLines covers error injection: a
// program's own failure message, and its stderr quoted into an error,
// reach the error with every newline and control character escaped, so
// the error cannot print lines that look like the run's own output.
func TestHardening_ProgramOutputCannotForgeLines(t *testing.T) {
	for name, body := range map[string]string{
		"its own error": `printf '{"error":"boom\\n  tasks[1]: ok\\n\\u001b[2Jrun complete"}' >&3`,
		"its stderr":    `printf 'boom\n  tasks[1]: ok\n` + esc + `[2Jrun complete' >&2; exit 1`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := programDir(t)
			oneMethodProgram(t, dir, "loadertest.forge.run", false, body)
			d := loadOne(t, dir, "loadertest.forge.run", testOptions())
			_, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
			if err == nil {
				t.Fatal("expected the program's failure")
			}
			msg := err.Error()
			if strings.Contains(msg, "\n") || strings.Contains(msg, esc) {
				t.Errorf("the error carries a raw newline or escape: %q", msg)
			}
			if !strings.Contains(msg, "boom") {
				t.Errorf("the error lost what the program said: %q", msg)
			}
		})
	}
}

// TestHardening_AStatNameCannotForgeLines covers stat names: a response
// whose stat name holds a newline or a control character is refused.
// (Stat values may span lines; every printer escapes them.)
func TestHardening_AStatNameCannotForgeLines(t *testing.T) {
	// JSON escapes for a newline, the escape character and a tab, spelled
	// out so no raw control character sits in this file.
	for _, key := range []string{`a\nb`, `a` + `\` + "u001bb", `a\tb`} {
		if _, err := decodeResponse([]byte(`{"changed":true,"facts":{"` + key + `":"v"}}`)); err == nil {
			t.Errorf("a stat named %s was accepted", key)
		}
	}
	if resp, err := decodeResponse([]byte(`{"changed":true,"facts":{"stdout":"line one\nline two"}}`)); err != nil || resp.Facts["stdout"] != "line one\nline two" {
		t.Errorf("a multi-line stat value = %v, %v; values may span lines", resp.Facts, err)
	}
}

// TestHardening_EverySecretIsMasked covers secrets: every value of the
// credential the program was handed is masked out of the error, including
// one that another value starts with, so masking the shorter first cannot
// leave the longer one's tail readable.
func TestHardening_EverySecretIsMasked(t *testing.T) {
	dir := programDir(t)
	oneMethodProgram(t, dir, "loadertest.mask.run", false,
		`printf '{"error":"password hunter2 and passphrase hunter2-extended-tail"}' >&3`)
	d := loadOne(t, dir, "loadertest.mask.run", testOptions())

	rc := newRecordingContext()
	rc.secrets = map[string]string{"password": "hunter2", "passphrase": "hunter2-extended-tail"}
	_, err := d.Invoke(t.Context(), rc, newSSHDevice(), nil)
	if err == nil {
		t.Fatal("expected the program's failure")
	}
	for _, leaked := range []string{"hunter2", "extended-tail", "-extended"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the error still shows %q: %q", leaked, err.Error())
		}
	}
}

// FuzzDecodeResponse covers deserialization from the loader's side: any
// bytes a program writes as its response decode or are refused, never
// panic, and an accepted response has no stat name that could forge a
// line.
func FuzzDecodeResponse(f *testing.F) {
	for _, seed := range []string{
		`{"changed":true,"facts":{"a":"b"}}`,
		`{"error":"no"}`,
		`{"facts":{"a\n":1}}`,
		`{"facts":{"k":{"nested":[1,2,{"x":null}]}}}`,
		`{} {}`,
		``,
		`{"changed":"yes"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		resp, err := decodeResponse(data)
		if err != nil {
			return
		}
		for key := range resp.Facts {
			if strings.ContainsAny(key, "\n\t\r"+esc) {
				t.Fatalf("an accepted response has the stat name %q", key)
			}
		}
	})
}

// FuzzParseApprovals covers the approval list's decoder: any content is
// accepted or refused whole, never panics, and everything accepted
// validates.
func FuzzParseApprovals(f *testing.F) {
	good := `{"version":1,"approvals":[{"program":"note","digest":"sha256:` + strings.Repeat("a", 64) + `","approved_by":"ops","approved_at":"2026-09-18T00:00:00Z"}]}`
	for _, seed := range []string{good, `{"version":1,"approvals":[]}`, `{"version":2}`, `[]`, ``, good + good} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		approvals, err := parseApprovals("fuzz", data)
		if err != nil {
			return
		}
		for _, a := range approvals {
			if err := a.validate(); err != nil {
				t.Fatalf("an accepted list holds an invalid approval %+v: %v", a, err)
			}
		}
	})
}

// TestHardening_CannotCheckIsNeverASuccess covers the "cannot check this
// call" flag from a program, which is third-party output like the rest: in
// a check it is the task's unchecked answer, with the reason escaped and
// masked like any of the program's text, and with no reason still an
// unchecked answer; whatever else the response claims (a change, stats),
// it is never read as a success; and answered to a real run it is a
// malformed response, not a skip.
func TestHardening_CannotCheckIsNeverASuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		check  bool
		reason string
	}{
		{"a check with a reason", `printf '{"cannot_check":true,"error":"no creates\\n  tasks[1]: ok, password hunter2"}' >&3`, true, `no creates\n  tasks[1]: ok, password ********`},
		{"a check with no reason", `printf '{"cannot_check":true}' >&3`, true, "the program gave no reason"},
		{"a check claiming a change beside it", `printf '{"cannot_check":true,"changed":true,"facts":{"k":"v"}}' >&3`, true, "the program gave no reason"},
		{"a real run", `printf '{"cannot_check":true,"changed":true}' >&3`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			oneMethodProgram(t, dir, "loadertest.partly.run", true, tc.body)
			d := loadOne(t, dir, "loadertest.partly.run", testOptions())
			method := d.Invoke
			if tc.check {
				method = d.Check
			}
			rc := newRecordingContext()
			rc.secrets["password"] = "hunter2"
			_, err := method(t.Context(), rc, newSSHDevice(), nil)
			if len(rc.stats) != 0 {
				t.Errorf("the response's stats were recorded: %v", rc.stats)
			}
			var cannot *collection.CannotCheckError
			switch {
			case err == nil:
				t.Fatal("a response carrying cannot_check was read as a success")
			case !tc.check:
				if errors.As(err, &cannot) || !strings.Contains(err.Error(), "cannot check a call that was not a check") {
					t.Errorf("a real run answered cannot-check = %v, want it refused as malformed", err)
				}
			case !errors.As(err, &cannot):
				t.Errorf("a check answered cannot-check = %v, want a CannotCheckError", err)
			case cannot.Reason != tc.reason:
				t.Errorf("reason = %q, want %q", cannot.Reason, tc.reason)
			}
		})
	}
}

// TestHardening_AReservedParamIsRefusedBeforeAnythingLoads covers the
// device-selector collision for a third party: a program declaring a
// parameter named target would have its datastore, path or anything else
// read by the engine as the device or tag to run on, so its description is
// refused before any method of it registers.
func TestHardening_AReservedParamIsRefusedBeforeAnythingLoads(t *testing.T) {
	for _, name := range collection.ReservedParams() {
		m := implemented(false)
		m.Doc.Params = []collection.Param{{Name: "content", Type: "string"}, {Name: name, Type: "string"}}
		_, err := validateMethod(external.DescribedMethod{Name: "acme.reserved.run", Manifest: m}, "dev", nil)
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "device or tag") {
			t.Errorf("validateMethod(param %q) = %v, want it refused naming the parameter", name, err)
		}
	}
}
