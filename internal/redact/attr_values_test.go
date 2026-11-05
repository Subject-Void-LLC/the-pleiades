// Tests that a registered secret never reaches a log line through a value
// that is not a string, or a leaf under a secret-named group, through the
// standard library's own text and JSON handlers, as every cmd/ binary builds
// them (HandlerOptions).
package redact_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// Two secrets of the same length and shape: a masked line must come out the
// same for either, which a line still carrying a secret in any spelling (its
// text, its decimal bytes, its base64) cannot (LESSONS 195).
const (
	plantedA = "Zq7-planted-device-secret-41"
	plantedB = "Wm3-planted-device-secret-98"
)

// stringer is a fmt.Stringer whose text carries a secret.
type stringer struct{ secret string }

func (s stringer) String() string { return "token=" + s.secret }

// token is a named string type, which slog holds as KindAny, not KindString.
type token string

// carrier is a struct a JSON handler would marshal whole.
type carrier struct {
	Host   string
	Output string
}

// panicky is a value whose MarshalJSON panics, as a buggy one can.
type panicky struct{}

func (panicky) MarshalJSON() ([]byte, error) { panic("broken marshaller") }

// handlers returns a text and a JSON logger writing into their own buffers,
// both built from m's HandlerOptions.
func handlers(m *redact.Masker) (map[string]*slog.Logger, map[string]*bytes.Buffer) {
	bufs := map[string]*bytes.Buffer{"text": {}, "json": {}}
	return map[string]*slog.Logger{
		"text": slog.New(slog.NewTextHandler(bufs["text"], m.HandlerOptions(slog.LevelDebug))),
		"json": slog.New(slog.NewJSONHandler(bufs["json"], m.HandlerOptions(slog.LevelDebug))),
	}, bufs
}

// TestAttr_ASecretInAValueThatIsNotAStringIsMasked: an error quoting device
// output, a []byte, a json.RawMessage, a Stringer, a *url.URL with a
// password, a named string type, and a struct, each carrying a registered
// secret, print the same line for either of two secrets under both handlers:
// no spelling of the secret survives.
func TestAttr_ASecretInAValueThatIsNotAStringIsMasked(t *testing.T) {
	values := map[string]func(secret string) any{
		"error":    func(s string) any { return fmt.Errorf("run failed: %w", errors.New("device said: "+s)) },
		"bytes":    func(s string) any { return []byte("stdout: " + s) },
		"raw json": func(s string) any { return json.RawMessage(`{"out":"` + s + `"}`) },
		"stringer": func(s string) any { return stringer{s} },
		"url": func(s string) any {
			link, _ := url.Parse("https://admin:" + s + "@device.example/api")
			return link
		},
		"named":  func(s string) any { return token(s) },
		"struct": func(s string) any { return carrier{Host: "sw1", Output: s} },
	}
	logged := func(secret, format, name string) string {
		m, err := redact.NewMasker(redact.DefaultRuleset())
		if err != nil {
			t.Fatal(err)
		}
		m.Literals().Add(secret)
		loggers, bufs := handlers(m)
		loggers[format].Info("task failed", "detail", values[name](secret))
		return bufs[format].String()
	}
	for name := range values {
		for _, format := range []string{"text", "json"} {
			a, b := stripTime(logged(plantedA, format, name)), stripTime(logged(plantedB, format, name))
			if a != b || strings.Contains(a, plantedA) {
				t.Errorf("%s through the %s handler depends on the secret:\n%s\n%s", name, format, a, b)
			}
		}
	}
}

// stripTime drops a log line's timestamp, the one part that differs between
// two runs.
func stripTime(line string) string {
	for _, prefix := range []string{`time=`, `"time":"`} {
		if i := strings.Index(line, prefix); i >= 0 {
			end := strings.IndexAny(line[i+len(prefix):], " \",")
			if end >= 0 {
				line = line[:i] + line[i+len(prefix)+end:]
			}
		}
	}
	return line
}

// TestAttr_APanickingMarshallerDoesNotEndTheLogCall: a value whose MarshalJSON
// panics is logged, not a panic in the logging goroutine, since ReplaceAttr
// runs outside slog's recovery.
func TestAttr_APanickingMarshallerDoesNotEndTheLogCall(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	loggers, bufs := handlers(m)
	for format, logger := range loggers {
		logger.Info("odd value", "detail", panicky{})
		if !strings.Contains(bufs[format].String(), "odd value") {
			t.Errorf("the %s handler wrote nothing for a panicking marshaller", format)
		}
	}
}

