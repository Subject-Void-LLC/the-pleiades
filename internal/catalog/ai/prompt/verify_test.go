// Tests for ai.prompt.verify: each document kind and each refusal.
package prompt_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

func signed(t *testing.T, priv ed25519.PrivateKey, doc string) string {
	t.Helper()
	parsed, err := promptsign.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := json.Marshal(promptsign.Sign(parsed, priv))
	return string(sig)
}

// TestVerify: a signed routing table verifies and reports itself; an
// unsigned document is refused under the default policy; the use must
// match; and verifying records no version.
func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	routes := `{"kind":"routes","name":"main","version":2,"use":"triage","body":{"triage":{"residency":"local"}}}`
	rc := aitest.NewContext()
	rc.Keys = []byte("keys:\n  - public_key: " + base64.StdEncoding.EncodeToString(pub) + "\n    uses: [triage]\n")
	rc.Prompts["main"] = [2][]byte{[]byte(routes), []byte(signed(t, priv, routes))}
	if _, err := prompt.Verify(context.Background(), rc, nil, map[string]any{"prompt_file": "main", "use": "triage"}); err != nil {
		t.Fatal(err)
	}
	if rc.Stats["status"] != "verified" || rc.Stats["kind"] != "routes" || rc.Stats["version"] != int64(2) || len(rc.Versions) != 0 {
		t.Errorf("stats %v versions %v", rc.Stats, rc.Versions)
	}
	for _, tc := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"prompt": routes}, "not signed"},
		{map[string]any{"prompt_file": "main", "use": "summarize"}, "not summarize"},
		{map[string]any{"prompt": strings.Replace(routes, `"version":2`, `"version":3`, 1), "signature": signed(t, priv, routes)}, "does not verify"},
		{map[string]any{"prompt_file": "main", "prompt": routes}, "exactly one"},
	} {
		if _, err := prompt.Verify(context.Background(), rc, nil, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.params, err, tc.want)
		}
	}
	optional := aitest.NewContext()
	if _, err := prompt.Verify(context.Background(), optional, nil, map[string]any{"prompt": routes, "signing": "optional"}); err != nil || optional.Stats["status"] != "unsigned" {
		t.Errorf("optional: %v %v", optional.Stats, err)
	}
}
