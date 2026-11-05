// Tests that a registered secret never reaches a log line through a value
// that is not a string, or a leaf under a secret-named group, through the
// standard library's own text and JSON handlers, as every cmd/ binary builds
// them (HandlerOptions).
package redact_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// planted is a secret value this process knows the bytes of.
const planted = "Zq7-planted-device-secret-41"

// stringer is a fmt.Stringer whose text carries the secret.
type stringer struct{}

func (stringer) String() string { return "token=" + planted }

// token is a named string type, which slog holds as KindAny, not KindString.
type token string

// carrier is a struct a JSON handler would marshal whole.
type carrier struct {
	Host   string
	Output string
}

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
// output, a []byte, a Stringer, a *url.URL with a password, a named string
// type, and a struct, each carrying a registered secret, print without it,
// under both handlers.
func TestAttr_ASecretInAValueThatIsNotAStringIsMasked(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	m.Literals().Add(planted)
	link, _ := url.Parse("https://admin:" + planted + "@device.example/api")
	values := map[string]any{
		"error":    fmt.Errorf("run failed: %w", errors.New("device said: "+planted)),
		"bytes":    []byte("stdout: " + planted),
		"stringer": stringer{},
		"url":      link,
		"named":    token(planted),
		"struct":   carrier{Host: "sw1", Output: planted},
	}
	loggers, bufs := handlers(m)
	for name, value := range values {
		for format, logger := range loggers {
			bufs[format].Reset()
			logger.Info("task failed", "detail", value)
			if out := bufs[format].String(); strings.Contains(out, planted) {
				t.Errorf("%s through the %s handler printed the secret: %s", name, format, out)
			}
		}
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

// TestAttr_AValueWithNothingSecretKeepsItsShape: a list with nothing secret in
// it is still a JSON array, not a string: only masking changes a value.
func TestAttr_AValueWithNothingSecretKeepsItsShape(t *testing.T) {
	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	loggers, bufs := handlers(m)
	loggers["json"].Info("hosts", "names", []string{"sw1", "sw2"})
	if out := bufs["json"].String(); !strings.Contains(out, `"names":["sw1","sw2"]`) {
		t.Fatalf("a list with nothing secret lost its shape: %s", out)
	}
}
