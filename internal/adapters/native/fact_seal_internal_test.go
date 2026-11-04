// Tests of sealing raw digests on a Runner (fact_seal.go): a dispatch's seal
// key seals a digest that opens only as this job's, device's and fact's; a
// dispatch with none, or with one that will not parse, seals nothing.
package native

import (
	"bytes"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// sealRaw is the SHA-256 of "pin1234\n".
const sealRaw = "3f773b3737a2f378fa4589ecacc53598168149f40fa0294c8580ee8fa4455b96"

func TestFactSealing_SealsToTheDispatchKeyBoundToItsJob(t *testing.T) {
	priv, err := sealed.DeriveKey(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	actions, run, err := factSealing(wire.DispatchPayload{JobID: "job-7", FactSealKey: priv.Public().String()})
	if err != nil || len(actions) != 1 || run == nil {
		t.Fatalf("factSealing: %d options, %v", len(actions), err)
	}
	// What this Runner seals opens only as job-7's, device's and fact's.
	text, err := sealerFor("job-7", priv.Public())("dev-1", "file.x", sealRaw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sealed.Parse([]byte(text))
	if err != nil || parsed.Purpose != sealed.PurposeFactDigest {
		t.Fatalf("sealed %q: %v", text, err)
	}
	opened, err := parsed.OpenWithKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := facts.OpenPlaintext(opened, "job-7", "dev-1", "file.x"); err != nil || raw != sealRaw {
		t.Errorf("opened %q, %v", raw, err)
	}
	if _, err := facts.OpenPlaintext(opened, "job-8", "dev-1", "file.x"); err == nil {
		t.Error("a digest sealed in job-7 opened as job-8's")
	}
	if _, err := sealerFor("job-7", priv.Public())("dev-1", "file.x", "not a digest"); err == nil {
		t.Error("a non-digest was sealed")
	}

	if actions, _, err := factSealing(wire.DispatchPayload{JobID: "job-7"}); err != nil || actions != nil {
		t.Errorf("no key: %d options, %v", len(actions), err)
	}
	if actions, _, err := factSealing(wire.DispatchPayload{JobID: "job-7", FactSealKey: "not a key"}); err == nil || actions != nil {
		t.Errorf("a key that does not parse: %d options, %v", len(actions), err)
	}
}
