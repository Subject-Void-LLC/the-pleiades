// Tests of seeding a Cisco IOS device's login: IOS type 9 hashes of its
// passwords, and only for a method that says it can write them.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iossecret"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ciscoInventory finds every name as a Cisco IOS device, but those in
// missing.
type ciscoInventory struct{ missing map[string]bool }

func (c ciscoInventory) GetByName(_ context.Context, name string) (inventory.InventoryItem, error) {
	if c.missing[name] {
		return nil, errors.New("no device named " + name)
	}
	d := &namedDevice{Stub: &inventorytest.Stub{}, name: name}
	d.Caps = []capability.Name{capability.NameCiscoIOS}
	switch name {
	case "CORE1":
		d.Caps = append(d.Caps, capability.NameNetworkAddressable)
		return addressedDevice{d, "10.10.20.181"}, nil
	case "CONSOLE1":
		// A console-served device with a stale host: it claims no address.
		return addressedDevice{d, "10.10.20.99"}, nil
	}
	return d, nil
}

// addressedDevice is a device with a host, as a cisco_router has.
type addressedDevice struct {
	*namedDevice
	addr string
}

// IPAddress returns the device's host.
func (a addressedDevice) IPAddress() string { return a.addr }

// registerIOSSeeding registers a method named enginetest.<suffix> that
// seeds the device its login parameter names, a required one, recording
// what it receives in got; with ios set it can seed a Cisco IOS device.
func registerIOSSeeding(t *testing.T, suffix string, got *map[string]string, ios bool) string {
	return registerIOSSeedingParam(t, suffix, got, ios, true)
}

