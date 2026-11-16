// Package gnmi: masking a device's answer by the client's own credential,
// in each form it could be echoed in.
package gnmi

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// maskingClient is a client holding secrets, never connected.
func maskingClient(t *testing.T, secrets map[string]string) *Client {
	t.Helper()
	c, err := New(Options{Target: "127.0.0.1:1", Plaintext: true, Auth: AuthNone, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestMask_ReplacesEachFormAndKeepsTheName masks the password, its JSON
// escaped form, the bearer header and a value added later, and leaves the
// username, which names rather than proves.
func TestMask_ReplacesEachFormAndKeepsTheName(t *testing.T) {
	c := maskingClient(t, map[string]string{
		wire.SecretUsername:    "admin",
		wire.SecretPassword:    `pa"ss-word-1`,
		wire.SecretBearerToken: "token-abcdef-123",
	})
	c.MaskAlso("ike-shared-key-99")
	text := `admin sent pa"ss-word-1 and pa\"ss-word-1, Bearer token-abcdef-123, and ike-shared-key-99`
	got, err := c.Mask(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{`pa"ss-word-1`, `pa\"ss-word-1`, "token-abcdef-123", "ike-shared-key-99"} {
		if strings.Contains(got, secret) {
			t.Errorf("Mask left %q in %q", secret, got)
		}
	}
	if !strings.HasPrefix(got, "admin sent ") {
		t.Errorf("Mask = %q, want the username kept", got)
	}
}

// TestMask_AShortSecretOnlyAsAWord masks a short password where it stands
// alone, never inside another word, and a JSON number equal to it whole.
func TestMask_AShortSecretOnlyAsAWord(t *testing.T) {
	c := maskingClient(t, map[string]string{wire.SecretPassword: "1234"})
	got, err := c.Mask("pin 1234 but not 912345")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, " 1234 ") || !strings.Contains(got, "912345") {
		t.Errorf("Mask = %q", got)
	}
	doc, err := c.Mask(`{"pin": 1234, "other": 5}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, "1234") || !strings.Contains(doc, `"other":5`) && !strings.Contains(doc, `"other": 5`) {
		t.Errorf("Mask of a JSON document = %q", doc)
	}
}
