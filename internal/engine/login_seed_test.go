// Tests for seeding a login into a machine a method creates: what the
// method receives, what it never does, and where it is refused.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/shacrypt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// vault is a credential store holding one credential per device.
type vault map[string]credential.Credential

func (v vault) Lookup(_ context.Context, device string) (credential.Credential, error) {
	cred, ok := v[device]
	if !ok {
		return credential.Credential{}, credential.ErrNotFound
	}
	return cred, nil
}

// registerSeeding registers a method that seeds the login its "login"
// parameter names, keeping what it was handed.
func registerSeeding(t *testing.T, suffix string, got *map[string]string) string {
	t.Helper()
	return registerSeedingFor(t, suffix, got, false)
}

// registerSeedingFor is registerSeeding for a method that can, when
// password is set, seed a machine reached over WinRM.
func registerSeedingFor(t *testing.T, suffix string, got *map[string]string, password bool) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	name := "enginetest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:             collection.StatusImplemented,
			Reversibility:      collection.Reversibility{Notes: "a test fixture that changes nothing"},
			SeedsLogin:         "login",
			SeedsLoginPassword: password,
			Doc:                collection.Doc{Params: []collection.Param{{Name: "login", Type: "string", Required: true}}},
		},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			*got = rc.InjectSecrets()
			return collection.Result{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestLoginSeed_TheMethodGetsOnlyWhatAMachineNeeds(t *testing.T) {
	generated, err := credential.Generate("root", "")
	if err != nil {
		t.Fatal(err)
	}
	store := vault{"ubuntu-lab": generated, "vengeance": {Username: "gate", Password: "winrm-secret"}}
	var got map[string]string
	name := registerSeeding(t, "seeds", &got)
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(store),
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(store, inventoryOf{})))
	target := newNamedDevice("vengeance")
	if _, err := exec.Execute(context.Background(), &engine.Task{FQCN: name, Params: map[string]any{"login": "ubuntu-lab"}}, target); err != nil {
		t.Fatal(err)
	}

	// The target's own credential, unchanged, beside the seed.
	if got[wire.SecretUsername] != "gate" || got[wire.SecretPassword] != "winrm-secret" {
		t.Errorf("the target's credential: %v", got)
	}
	if got[wire.SecretSeedUsername] != "root" {
		t.Errorf("seed username = %q", got[wire.SecretSeedUsername])
	}
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(got[wire.SecretSeedAuthorizedKey]))
	signer, _ := ssh.ParsePrivateKey(generated.PrivateKeyPEM)
	if err != nil || comment != "root@ubuntu-lab" || string(key.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Errorf("seed key %q: %v", got[wire.SecretSeedAuthorizedKey], err)
	}
	if !shacrypt.Verify(generated.Password, got[wire.SecretSeedPasswordHash]) {
		t.Errorf("seed hash %q does not verify the stored password", got[wire.SecretSeedPasswordHash])
	}
	for k, v := range got {
		if strings.Contains(v, generated.Password) || strings.Contains(v, "PRIVATE KEY") {
			t.Errorf("%s carries the seeded device's secret", k)
		}
	}

	// A fresh salt each time: no two seeds of one password are alike.
	first := got[wire.SecretSeedPasswordHash]
	if _, err := exec.Execute(context.Background(), &engine.Task{FQCN: name, Params: map[string]any{"login": "ubuntu-lab"}}, target); err != nil {
		t.Fatal(err)
	}
	if got[wire.SecretSeedPasswordHash] == first {
		t.Error("two seeds of one password share a salt")
	}
}