// TestAttr_AStructUnderAnIdentifierKeyInASecretGroupIsMasked: an identifier
// key stays shown only for a string or a scalar; a struct under one can hold
// a secret whose bytes are not known.
func TestAttr_AStructUnderAnIdentifierKeyInASecretGroupIsMasked(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	const unknown = "never-registered-struct-secret"
	loggers, bufs := handlers(m)
	loggers["json"].Info("login", slog.Group("credential", "user", carrier{Host: "sw1", Output: unknown}))
	if out := bufs["json"].String(); strings.Contains(out, unknown) {
		t.Fatalf("a struct under an identifier key in a secret group was shown: %s", out)
	}
}

// TestAttr_ASecretNamedGroupMasksItsLeaves: a group named for a secret, as a
// Group attribute or a WithGroup logger, masks the values under it by name,
// though their own keys are not secret names and their bytes are not known,
// and an identifier under it (a username) stays shown.
func TestAttr_ASecretNamedGroupMasksItsLeaves(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	const unknown = "never-registered-old-value"
	loggers, bufs := handlers(m)
	for format, logger := range loggers {
		bufs[format].Reset()
		logger.Info("rotated", slog.Group("password", "old", unknown, "username", "netops"))
		logger.WithGroup("password").Info("rotated", "new", unknown)
		out := bufs[format].String()
		if strings.Contains(out, unknown) || !strings.Contains(out, redact.Marker) {
			t.Errorf("a secret-named group through the %s handler: %s", format, out)
		}
		if !strings.Contains(out, "netops") {
			t.Errorf("an identifier under a secret-named group was masked through the %s handler: %s", format, out)
		}
	}
}

// credentialed is a struct whose field name says it holds a secret.
type credentialed struct {
	Host     string
	Password string
}

// TestAttr_ASecretNamedKeyInsideAValueIsMasked: a map entry or a struct
// field named for a secret is masked by its name, as a top-level attribute
// is, though its bytes were never registered, under both handlers; the
// value keeps its shape and the rest of it stays shown.
func TestAttr_ASecretNamedKeyInsideAValueIsMasked(t *testing.T) {
	const unknown = "never-registered-field-secret"
	values := map[string]any{
		"map":            map[string]any{"host": "sw1", "password": unknown},
		"string map":     map[string]string{"host": "sw1", "password": unknown},
		"struct":         credentialed{Host: "sw1", Password: unknown},
		"struct pointer": &credentialed{Host: "sw1", Password: unknown},
		"nested":         map[string]any{"device": map[string]any{"host": "sw1", "token": unknown}},
		"list":           []any{map[string]any{"host": "sw1", "secret": unknown}},
	}
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	loggers, bufs := handlers(m)
	for name, value := range values {
		for format, logger := range loggers {
			bufs[format].Reset()
			logger.Info("params", "detail", value)
			out := bufs[format].String()
			if strings.Contains(out, unknown) || !strings.Contains(out, redact.Marker) || !strings.Contains(out, "sw1") {
				t.Errorf("%s through the %s handler: %s", name, format, out)
			}
		}
	}
	bufs["json"].Reset()
	loggers["json"].Info("params", "detail", map[string]any{"host": "sw1", "password": unknown})
	if out := bufs["json"].String(); !strings.Contains(out, `"detail":{"host":"sw1","password":"`+redact.Marker+`"}`) {
		t.Errorf("a masked map lost its shape: %s", out)
	}
}

// TestAttr_AValueWithNothingSecretKeepsItsShape: a list with nothing secret in
// it is still a JSON array, not a string: only masking changes a value.
func TestAttr_AValueWithNothingSecretKeepsItsShape(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	loggers, bufs := handlers(m)
	loggers["json"].Info("hosts", "names", []string{"sw1", "sw2"}, "device", carrier{Host: "sw1", Output: "ok"})
	if out := bufs["json"].String(); !strings.Contains(out, `"names":["sw1","sw2"]`) || !strings.Contains(out, `"device":{"Host":"sw1","Output":"ok"}`) {
		t.Fatalf("a value with nothing secret lost its shape: %s", out)
	}
}
