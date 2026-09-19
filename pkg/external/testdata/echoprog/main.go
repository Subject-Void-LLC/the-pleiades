// Package main is echoprog, the smallest real external Collection: one
// implemented method, served by external.Main, built and run as a genuine
// child process by pkg/external's own tests.
//
// It exists so a test can prove Main's real wiring rather than Serve's
// buffers: os.Args reaching the command, os.Stdin carrying the request,
// file descriptor 3 carrying the response frame, and os.Exit carrying
// Serve's code. None of that is reachable from an in-process test, and all
// of it is what a third-party author's program actually runs.
//
// It lives under testdata/ so `go build ./...` and `go vet ./...` never
// see it. The test builds it explicitly, by path, into a temporary
// directory.
//
// The method echoes what it was handed into stats so the test can read
// back, from the far side of a real process boundary, which of the two
// functions ran and what each one received. It never echoes the secret
// itself: it reports only whether the secret it received hashes to the
// digest the test passed as a param, which proves the secret was usable
// inside the child without putting it into a response.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// methodName is the one method this program provides. The test in
// pkg/external/program_test.go names it too; the two must agree.
const methodName = "externaltest.echo.run"

// main hands the one method to external.Main, exactly as the package
// documentation tells a third-party author to.
func main() {
	external.Main(collection.Descriptor{
		Name: methodName,
		// The manifest carries a field of every kind describe has to
		// carry, so the test can compare what arrives on stdout against
		// what was declared here. The test's own expected copy must be
		// kept identical to this one.
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSSHTransport},
			Status:               collection.StatusImplemented,
			Reversibility:        collection.Reversibility{Notes: "a test fixture that changes nothing on any device"},
			SupportsCheck:        true,
			Doc:                  collection.Doc{Summary: "Echo what the method was handed, for pkg/external's tests."},
		},
		Invoke: invoke,
		Check:  check,
	})
}

// invoke is the execute-mode function. It reports Changed true so the
// test can tell its answer apart from check's.
func invoke(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := echo(rc, device, params, "invoke"); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, nil
}

// check is the check-mode function. It echoes exactly what invoke does,
// since a check receives the same arguments, but reports Changed false:
// this fixture's prediction is that a real run would change nothing,
// which is the opposite of what invoke reports and so tells the two apart.
func check(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := echo(rc, device, params, "check"); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: false}, nil
}

// echo records what the running function received as stats, under names
// the test reads back. ran names which of the two functions called it.
func echo(rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, ran string) error {
	// A method's own failure, on request, so the test can see it travel
	// back as the response's Error with a zero exit code rather than as a
	// broken process.
	if msg, ok := params["fail"].(string); ok && msg != "" {
		return errors.New(msg)
	}

	// Arbitrary output on stdout, on request, standing in for a stray
	// fmt.Println in a method's own code. The response frame goes to file
	// descriptor 3, so this must never corrupt it.
	if noise, ok := params["print"].(string); ok && noise != "" {
		fmt.Println(noise)
	}

	// The secret-derived boolean: hash the secret this process received
	// and compare it with the digest the test sent as a param. True proves
	// the secret crossed stdin and was usable here, without the secret
	// itself ever being written into a stat.
	secrets := rc.InjectSecrets()
	sum := sha256.Sum256([]byte(secrets["password"]))
	wantDigest, _ := params["password_sha256"].(string)
	passwordMatches := secrets["password"] != "" && hex.EncodeToString(sum[:]) == wantDigest

	stats := map[string]any{
		"ran":              ran,
		"echoed":           params["message"],
		"password_matches": passwordMatches,
	}

	// What the rebuilt device reports, so the test can see the device's
	// identity and capabilities crossed the boundary too.
	if device != nil {
		stats["device_name"] = device.Name()
		stats["ssh_capable"] = device.HasCapability(capability.NameSSHTransport)
		if ssh, ok := device.(capability.SSHTransportCapable); ok {
			stats["device_host"] = ssh.SSHHost()
			stats["device_port"] = ssh.SSHPort()
		}
	}

	for key, value := range stats {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}
