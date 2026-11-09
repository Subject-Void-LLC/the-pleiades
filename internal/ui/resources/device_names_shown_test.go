// Package resources_test: the UI's device form refuses a name that would
// not show as itself, through the session, CSRF and admission middleware a
// browser's submission passes. Every character outside printable ASCII is
// built from its code point, so this file holds none (FAILURE_PATTERNS 256,
// 834).
package resources_test

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// TestDevices_CreateRefusesANameThatWouldNotShowAsItself submits the
// create form with a name holding a newline, an escape, a direction
// override and a byte order mark. Each is refused on the name field with
// the reason naming the character (422, the form redisplayed), and nothing
// reaches the store.
func TestDevices_CreateRefusesANameThatWouldNotShowAsItself(t *testing.T) {
	h := newHarness(t, adminIdentity)
	before := conformanceRepo.itemCount()
	for _, tc := range []struct{ name, says string }{
		{"sw2\npleiades: approved", "U+000A at byte 3"},
		{"sw\x1b[2J1", "U+001B at byte 2"},
		{"abc" + string(rune(0x202E)) + "def", "U+202E at byte 3"},
		{string(rune(0xFEFF)) + "web1", "U+FEFF at byte 0"},
	} {
		t.Run(tc.says, func(t *testing.T) {
			rec := h.post(t, "/ui/devices", map[string]string{"name": tc.name, "type": "linux_server"})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("creating a device named %q = %d, want 422: %s", tc.name, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, html.EscapeString("the name cannot be used")) || !strings.Contains(body, tc.says) {
				t.Errorf("the form for %q does not say why it was refused", tc.name)
			}
		})
	}
	if after := conformanceRepo.itemCount(); after != before {
		t.Errorf("the store holds %d devices after the refused creates, want %d", after, before)
	}
}
