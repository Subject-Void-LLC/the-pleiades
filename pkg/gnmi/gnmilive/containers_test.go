// Package gnmilive: starting the two servers, once each for the package,
// and the devices and run context the tests reach them through.
package gnmilive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/srlinux"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// stratumImage is the Open Networking Foundation's Mininet with
// stratum_bmv2, which serves gNMI and gNOI on 50001, pinned by digest.
const stratumImage = "opennetworking/mn-stratum@sha256:352323780adf642664895689b7d2350f2e0be24780420c84bb5aa437d7ac9315"

// SR Linux's factory login, a throwaway container's, never a device's.
const (
	srlUser     = srlinux.User
	srlPassword = srlinux.Password
)

// server is one started container's gNMI address, and SR Linux's own
// command line for an SR Linux one.
type server struct {
	target string
	c      testcontainers.Container
	srl    *srlinux.SRLinux
}

// One container of each serves every test here: SR Linux takes a minute or
// more to start, and each test reads, or writes and reverts, what is its own.
var (
	srl, stratum         server
	srlOnce, stratumOnce sync.Once
	srlErr, stratumErr   error
)

// TestMain stops the shared containers once every test has finished, and
// holds every fact the net.gnmi methods emitted against SR Linux and Stratum
// to their declarations (pkg/collection/facttest), failing the binary for a
// fact a method does not declare, a value of another type, and a
// fact-declaring method of internal/catalog/net/gnmi these tests did not
// drive. A real device's answer is where a wrong type is likeliest; without
// docker the coverage is reported, not enforced.
func TestMain(m *testing.M) {
	code := facttest.Main(m, facttest.Options{
		Of:          "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/gnmi",
		Unavailable: testsupport.DockerMissing(),
	})
	for _, s := range []server{srl, stratum} {
		if s.c != nil {
			_ = testcontainers.TerminateContainer(s.c)
		}
	}
	os.Exit(code)
}

// startSRLinux runs SR Linux once, with gNOI added to its gRPC server's
// services (its factory configuration serves gNMI and gNSI only), and waits
// until both answer.
func startSRLinux(t *testing.T) server {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a privileged SR Linux container")
	}
	srlOnce.Do(func() { srl, srlErr = runSRLinux() })
	if srlErr != nil {
		t.Fatal(srlErr)
	}
	return srl
}

// runSRLinux starts the container (internal/testsupport, which adds gNOI to
// its services) and waits as a person would: until its TLS answers, whose
// certificate is learned and pinned, then until gNMI and gNOI answer.
func runSRLinux() (server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	srlinux, err := srlinux.Run(ctx)
	s := server{target: srlinux.Target, c: srlinux.Container, srl: srlinux}
	if err != nil {
		return s, err
	}
	if err := until(ctx, func(ctx context.Context) error {
		pin, err := learnPin(ctx, s.target)
		srlPin = pin
		return err
	}); err != nil {
		return s, fmt.Errorf("SR Linux's TLS never answered: %w", err)
	}
	for name, call := range map[string]func(context.Context, *gnmi.Client) error{
		"gNMI": func(ctx context.Context, c *gnmi.Client) error { _, err := c.Capabilities(ctx); return err },
		"gNOI": func(ctx context.Context, c *gnmi.Client) error { _, err := c.Time(ctx); return err },
	} {
		if err := until(ctx, func(ctx context.Context) error {
			client, err := srlClient(s.target, srlPassword)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			return call(ctx, client)
		}); err != nil {
			return s, fmt.Errorf("SR Linux's %s never answered: %w", name, err)
		}
	}
	return s, nil
}

// startStratum runs Stratum once and waits until its gNMI answers.
func startStratum(t *testing.T) server {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a privileged Stratum container")
	}
	stratumOnce.Do(func() { stratum, stratumErr = runStratum() })
	if stratumErr != nil {
		t.Fatal(stratumErr)
	}
	return stratum
}