// registerIOSSeedingParam is registerIOSSeeding with the login parameter
// required or optional.
func registerIOSSeedingParam(t *testing.T, suffix string, got *map[string]string, ios, required bool) string {
	t.Helper()
	name := "enginetest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:              collection.StatusImplemented,
			Reversibility:       collection.Reversibility{Notes: "a test fixture that changes nothing"},
			SeedsLogin:          "login",
			SeedsLoginIOSSecret: ios,
			Doc:                 collection.Doc{Params: []collection.Param{{Name: "login", Type: "string", Required: required}}},
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

// TestLoginSeed_ACiscoDeviceGetsTypeNineHashesOnly: a method that can seed
// IOS gets the username and type 9 hashes of the password and the enable
// password, each verifying, and neither password nor a key; a credential
// with no enable password seeds no enable secret; a method that cannot seed
// IOS, and a credential with no password, are refused before the method
// runs.
func TestLoginSeed_ACiscoDeviceGetsTypeNineHashesOnly(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	keyOnly, err := credential.Generate("netops", "")
	if err != nil {
		t.Fatal(err)
	}
	keyOnly.Password = ""
	store := vault{
		"CORE1":    {Username: "netops", Password: "login-pw", EnablePassword: "enable-pw"},
		"CONSOLE1": {Username: "netops", Password: "login-pw"},
		"BR1":      {Username: "netops", Password: "login-pw"},
		"KEY":      keyOnly,
	}
	var got map[string]string
	ios := registerIOSSeeding(t, "ios", &got, true)
	plain := registerIOSSeeding(t, "plain", &got, false)
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(vault{}),
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(store, ciscoInventory{missing: map[string]bool{"gone": true}})))
	run := func(method, login string) error {
		got = nil
		_, err := exec.Execute(context.Background(), &engine.Task{FQCN: method, Params: map[string]any{"login": login}}, newNamedDevice("cml"))
		return err
	}

	if err := run(ios, "CORE1"); err != nil {
		t.Fatal(err)
	}
	secret, enable := got[wire.SecretSeedIOSSecret], got[wire.SecretSeedIOSEnableSecret]
	if got[wire.SecretSeedUsername] != "netops" || !strings.HasPrefix(secret, "$9$") || !strings.HasPrefix(enable, "$9$") {
		t.Fatalf("an IOS seed: %v", got)
	}
	if ok, err := iossecret.Verify("login-pw", secret); !ok || err != nil {
		t.Errorf("the login secret does not verify: %v, %v", ok, err)
	}
	if ok, err := iossecret.Verify("enable-pw", enable); !ok || err != nil {
		t.Errorf("the enable secret does not verify: %v, %v", ok, err)
	}
	if got[wire.SecretSeedHost] != "10.10.20.181" {
		t.Errorf("the seed carries the device's address as %q", got[wire.SecretSeedHost])
	}
	for _, key := range []string{wire.SecretSeedPassword, wire.SecretSeedAuthorizedKey, wire.SecretSeedPasswordHash} {
		if got[key] != "" {
			t.Errorf("an IOS seed carried %s", key)
		}
	}
	for k, v := range got {
		if strings.Contains(v, "login-pw") || strings.Contains(v, "enable-pw") {
			t.Errorf("%s carries a password itself", k)
		}
	}

	if err := run(ios, "BR1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got[wire.SecretSeedIOSEnableSecret]; ok || got[wire.SecretSeedIOSSecret] == "" {
		t.Errorf("a credential with no enable password seeded %v", got)
	}
	if _, ok := got[wire.SecretSeedHost]; ok {
		t.Errorf("a device with no address seeded one: %v", got)
	}
	if err := run(ios, "CONSOLE1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got[wire.SecretSeedHost]; ok {
		t.Errorf("a device that claims no address seeded its stale host: %v", got)
	}

	// An optional device left empty seeds nothing, and the method still
	// runs; a required one left empty is refused.
	optional := registerIOSSeedingParam(t, "optional", &got, true, false)
	if err := run(optional, ""); err != nil || got == nil || got[wire.SecretSeedIOSSecret] != "" || got[wire.SecretSeedUsername] != "" {
		t.Errorf("an empty optional device: err %v, secrets %v", err, got)
	}
	if err := run(ios, ""); err == nil || !strings.Contains(err.Error(), "must name the inventory device") {
		t.Errorf("an empty required device: %v", err)
	}
	// Where no login can be seeded (a Runner), an optional device left
	// empty still runs, and one named is refused.
	unseeded := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(vault{}))
	for login, want := range map[string]string{"": "", "CORE1": "only the pleiades CLI"} {
		got = nil
		_, err := unseeded.Execute(context.Background(), &engine.Task{FQCN: optional, Params: map[string]any{"login": login}}, newNamedDevice("cml"))
		if (want == "" && (err != nil || got == nil)) || (want != "" && (err == nil || !strings.Contains(err.Error(), want))) {
			t.Errorf("no seeder, login %q: %v, want %q", login, err, want)
		}
	}

	for why, tt := range map[string]struct{ method, login, want string }{
		"a method that cannot seed IOS": {plain, "CORE1", "cannot seed one"},
		"a credential with no password": {ios, "KEY", "needs a username and a password"},
		"a login not in the inventory":  {ios, "gone", "no device named gone"},
	} {
		err := run(tt.method, tt.login)
		if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
			t.Errorf("%s: err = %v, want one mentioning %q, and the method not run", why, err, tt.want)
		}
	}
}

// TestRegister_RefusesAnIOSSeedWithNoLoginToSeed: SeedsLoginIOSSecret
// without SeedsLogin names no device, and is refused at registration.
func TestRegister_RefusesAnIOSSeedWithNoLoginToSeed(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	err := collection.Register(collection.Descriptor{
		Name: "enginetest.nologin",
		Manifest: collection.Manifest{
			Status:              collection.StatusImplemented,
			Reversibility:       collection.Reversibility{Notes: "a test fixture that changes nothing"},
			SeedsLoginIOSSecret: true,
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "without SeedsLogin") {
		t.Errorf("Register = %v", err)
	}
}
