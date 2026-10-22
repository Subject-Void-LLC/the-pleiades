// Tests for the project's seal key and for opening a sealed password with
// it or with a passphrase.
package credential_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

func masterKey(t *testing.T, dir string) []byte {
	t.Helper()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSealKey_IsMadeOnceAndKeptEncrypted(t *testing.T) {
	dir := t.TempDir()
	key := masterKey(t, dir)

	if _, err := credential.LoadSealKey(dir, key); !errors.Is(err, credential.ErrNoSealKey) {
		t.Fatalf("before any: err = %v, want ErrNoSealKey", err)
	}
	first, created, err := credential.SealKey(dir, key)
	if err != nil || !created {
		t.Fatalf("first SealKey: created %v, err %v", created, err)
	}
	second, created, err := credential.SealKey(dir, key)
	if err != nil || created {
		t.Fatalf("second SealKey: created %v, err %v", created, err)
	}
	if first.Public().Fingerprint() != second.Public().Fingerprint() {
		t.Error("a second call made a different key")
	}

	path := filepath.Join(dir, ".pleiades", "seal.key")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX modes to hold it to.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("seal.key mode %v, want 0600", info.Mode().Perm())
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := first.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, raw) {
		t.Error("seal.key holds the private key unencrypted")
	}

	other := make([]byte, 32)
	if _, err := credential.LoadSealKey(dir, other); err == nil || !strings.Contains(err.Error(), "master key") {
		t.Errorf("another master key: err = %v", err)
	}
	if err := os.WriteFile(path, []byte("something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.LoadSealKey(dir, key); err == nil || !strings.Contains(err.Error(), "not a seal key") {
		t.Errorf("a foreign file: err = %v", err)
	}
}

func TestOpenSealedPassword_OpensWithWhatItWasSealedTo(t *testing.T) {
	const password = "s3cret-pass"
	const passphrase = "a long enough passphrase"
	dir := t.TempDir()
	key := masterKey(t, dir)
	priv, _, err := credential.SealKey(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	parse := func(data []byte, err error) *sealed.File {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		f, err := sealed.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	byKey := parse(sealed.SealToKey(priv.Public(), sealed.PurposePassword, []byte(password)))
	byPassphrase := parse(sealed.SealWithPassphrase(passphrase, sealed.PurposePassword, []byte(password)))
	give := func(p string) func() (string, error) { return func() (string, error) { return p, nil } }

	if got, err := credential.OpenSealedPassword(dir, key, byKey, nil); err != nil || got != password {
		t.Errorf("by key: %q, %v", got, err)
	}
	if got, err := credential.OpenSealedPassword(dir, key, byPassphrase, give(passphrase)); err != nil || got != password {
		t.Errorf("by passphrase: %q, %v", got, err)
	}

	elsewhere := t.TempDir()
	for name, c := range map[string]struct {
		dir        string
		file       *sealed.File
		passphrase func() (string, error)
		want       string
	}{
		"a passphrase for a key-sealed file":         {dir, byKey, give(passphrase), "does not apply"},
		"no passphrase for a passphrase-sealed file": {dir, byPassphrase, nil, "--passphrase"},
		"the wrong passphrase":                       {dir, byPassphrase, give(passphrase + "!"), "passphrase is wrong"},
		"a passphrase that cannot be read":           {dir, byPassphrase, func() (string, error) { return "", errors.New("no terminal") }, "no terminal"},
		"another project with no seal key":           {elsewhere, byKey, nil, "another project"},
	} {
		k := key
		if c.dir == elsewhere {
			k = masterKey(t, elsewhere)
		}
		if _, err := credential.OpenSealedPassword(c.dir, k, c.file, c.passphrase); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, c.want)
		}
	}

	// Another project with a seal key of its own names both keys.
	if _, _, err := credential.SealKey(elsewhere, masterKey(t, elsewhere)); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.OpenSealedPassword(elsewhere, masterKey(t, elsewhere), byKey, nil); !errors.Is(err, sealed.ErrWrongKey) {
		t.Errorf("another project's key: err = %v, want ErrWrongKey", err)
	}
}

func TestSealKey_RefusesWhatItCannotUse(t *testing.T) {
	dir := t.TempDir()
	key := masterKey(t, dir)
	path := filepath.Join(dir, ".pleiades", "seal.key")

	short := make([]byte, 16)
	if _, _, err := credential.SealKey(dir, short); err == nil {
		t.Error("SealKey accepted a 16-byte master key")
	}

	if err := os.WriteFile(path, []byte("pleiades seal key v1\nnot base64!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.LoadSealKey(dir, key); err == nil || !strings.Contains(err.Error(), "not a seal key") {
		t.Errorf("bad base64: err = %v", err)
	}
	if err := os.WriteFile(path, []byte("pleiades seal key v1\nAAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.LoadSealKey(dir, short); err == nil {
		t.Error("LoadSealKey accepted a 16-byte master key")
	}
	if _, _, err := credential.SealKey(dir, key); err == nil {
		t.Error("SealKey replaced or accepted an unreadable seal key")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.LoadSealKey(dir, key); err == nil || !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("a directory in its place: err = %v", err)
	}

	// A project directory that is a file, and a .pleiades nobody may write.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := credential.SealKey(file, key); err == nil {
		t.Error("SealKey made a key under a file")
	}
	locked := t.TempDir()
	if err := os.Mkdir(filepath.Join(locked, ".pleiades"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(locked, ".pleiades"), 0o700) })
	// Root writes anywhere, and Windows ignores a directory's mode bits.
	if os.Geteuid() != 0 && runtime.GOOS != "windows" {
		if _, _, err := credential.SealKey(locked, key); err == nil || !strings.Contains(err.Error(), "write the seal key") {
			t.Errorf("an unwritable .pleiades: err = %v", err)
		}
	}
}

func TestOpenSealedPassword_RefusesAFileItCannotJudge(t *testing.T) {
	dir := t.TempDir()
	key := masterKey(t, dir)
	for name, c := range map[string]struct {
		file *sealed.File
		want string
	}{
		"another purpose": {&sealed.File{Method: sealed.MethodKey, Purpose: "token"}, "not a password"},
		"another method":  {&sealed.File{Method: "rot13", Purpose: sealed.PurposePassword}, "does not open"},
	} {
		if _, err := credential.OpenSealedPassword(dir, key, c.file, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, c.want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".pleiades", "seal.key"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &sealed.File{Method: sealed.MethodKey, Purpose: sealed.PurposePassword, Recipient: "SHA256:x"}
	if _, err := credential.OpenSealedPassword(dir, key, f, nil); err == nil || !strings.Contains(err.Error(), "not a seal key") {
		t.Errorf("a corrupt seal key: err = %v", err)
	}
}

func TestSealKey_NeverWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	key := masterKey(t, dir)
	target := filepath.Join(t.TempDir(), "planted")
	if err := os.Symlink(target, filepath.Join(dir, ".pleiades", "seal.key")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("making a symbolic link needs a privilege here: %v", err)
		}
		t.Fatal(err)
	}
	if _, _, err := credential.SealKey(dir, key); err == nil || !strings.Contains(err.Error(), "not a readable file") {
		t.Errorf("a link to nothing: err = %v", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("SealKey wrote a key through the link")
	}
}

// TestSealKey_ConcurrentFirstUsesAgreeOnOneKey makes the first key from
// several callers at once: exactly one makes it, every one gets the same
// key, none reads a half-written file, and no temporary file is left.
func TestSealKey_ConcurrentFirstUsesAgreeOnOneKey(t *testing.T) {
	dir := t.TempDir()
	key := masterKey(t, dir)
	const callers = 8
	var wg sync.WaitGroup
	fingerprints := make([]string, callers)
	created := make([]bool, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			priv, made, err := credential.SealKey(dir, key)
			errs[i], created[i] = err, made
			if err == nil {
				fingerprints[i] = priv.Public().Fingerprint()
			}
		}()
	}
	wg.Wait()
	makers := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if fingerprints[i] != fingerprints[0] {
			t.Errorf("caller %d got key %s, caller 0 got %s", i, fingerprints[i], fingerprints[0])
		}
		if created[i] {
			makers++
		}
	}
	if makers != 1 {
		t.Errorf("%d callers report making the key, want exactly one", makers)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".pleiades"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".seal.key.") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