// runStratum starts the container and readies it.
func runStratum() (server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        stratumImage,
			ExposedPorts: []string{"50001/tcp"},
			// Mininet builds its switch from network namespaces and veth
			// pairs, which needs a privileged container.
			HostConfigModifier: func(hc *container.HostConfig) { hc.Privileged = true },
			// Mininet's command line reads its standard input, and ends at
			// its end, taking the switch with it.
			ConfigModifier: func(cfg *container.Config) {
				cfg.Tty, cfg.OpenStdin = true, true
			},
			WaitingFor: wait.ForListeningPort("50001/tcp").WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return server{}, fmt.Errorf("starting %s: %w", stratumImage, err)
	}
	s, err := serverOf(ctx, c, "50001/tcp")
	if err != nil {
		return s, err
	}
	if err := until(ctx, func(ctx context.Context) error {
		client, err := gnmi.New(gnmi.Options{Target: s.target, Plaintext: true, Auth: gnmi.AuthNone})
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		_, err = client.Capabilities(ctx)
		return err
	}); err != nil {
		return s, fmt.Errorf("Stratum's gNMI never answered: %w", err)
	}
	return s, nil
}

// serverOf reads a started container's mapped port.
func serverOf(ctx context.Context, c testcontainers.Container, port string) (server, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return server{c: c}, err
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		return server{c: c}, err
	}
	return server{target: net.JoinHostPort(host, strconv.Itoa(int(mapped.Num()))), c: c}, nil
}

// until tries try every two seconds, each try bounded to ten, until it
// succeeds or ctx ends, returning the last failure.
func until(ctx context.Context, try func(context.Context) error) error {
	var last error
	for {
		tryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		last = try(tryCtx)
		cancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), last)
		case <-time.After(2 * time.Second):
		}
	}
}

// srlPin is SR Linux's certificate's fingerprint, learned once as a person
// learns it: from the refusal naming the certificate the device presented.
var srlPin string

// srlSecrets is SR Linux's login with password.
func srlSecrets(password string) map[string]string {
	return map[string]string{wire.SecretUsername: srlUser, wire.SecretPassword: password}
}

// srlPinned is SR Linux's device at target, its certificate pinned.
func srlPinned(target string) (inventory.InventoryItem, error) {
	return srlDevice(target, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: srlPin})
}

// srlClient is a client of SR Linux at target with password, its
// certificate pinned.
func srlClient(target, password string) (*gnmi.Client, error) {
	device, err := srlPinned(target)
	if err != nil {
		return nil, err
	}
	client, _, err := gnmi.ClientFor(device, "srl", srlSecrets(password))
	return client, err
}

// learnPin connects with the system's roots, which refuse SR Linux's
// self-signed certificate, and returns the fingerprint the refusal names.
func learnPin(ctx context.Context, target string) (string, error) {
	device, err := srlDevice(target, nil)
	if err != nil {
		return "", err
	}
	client, _, err := gnmi.ClientFor(device, "srl", srlSecrets(srlPassword))
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	_, err = client.Capabilities(ctx)
	var untrusted *devicetls.UntrustedError
	if !errors.As(err, &untrusted) {
		return "", fmt.Errorf("the system's roots did not refuse SR Linux's certificate: %v", err)
	}
	return untrusted.Fingerprint, nil
}

// srlDevice is a generic_gnmi device for SR Linux at target, built through
// the type's own constructor, with tls (its TLS properties) and a discovery
// granting GNMICapable, as onboarding records one.
func srlDevice(target string, tls map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	props := map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target}
	for k, v := range tls {
		props[k] = v
	}
	return buildGNMI(props)
}

// buildGNMI builds a generic_gnmi item from props, its discovery bound to
// them.
func buildGNMI(props map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	d := inventory.Discovery{Protocol: "gnmi", Capabilities: []capability.Name{capability.NameGNMI}}
	d.Binding = generic.Binding(generic.TypeGNMI, inventory.NewProperties(props))
	withDiscovery := map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}
	for k, v := range props {
		withDiscovery[k] = v
	}
	ctor, _ := record.LookupType(generic.TypeGNMI)
	return ctor(record.Record{ID: "srl", Name: "srl", Type: generic.TypeGNMI, Properties: withDiscovery})
}
