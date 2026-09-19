// Package main is extprog, a real external Collection built with
// pkg/external exactly as a third-party author would build one, for
// internal/loader's tests.
//
// The shell-script fixtures in those tests can misbehave in ways a Go
// program built on pkg/external never would, which is what they are for.
// This one is the other half: proof that the loader and the real SDK agree
// about the whole contract (describe on stdout, the request on stdin, the
// response on file descriptor 3, the mode, the device and the secret)
// rather than the loader merely agreeing with a script written to match
// it.
//
// It lives under testdata/ so `go build ./...` and `go vet ./...` never
// see it. The tests build it by path into a temporary directory.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"syscall"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The method names this program provides. internal/loader's tests name
// them too, and the two must agree.
const (
	runMethod   = "loadertest.goprog.run"
	failMethod  = "loadertest.goprog.fail"
	reachMethod = "loadertest.goprog.reach"
)

// reversibility is every method's answer: these fixtures change nothing.
var reversibility = collection.Reversibility{Notes: "a test fixture that changes nothing on any device"}

// main hands both methods to external.Main.
func main() {
	external.Main(
		collection.Descriptor{
			Name: runMethod,
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: reversibility,
				SupportsCheck: true,
			},
			Invoke: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
				return collection.Result{Changed: true}, echo(rc, device, params, "invoke")
			},
			Check: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
				return collection.Result{Changed: false}, echo(rc, device, params, "check")
			},
		},
		collection.Descriptor{
			Name: failMethod,
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: reversibility,
			},
			Invoke: fail,
		},
		collection.Descriptor{
			Name: reachMethod,
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: reversibility,
			},
			Invoke: reach,
		},
	)
}

// echo records what the running function received as stats: which
// function ran, a param, the device's name and address, and whether the
// password it was handed hashes to the digest the test sent as a param.
// The password itself is never echoed.
func echo(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, ran string) error {
	sum := sha256.Sum256([]byte(rc.InjectSecrets()["password"]))
	want, _ := params["password_sha256"].(string)
	stats := map[string]any{
		"ran":              ran,
		"echoed":           params["message"],
		"password_matches": hex.EncodeToString(sum[:]) == want,
	}
	if device != nil {
		stats["device_name"] = device.Name()
	}
	for key, value := range stats {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}

// fail reports a failure that quotes the password it was handed, the way
// a careless method might, so a test can prove the loader masks it before
// the error goes anywhere.
func fail(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	return collection.Result{}, fmt.Errorf("login rejected for password %s", rc.InjectSecrets()["password"])
}

// reach tries everything a hostile program would try and reports what it
// got, as stats, so a test can assert on what confinement allowed: opening
// each path in the "paths" param, signalling the process that started it,
// and writing a file in its own temporary directory. Each stat is "ok" or
// the error's text. It opens and never reads, so nothing it reaches is
// ever echoed back.
func reach(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	paths, _ := params["paths"].([]any)
	for _, p := range paths {
		path, _ := p.(string)
		// #nosec G304 -- opening whatever path the test names is this
		// probe's whole purpose: it reports whether confinement let it.
		f, err := os.Open(path)
		if err == nil {
			_ = f.Close()
		}
		if err := rc.SetStat("open:"+path, outcome(err)); err != nil {
			return collection.Result{}, err
		}
	}
	if err := rc.SetStat("signal_parent", outcome(syscall.Kill(os.Getppid(), 0))); err != nil {
		return collection.Result{}, err
	}
	f, err := os.CreateTemp("", "reach")
	if err == nil {
		_ = f.Close()
	}
	return collection.Result{}, rc.SetStat("write_tmpdir", outcome(err))
}

// outcome is "ok" for a nil error and the error's text otherwise.
func outcome(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ok"
}
