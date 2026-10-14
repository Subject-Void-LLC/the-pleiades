// The stress gate for the container engine port: many devices, each running
// its tasks in turn, all at once over a run's connection pool, against a real
// engine behind a real sshd.
package onboard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// pooledRC is a run's RunbookContext as the engine hands one to a method:
// the device's credential, and the run's one connection pool, which lends
// each device its own login and keeps it between that device's tasks.
type pooledRC struct {
	secrets map[string]string
	pool    *remoteexec.Pool
}

func (r pooledRC) InjectSecrets() map[string]string { return r.secrets }
func (pooledRC) SetStat(string, any) error          { return nil }
func (pooledRC) EmitFact(string, any) error         { return nil }
func (r pooledRC) ConnectionPool() *remoteexec.Pool { return r.pool }

// fleet rebuilds an onboarded device as n records of its own, each with
// its own name, so the pool lends each its own login: one engine host
// standing in for n.
func fleet(t *testing.T, onboardedProps inventory.Properties, n int) []inventory.InventoryItem {
	t.Helper()
	ctor, _ := record.LookupType(container.TypeHost)
	props := map[string]inventory.PropertyValue{}
	for k, v := range onboardedProps.Raw() {
		props[k] = v
	}
	out := make([]inventory.InventoryItem, n)
	for i := range n {
		name := "host-" + strconv.Itoa(i)
		item, err := ctor(record.Record{ID: inventory.DeviceID(name), Name: name, Type: container.TypeHost, Properties: props})
		if err != nil {
			t.Fatal(err)
		}
		out[i] = item
	}
	return out
}

// operate runs one task's worth of engine work: a session limited to two
// operations, the engine's info and a container listing, then closes it.
func operate(ctx context.Context, rc pooledRC, device inventory.InventoryItem, want capability.EngineID) error {
	s, err := containerengine.Open(ctx, rc, device, nil, "stress.operate", containerengine.OpInfo, containerengine.OpContainerList)
	if err != nil {
		return err
	}
	defer s.Close()
	info, err := s.API.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("info over %s: %w", s.Carrier, err)
	}
	if capability.EngineID(info.Info.ID) != want {
		return fmt.Errorf("info over %s answered engine %s, want %s", s.Carrier, info.Info.ID, want)
	}
	if _, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true}); err != nil {
		return fmt.Errorf("list over %s: %w", s.Carrier, err)
	}
	return nil
}

// runFleet runs ops operations on every device at once, each device's in
// turn as a run's tasks on one device are, and returns every failure.
func runFleet(ctx context.Context, rc pooledRC, devices []inventory.InventoryItem, ops int, want capability.EngineID) []error {
	var mu sync.Mutex
	var failed []error
	var wg sync.WaitGroup
	for _, device := range devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ops {
				if err := operate(ctx, rc, device, want); err != nil {
					mu.Lock()
					failed = append(failed, fmt.Errorf("%s, operation %d: %w", device.Name(), i, err))
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	return failed
}

// established counts the SSH connections the fixture's sshd holds open.
func established(t *testing.T, ctx context.Context, d *testsupport.EngineHost) int {
	t.Helper()
	out, err := d.RootExec(ctx, "sh", "-c", "netstat -tn | grep ':2222 ' | grep -c ESTABLISHED || true")
	if err != nil {
		t.Fatalf("netstat on the fixture: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("netstat count %q: %v", out, err)
	}
	return n
}

// TestEngineStress_FiftyHostsTenOperationsEach is the 119b stress gate: 50
// devices, each running 10 operations in turn, all at once, over the run's
// connection pool, against a real engine behind a real sshd. Every one must
// answer from the engine onboarding pinned, and the pool must have kept one
// login per device rather than opening one per operation. One sshd stands
// in for 50 hosts, so it accepts that many logins starting together
// (MaxStartups), as 50 sshds would each accept one.
func TestEngineStress_FiftyHostsTenOperationsEach(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	const hosts, ops = 50, 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{SSHDConfig: "AllowTcpForwarding local\nMaxStartups 200\n"})
	trustFixture(t, d)
	secrets := passwordFor(testsupport.DockerHostUser)
	one, _ := onboarded(t, container.TypeHost, map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}, secrets)

	pool := remoteexec.NewPool(time.Minute)
	defer func() { _ = pool.Close() }()
	rc := pooledRC{secrets: secrets, pool: pool}
	devices := fleet(t, one.Properties(), hosts)

	begun := time.Now()
	for _, err := range runFleet(ctx, rc, devices, ops, capability.EngineID(d.EngineID(t))) {
		t.Error(err)
	}
	took := time.Since(begun)
	t.Logf("%d hosts x %d operations: %s (%s per operation per host)", hosts, ops, took, took/ops)

	if n := established(t, ctx, d); n != hosts {
		t.Errorf("the fixture holds %d SSH connections after the run, want one per device (%d): the pool did not keep each device's login", n, hosts)
	}
	_ = pool.Close()
}

// TestEngineStress_DialStdioLeavesNothingRunning is the failure-mode walk's
// "a dial-stdio process left on the host": devices reached only over the
// stdio carrier run many sessions, each request its own `docker system
// dial-stdio`, and once every session has closed, while the run's pool still
// holds each device's login, the host's process list holds none of them.
// Checking before the pool closes is the point: closing a login would end
// its processes anyway, so only this order shows that closing each
// request's channel does. Each device also keeps its one login across its
// tasks, since every process it started was seen to exit.
func TestEngineStress_DialStdioLeavesNothingRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	const hosts, ops = 5, 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{SSHDConfig: "DisableForwarding yes\n"})
	trustFixture(t, d)
	secrets := passwordFor(testsupport.DockerHostUser)
	one, got := onboarded(t, container.TypeHost, map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}, secrets)
	if carriers := got.Facts[engines.FactKey(capability.EngineDocker, engines.FieldCarriers)]; fmt.Sprint(carriers) != "[stdio]" {
		t.Fatalf("carriers %v, want stdio alone", carriers)
	}

	pool := remoteexec.NewPool(time.Minute)
	defer func() { _ = pool.Close() }()
	rc := pooledRC{secrets: secrets, pool: pool}
	begun := time.Now()
	for _, err := range runFleet(ctx, rc, fleet(t, one.Properties(), hosts), ops, capability.EngineID(d.EngineID(t))) {
		t.Error(err)
	}
	t.Logf("%d hosts x %d operations over dial-stdio: %s", hosts, ops, time.Since(begun))

	out, err := d.RootExec(ctx, "sh", "-c", "ps -o args | grep 'dial-stdio' | grep -v grep || true")
	if err != nil {
		t.Fatalf("ps on the fixture: %v", err)
	}
	if left := strings.TrimSpace(out); left != "" {
		t.Errorf("dial-stdio processes still running after every session closed, with the logins still open:\n%s", left)
	}
	if n := established(t, ctx, d); n != hosts {
		t.Errorf("the fixture holds %d SSH connections, want one pooled login per device (%d)", n, hosts)
	}
}
