// Tests of keyed digests (keyed.go): what a store keeps of a restricted
// file's digest is HMAC-SHA-256 under a derived key, bound to the device and
// the fact, never the raw digest; and Prepare drops any digest nothing keyed.
package facts_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// keyedSecret is a 32-byte secret, as a master key is.
var keyedSecret = bytes.Repeat([]byte{7}, 32)

// keyedRaw is the SHA-256 of "pin1234\n", a small secret a plain digest gives
// away (printf 'pin1234\n' | sha256sum).
const keyedRaw = "3f773b3737a2f378fa4589ecacc53598168149f40fa0294c8580ee8fa4455b96"

func TestKeyer_SealsWithADerivedKeyBoundToDeviceAndFact(t *testing.T) {
	k, err := facts.NewKeyer(keyedSecret, "project")
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Seal("dev-1", "file.%2Fetc%2Fpin", keyedRaw)
	if err != nil {
		t.Fatal(err)
	}
	// Computed here from the design, sharing no code with the keyer.
	derive := hmac.New(sha256.New, keyedSecret)
	derive.Write([]byte("pleiades/facts/keyed-digest/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte("dev-1\x00file.%2Fetc%2Fpin\x00" + keyedRaw))
	if want := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("Seal = %s, want %s", got, want)
	}
	other, _ := k.Seal("dev-2", "file.%2Fetc%2Fpin", keyedRaw)
	path, _ := k.Seal("dev-1", "file.%2Fetc%2Fpin2", keyedRaw)
	again, _ := k.Seal("dev-1", "file.%2Fetc%2Fpin", keyedRaw)
	upper, _ := k.Seal("dev-1", "file.%2Fetc%2Fpin", strings.ToUpper(keyedRaw))
	if other == got || path == got || again != got || upper != got {
		t.Errorf("seal not bound as designed: other device %v, other path %v, repeat %v, upper case %v", other == got, path == got, again == got, upper == got)
	}
	if _, err := k.Seal("dev-1", "file.x", "not-hex"); err == nil {
		t.Error("a value that is not a digest was sealed")
	}
}

func TestKeyer_IDNamesTheSourceAndKeyAndNothingOfIt(t *testing.T) {
	a, _ := facts.NewKeyer(keyedSecret, "project")
	b, _ := facts.NewKeyer(bytes.Repeat([]byte{8}, 32), "project")
	c, _ := facts.NewKeyer(keyedSecret, "controller")
	if !strings.HasPrefix(a.ID(), "project:") || len(a.ID()) != len("project:")+16 || a.ID() == b.ID() || !strings.HasPrefix(c.ID(), "controller:") {
		t.Errorf("ids %q, %q, %q", a.ID(), b.ID(), c.ID())
	}
	if _, err := facts.NewKeyer(make([]byte, 16), "project"); err == nil {
		t.Error("a 16-byte secret was accepted")
	}
}

func TestNewLazyKeyer_ReadsItsSecretOnlyWhenADigestNeedsIt(t *testing.T) {
	reads := 0
	k := facts.NewLazyKeyer(func() ([]byte, error) { reads++; return keyedSecret, nil }, "project")
	if reads != 0 {
		t.Fatal("the secret was read before any digest needed it")
	}
	eager, _ := facts.NewKeyer(keyedSecret, "project")
	got, _ := k.Seal("dev-1", "file.x", keyedRaw)
	want, _ := eager.Seal("dev-1", "file.x", keyedRaw)
	_, _ = k.Seal("dev-1", "file.y", keyedRaw)
	if got != want || reads != 1 || k.ID() != eager.ID() {
		t.Errorf("lazy %q, eager %q, %d reads", got, want, reads)
	}
	// A secret that cannot be read keys nothing, and the raw digest is
	// dropped, never kept.
	broken := facts.NewLazyKeyer(func() ([]byte, error) { return nil, errors.New("no key file") }, "project")
	if out := broken.KeyRaw("dev-1", "file.x", map[string]any{"sha256_raw": keyedRaw}).(map[string]any); len(out) != 0 || broken.ID() != "" {
		t.Errorf("an unreadable secret left %v, id %q", out, broken.ID())
	}
	short := facts.NewLazyKeyer(func() ([]byte, error) { return make([]byte, 8), nil }, "project")
	if _, err := short.Seal("dev-1", "file.x", keyedRaw); err == nil {
		t.Error("an 8-byte secret keyed a digest")
	}
}

func TestKeyRaw_ReplacesTheRawDigestAndNeverKeepsIt(t *testing.T) {
	k, _ := facts.NewKeyer(keyedSecret, "project")
	in := map[string]any{"state": "present", "sha256_raw": keyedRaw, "mode": "0600"}
	out := k.KeyRaw("dev-1", "file.x", in).(map[string]any)
	sealed, _ := k.Seal("dev-1", "file.x", keyedRaw)
	if out["hmac_sha256"] != sealed || out["hmac_key"] != k.ID() || out["mode"] != "0600" || out["sha256_raw"] != nil {
		t.Fatalf("KeyRaw = %v", out)
	}
	if in["sha256_raw"] != keyedRaw || in["hmac_sha256"] != nil {
		t.Error("KeyRaw changed the value it was given")
	}
	// A raw value that is not a digest is dropped, never kept.
	bad := k.KeyRaw("dev-1", "file.x", map[string]any{"sha256_raw": "plaintext secret"}).(map[string]any)
	if len(bad) != 0 {
		t.Errorf("a non-digest = %v", bad)
	}
	plain := map[string]any{"sha256": keyedRaw}
	if got := k.KeyRaw("dev-1", "file.x", plain); !reflect.DeepEqual(got, plain) {
		t.Errorf("a value with no raw digest = %v", got)
	}
	if got := k.KeyRaw("dev-1", "file.x", "a string"); got != "a string" {
		t.Errorf("a scalar = %v", got)
	}
}

func TestReplaceRaw_DropsTheRawDigestWhateverTheReplacementSays(t *testing.T) {
	in := map[string]any{"sha256_raw": keyedRaw, "state": "present"}
	out := facts.ReplaceRaw(in, func(raw string) (map[string]any, error) {
		return map[string]any{"sha256_sealed": "ciphertext of " + raw[:4]}, nil
	}).(map[string]any)
	if out["sha256_raw"] != nil || out["sha256_sealed"] != "ciphertext of 3f77" || out["state"] != "present" {
		t.Errorf("ReplaceRaw = %v", out)
	}
	failed := facts.ReplaceRaw(in, func(string) (map[string]any, error) { return nil, errors.New("no key") }).(map[string]any)
	dropped := facts.ReplaceRaw(in, nil).(map[string]any)
	notDigest := facts.ReplaceRaw(map[string]any{"sha256_raw": "secret"}, func(string) (map[string]any, error) {
		t.Error("a raw value that is not a digest reached the replacement")
		return nil, nil
	}).(map[string]any)
	if len(failed) != 1 || len(dropped) != 1 || len(notDigest) != 0 || in["sha256_raw"] != keyedRaw {
		t.Errorf("refused %v, no replacement %v, not a digest %v", failed, dropped, notDigest)
	}
}

func TestPrepare_StoresNoDigestNothingKeyed(t *testing.T) {
	observed := map[string]any{"file.x": map[string]any{"state": "present", "sha256_raw": keyedRaw, "sha256_sealed": "c"}}
	for _, allow := range []bool{false, true} {
		entries, problems, err := facts.Prepare(observed, facts.Guard{AllowSealed: allow})
		if err != nil || len(problems) != 0 || len(entries) != 1 {
			t.Fatalf("Prepare: %v %v %v", entries, problems, err)
		}
		v := entries[0].Value.(map[string]any)
		if v["sha256_raw"] != nil || (v["sha256_sealed"] != nil) != allow || v["state"] != "present" {
			t.Errorf("allowSealed %v: stored %v", allow, v)
		}
		if want, _ := facts.Hash("file.x", v); entries[0].Hash != want {
			t.Errorf("allowSealed %v: the hash is not of what is stored", allow)
		}
	}
}

func TestSealPlaintext_BindsTheDigestToItsFactAndDevice(t *testing.T) {
	pt, err := facts.SealPlaintext("dev-1", "file.x", strings.ToUpper(keyedRaw))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := facts.OpenPlaintext(pt, "dev-1", "file.x"); err != nil || raw != keyedRaw {
		t.Errorf("OpenPlaintext = %q, %v", raw, err)
	}
	for _, c := range [][2]string{{"dev-2", "file.x"}, {"dev-1", "file.y"}} {
		if _, err := facts.OpenPlaintext(pt, c[0], c[1]); err == nil {
			t.Errorf("a digest sealed for dev-1's file.x opened as %s's %s", c[0], c[1])
		}
	}
	if _, err := facts.SealPlaintext("dev-1", "file.x", "nope"); err == nil {
		t.Error("a non-digest was sealed")
	}
	if _, err := facts.OpenPlaintext([]byte("dev-1\x00file.x\x00nope"), "dev-1", "file.x"); err == nil {
		t.Error("a sealed non-digest opened")
	}
}
