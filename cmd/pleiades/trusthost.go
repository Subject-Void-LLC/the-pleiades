// The trust-host command: adding a device's SSH host keys to the
// known_hosts file every SSH connection this CLI makes verifies against.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/hosttrust"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// hostKeysMethod is the method --from-console runs on the VM's host.
const hostKeysMethod = "virt.vbox.vm.host_keys"

// runTrustHost trusts a device's SSH host keys, taken one of two ways.
// --from-console reads the keys a VM printed on its serial console, over
// the authenticated connection to the VirtualBox host that runs it, so
// they are the VM's own. --first-connect takes whatever answers at the
// device's address, which proves nothing, and says so every time.
func runTrustHost(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"first-connect": true, "replace": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades trust-host <device> (--from-console <virtualbox host> [--vm <name>] | --first-connect) [--replace]: %w", err)
	}
	fs := flag.NewFlagSet("trust-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	fromConsole := fs.String("from-console", "", "the VirtualBox host running the device's VM, whose console log holds its host keys")
	vm := fs.String("vm", "", "with --from-console, the VM's name on the host, when it is not the device's")
	firstConnect := fs.Bool("first-connect", false, "trust whatever host keys answer at the device's address, unverified")
	replace := fs.Bool("replace", false, "drop keys already trusted for the device's address that differ, as after a VM is made again")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the keys")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if (*fromConsole == "") == !*firstConnect {
		return errors.New("name one source of keys: --from-console <virtualbox host>, or --first-connect")
	}
	if *vm != "" && *fromConsole == "" {
		return errors.New("--vm only applies to --from-console")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+30*time.Second)
	defer cancel()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	device, err := repo.GetByName(ctx, name)
	if err != nil {
		return err
	}
	address, err := sshAddress(device)
	if err != nil {
		return err
	}
	var keys []ssh.PublicKey
	if *firstConnect {
		fmt.Fprintf(os.Stderr, "WARNING: trusting whatever answered at %s without verifying it: anything on that network could have answered as %s\n", address, name)
		keys, err = hosttrust.Scan(ctx, address, 10*time.Second)
	} else {
		if *vm == "" {
			*vm = name
		}
		keys, err = consoleKeys(ctx, *dir, repo, *fromConsole, *vm, *timeout)
	}
	if err != nil {
		return err
	}
	path, err := remoteexec.KnownHostsFile()
	if err != nil {
		return err
	}
	out, err := hosttrust.Merge(path, address, keys, *replace)
	if err != nil {
		return err
	}
	for _, key := range keys {
		sum := sha256.Sum256(key.Marshal())
		fmt.Printf("%s %s SHA256:%s\n", address, key.Type(), base64.RawStdEncoding.EncodeToString(sum[:]))
	}
	fmt.Printf("%s: %d key(s) added, %d already trusted, %d replaced\n", path, out.Added, out.Known, out.Removed)
	return nil
}

// sshAddress is the host and port a device is reached at over SSH.
func sshAddress(device inventory.InventoryItem) (string, error) {
	ssh, ok := device.(capability.SSHTransportCapable)
	if !ok || ssh.SSHHost() == "" {
		return "", fmt.Errorf("device %q is not reached over SSH at an address", device.Name())
	}
	port := ssh.SSHPort()
	if port == 0 || port == 22 {
		return ssh.SSHHost(), nil
	}
	return ssh.SSHHost() + ":" + strconv.Itoa(port), nil
}

// consoleKeys runs virt.vbox.vm.host_keys for vm on the VirtualBox host
// named host, with that host's own stored credential, and returns the
// keys it read off the VM's console.
func consoleKeys(ctx context.Context, dir string, repo inv.Repository, host, vm string, timeout time.Duration) ([]ssh.PublicKey, error) {
	device, err := repo.GetByName(ctx, host)
	if err != nil {
		return nil, err
	}
	desc, ok := collection.Lookup(hostKeysMethod)
	if !ok {
		return nil, fmt.Errorf("%s is not in this build", hostKeysMethod)
	}
	for _, need := range desc.Manifest.RequiredCapabilities {
		if !device.HasCapability(need) {
			return nil, fmt.Errorf("device %q does not declare %s; set virtualbox: true on it", host, need)
		}
	}
	rc, err := engine.NewCredentialRunbookContext(credential.NewLazyFileStore(dir))(ctx, device)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"name": vm, "timeout": int(timeout / time.Second)}
	if _, err := desc.Invoke(ctx, rc, device, params); err != nil {
		return nil, err
	}
	facts, ok := rc.(engine.FactCollector)
	if !ok {
		return nil, fmt.Errorf("%s's answer could not be read", hostKeysMethod)
	}
	lines, _ := facts.Facts()["ssh_host_keys"].([]string)
	var keys []ssh.PublicKey
	for _, line := range lines {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("%s gave a key that does not parse: %w", hostKeysMethod, err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s found no host keys for %s", hostKeysMethod, vm)
	}
	return keys, nil
}
