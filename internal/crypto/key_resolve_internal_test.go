package crypto

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateAndSaveKey_LostRaceReadsWinner is a white-box test for
// generateAndSaveKey's own race-detection branch (the os.Link ErrExist
// path): internal (package crypto, not crypto_test) because deterministically
// triggering it requires calling this unexported function directly with
// keyPath already occupied by a file generateAndSaveKey itself never
// wrote, which ResolveKey's own exported entry point cannot set up (its
// initial os.ReadFile check would just take the "file already exists"
// branch and never call this function at all). A genuinely concurrent
// version of this same property is covered by
// TestResolveKey_ConcurrentGenerationConverges (key_resolve_test.go),
// which exercises the real race end to end but cannot guarantee this
// exact branch fires on every run; this test proves the branch's own
// logic deterministically.
func TestGenerateAndSaveKey_LostRaceReadsWinner(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "winner.key")

	winnerKey := make([]byte, keySize)
	for i := range winnerKey {
		winnerKey[i] = byte(i)
	}
	winnerEncoded := []byte(base64.StdEncoding.EncodeToString(winnerKey))
	if err := os.WriteFile(keyPath, winnerEncoded, 0o600); err != nil {
		t.Fatalf("failed to seed winning key file: %v", err)
	}

	got, err := generateAndSaveKey(keyPath)
	if err != nil {
		t.Fatalf("generateAndSaveKey() error = %v, want nil (must fall back to the winner's key)", err)
	}
	if string(got) != string(winnerKey) {
		t.Fatalf("generateAndSaveKey() = %x, want the pre-existing winner's key %x (a freshly generated key must never silently replace it)", got, winnerKey)
	}

	// The file on disk must still be exactly the winner's content, never
	// overwritten by the loser's own freshly generated key.
	onDisk, err := os.ReadFile(keyPath) // #nosec G304 -- keyPath is this test's own tempdir path
	if err != nil {
		t.Fatalf("failed to read key file: %v", err)
	}
	if string(onDisk) != string(winnerEncoded) {
		t.Fatalf("key file on disk was modified: got %q, want unchanged %q", onDisk, winnerEncoded)
	}
}