func TestLoginSeed_Refusals(t *testing.T) {
	var got map[string]string
	name := registerSeeding(t, "refused", &got)
	keyOnly, _ := credential.Generate("root", "")
	keyOnly.Password = ""
	store := vault{"password-only": {Username: "root", Password: "p"}, "key-only": keyOnly}
	seeded := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(store),
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(store, inventoryOf{})))
	run := func(exec engine.ActionExecutor, params map[string]any) error {
		_, err := exec.Execute(context.Background(), &engine.Task{FQCN: name, Params: params}, newNamedDevice("vengeance"))
		return err
	}
	for why, tt := range map[string]struct {
		exec   engine.ActionExecutor
		params map[string]any
		want   string
	}{
		"no seeder": {engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext),
			map[string]any{"login": "ubuntu-lab"}, "only the pleiades CLI resolves"},
		"a subprocess invoker": {engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext,
			engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(store, inventoryOf{})),
			engine.WithCollectionInvoker(func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]any, error) {
				t.Error("the subprocess ran a seeding method")
				return collection.Result{}, nil, nil
			})), map[string]any{"login": "ubuntu-lab"}, "only the pleiades CLI resolves"},
		"no login named":           {seeded, map[string]any{}, "must name the inventory device"},
		"no credential stored":     {seeded, map[string]any{"login": "ubuntu-lab"}, "add-credential ubuntu-lab --username <user> --generate"},
		"a credential with no key": {seeded, map[string]any{"login": "password-only"}, "needs a username and a key"},
	} {
		got = nil
		err := run(tt.exec, tt.params)
		if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
			t.Errorf("%s: err = %v, want one mentioning %q, and the method not run", why, err, tt.want)
		}
	}
	if err := run(seeded, map[string]any{"login": "key-only"}); err != nil || got[wire.SecretSeedPasswordHash] != "" || got[wire.SecretSeedAuthorizedKey] == "" {
		t.Errorf("a key with no password: %v, %v", err, got)
	}
	broken := vault{"x": {Username: "root", PrivateKeyPEM: []byte("garbage")}}
	if err := run(engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext,
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(broken, inventoryOf{}))), map[string]any{"login": "x"}); err == nil {
		t.Error("a key that does not parse was seeded")
	}
	if err := run(engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext,
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(failingStore{err: errors.New("wrong master key")}, inventoryOf{}))), map[string]any{"login": "x"}); err == nil || !strings.Contains(err.Error(), "resolve credential") {
		t.Errorf("an unreadable store: %v", err)
	}
	if _, err := engine.NewCredentialLoginSeeder(nil, inventoryOf{})(context.Background(), "x", false); err == nil {
		t.Error("a nil store seeded")
	}
	if _, err := engine.NewCredentialLoginSeeder(store, nil)(context.Background(), "key-only", false); err == nil {
		t.Error("seeded with no inventory")
	}
}

// inventoryOf finds a device of every name except those in missing, each
// reached over WinRM when named in winrm and over SSH otherwise.
type inventoryOf struct {
	winrm, missing map[string]bool
}

func (i inventoryOf) GetByName(_ context.Context, name string) (inventory.InventoryItem, error) {
	if i.missing[name] {
		return nil, errors.New("no device named " + name)
	}
	d := &namedDevice{Stub: &inventorytest.Stub{}, name: name}
	if i.winrm[name] {
		d.Caps = []capability.Name{capability.NameWinRM}
	}
	return d, nil
}

func TestLoginSeed_AWindowsMachineGetsThePasswordOnlyWhenTheMethodCanSeedOne(t *testing.T) {
	generated, err := credential.Generate("Administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	store := vault{"win-lab": generated, "cert-only": {Username: "gate", CertificatePEM: []byte("cert"), PrivateKeyPEM: []byte("key")}, "linux": generated}
	devices := inventoryOf{winrm: map[string]bool{"win-lab": true, "cert-only": true}, missing: map[string]bool{"gone": true}}
	var got map[string]string
	windows := registerSeedingFor(t, "windows", &got, true)
	keyOnly := registerSeedingFor(t, "keyonly", &got, false)
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(vault{}),
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(store, devices)))
	run := func(method, login string) error {
		got = nil
		_, err := exec.Execute(context.Background(), &engine.Task{FQCN: method, Params: map[string]any{"login": login}}, newNamedDevice("vengeance"))
		return err
	}

	if err := run(windows, "win-lab"); err != nil {
		t.Fatal(err)
	}
	if got[wire.SecretSeedUsername] != "Administrator" || got[wire.SecretSeedPassword] != generated.Password {
		t.Errorf("a Windows seed: %v", got)
	}
	if got[wire.SecretSeedAuthorizedKey] != "" || got[wire.SecretSeedPasswordHash] != "" {
		t.Errorf("a Windows seed carried a key or a hash too: %v", got)
	}

	// A machine reached over SSH gets a key and a hash, never the
	// password, even from a method that could take one.
	if err := run(windows, "linux"); err != nil {
		t.Fatal(err)
	}
	if got[wire.SecretSeedPassword] != "" || got[wire.SecretSeedAuthorizedKey] == "" || !shacrypt.Verify(generated.Password, got[wire.SecretSeedPasswordHash]) {
		t.Errorf("an SSH seed from a method that can take a password: %v", got)
	}

	for why, tt := range map[string]struct{ method, login, want string }{
		"a method that seeds keys only":  {keyOnly, "win-lab", "seeds only machines reached by a key"},
		"a WinRM login with no password": {windows, "cert-only", "needs a username and a password"},
		"a login not in the inventory":   {windows, "gone", "no device named gone"},
	} {
		err := run(tt.method, tt.login)
		if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
			t.Errorf("%s: err = %v, want one mentioning %q, and the method not run", why, err, tt.want)
		}
	}
}
