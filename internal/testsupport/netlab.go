// A small real network for a test: FRRouting routers and Open vSwitch
// switches in containers, wired by Docker networks, each reached over SSH
// as an unprivileged account the way a real device is. And Batfish.
package testsupport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The images a net lab starts. The two base images are pulled; the router
// and switch images are built from them (testdata/netlab) and never pulled.
const (
	// FRRBaseImage is the FRRouting release the test routers run.
	FRRBaseImage = "quay.io/frrouting/frr:10.1.1"
	// OVSBaseImage is the Alpine release the test switches install Open
	// vSwitch on, from its own package repository.
	OVSBaseImage = "alpine:3.20.10"
	// BatfishImage is the Batfish release the network model tests ask,
	// the build its REST API was traced against (Phase 127b's spikes).
	BatfishImage = "batfish/batfish:2026.08.27.3685"
)

// The local tags the net lab's images are built under.
const (
	netLabRouterTag = "pleiades/netlab-frr:10.1.1"
	netLabSwitchTag = "pleiades/netlab-ovs:3.20.10"
)

// NetLabUser is the account every net lab node accepts, by key only.
const NetLabUser = "netops"

// NetLabLink is one link of a net lab: a Docker network with its own
// subnet, and the address each node on it holds.
type NetLabLink struct {
	// Name names the link in a test's own terms ("r1-r2").
	Name string
	// Subnet is the link's network, small and fixed so a test knows every
	// address on it.
	Subnet netip.Prefix
	// Ends maps each node on the link to its address there.
	Ends map[string]netip.Addr
}

// NetLabNode is one running node.
type NetLabNode struct {
	// Name is the node's name in the test.
	Name string
	// Container is the running container.
	Container testcontainers.Container
	// Host and Port are its SSH address from the test.
	Host string
	Port int
	// HostKey is the key its SSH server presented.
	HostKey ssh.PublicKey
}

// NetLab is a running net lab.
type NetLab struct {
	// Nodes are the routers and switches, by name.
	Nodes map[string]*NetLabNode
	// Links are the links as given.
	Links []NetLabLink
	// PrivateKeyPEM is the key every node accepts for NetLabUser, in the
	// OpenSSH PEM form a stored credential takes.
	PrivateKeyPEM []byte
}

// buildNetLabImage builds one of testdata/netlab's images from its pinned
// base, once per test binary in practice (docker's layer cache makes a
// repeat a no-op).
func buildNetLabImage(tb testing.TB, dir, base, tag string) {
	tb.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		tb.Skip("docker not found on PATH")
	}
	src := filepath.Join(RepoRoot(tb), "internal", "testsupport", "testdata", "netlab", dir)
	cmd := exec.Command("docker", "build", "--build-arg", "BASE="+base, "-t", tag, src) // #nosec G204 -- fixed arguments and a path in this repository
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("building %s: %v\n%s", tag, err, out)
	}
}

// StartNetLab starts routers (FRR) and switches (Open vSwitch) wired by
// links, every node also on one management network that carries its SSH,
// and returns them once each answers SSH as NetLabUser. Routers run
// privileged, as FRR's ospfd needs. Everything is removed when tb ends.
func StartNetLab(tb testing.TB, routers, switches []string, links []NetLabLink) *NetLab {
	tb.Helper()
	ctx := context.Background()
	if len(routers) > 0 {
		buildNetLabImage(tb, "frr", FRRBaseImage, netLabRouterTag)
	}
	if len(switches) > 0 {
		buildNetLabImage(tb, "ovs", OVSBaseImage, netLabSwitchTag)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		tb.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		tb.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		tb.Fatal(err)
	}
	lab := &NetLab{Nodes: map[string]*NetLabNode{}, Links: links, PrivateKeyPEM: pem.EncodeToMemory(block)}

	// Each network is named so the management network sorts first: Docker
	// Desktop forwards a published port through the container's network
	// whose name sorts first, whatever its gateway priority or network mode
	// (measured 2026-10-22). With random names, half the labs reached a node
	// over a link, so taking that link down cut the test's own SSH session,
	// and an internal link's address refused it.
	run := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	mgmt := netLabNetwork(tb, "netlab-"+run+"-0-mgmt", nil)
	linkNets := map[string]*testcontainers.DockerNetwork{}
	for _, l := range links {
		// Not internal: Docker drops a bridged packet on an internal network
		// whose destination is outside the subnet, and OSPF's hellos go to
		// 224.0.0.5, so no adjacency ever formed. The management network
		// keeps the default route (its gateway priority).
		linkNets[l.Name] = netLabNetwork(tb, "netlab-"+run+"-1-"+l.Name, &mobynetwork.IPAM{
			Config: []mobynetwork.IPAMConfig{{Subnet: l.Subnet}},
		})
	}

	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	start := func(name, image, ready string, privileged bool) {
		networks := []string{mgmt.Name}
		addrs := map[string]netip.Addr{}
		for _, l := range links {
			if a, ok := l.Ends[name]; ok {
				networks = append(networks, linkNets[l.Name].Name)
				addrs[linkNets[l.Name].Name] = a
			}
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        image,
				Name:         fmt.Sprintf("netlab-%s-%d", name, time.Now().UnixNano()),
				Networks:     networks,
				ExposedPorts: []string{"22/tcp"},
				Env:          map[string]string{"NETOPS_AUTHORIZED_KEY": authorized},
				HostConfigModifier: func(hc *container.HostConfig) {
					hc.Privileged = privileged
				},
				EndpointSettingsModifier: func(eps map[string]*mobynetwork.EndpointSettings) {
					for netName, ep := range eps {
						if a, ok := addrs[netName]; ok {
							ep.IPAMConfig = &mobynetwork.EndpointIPAMConfig{IPv4Address: a}
						}
						if netName == mgmt.Name {
							ep.GwPriority = 100
						}
					}
				},
				WaitingFor: wait.ForAll(wait.ForLog(ready).WithStartupTimeout(ContainerStartupTimeout), SSHGreeting("22/tcp")),
			},
			Started: true,
		})
		if err != nil {
			if c != nil {
				_ = c.Terminate(context.Background())
			}
			tb.Fatalf("starting %s: %v", name, err)
		}
		tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
		host, err := c.Host(ctx)
		if err != nil {
			tb.Fatal(err)
		}
		mapped, err := c.MappedPort(ctx, "22/tcp")
		if err != nil {
			tb.Fatal(err)
		}
		node := &NetLabNode{Name: name, Container: c, Host: host, Port: int(mapped.Num())}
		node.HostKey = netLabHostKey(tb, node, signer)
		lab.Nodes[name] = node
	}
	for _, r := range routers {
		start(r, netLabRouterTag, "netlab router ready", true)
	}
	for _, s := range switches {
		start(s, netLabSwitchTag, "netlab switch ready", false)
	}
	return lab
}

// netLabNetwork creates a bridge network named name, removed when tb
// ends. network.New names every network with a fresh UUID, and the order
// of the names is what decides the path a published port takes.
func netLabNetwork(tb testing.TB, name string, ipam *mobynetwork.IPAM) *testcontainers.DockerNetwork {
	tb.Helper()
	//nolint:staticcheck // NetworkRequest is the one way to choose a network's name
	n, err := testcontainers.GenericNetwork(context.Background(), testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{Name: name, Driver: "bridge", Labels: testcontainers.GenericLabels(), IPAM: ipam},
	})
	if err != nil {
		tb.Fatalf("creating network %s: %v", name, err)
	}
	nw := n.(*testcontainers.DockerNetwork)
	tb.Cleanup(func() { _ = nw.Remove(context.Background()) })
	return nw
}

// netLabHostKey logs in once as NetLabUser and returns the host key the
// node presented, which a test then trusts in a known_hosts file of its
// own.
func netLabHostKey(tb testing.TB, node *NetLabNode, signer ssh.Signer) ssh.PublicKey {
	tb.Helper()
	var key ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: NetLabUser,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k
			return nil
		},
		Timeout: 30 * time.Second,
	}
	addr := net.JoinHostPort(node.Host, fmt.Sprint(node.Port))
	deadline := time.Now().Add(SSHDStartupTimeout)
	for {
		client, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			_ = client.Close()
			return key
		}
		if time.Now().After(deadline) {
			tb.Fatalf("logging in to %s as %s: %v", node.Name, NetLabUser, err)
		}
		time.Sleep(time.Second)
	}
}

// TrustHostKeys writes every node's host key to a known_hosts file of the
// test's own and points remoteexec at it for the rest of tb, so a method
// under test verifies each node's key as it would a real device's.
func (l *NetLab) TrustHostKeys(tb testing.TB) {
	tb.Helper()
	var lines []byte
	for _, n := range l.Nodes {
		lines = append(lines, []byte(knownhosts.Line([]string{net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}, n.HostKey)+"\n")...)
	}
	path := filepath.Join(tb.TempDir(), "known_hosts")
	if err := os.WriteFile(path, lines, 0o600); err != nil {
		tb.Fatal(err)
	}
	tb.Setenv(remoteexec.KnownHostsEnv, path)
}

// Secrets is the credential every node accepts, as a run injects one.
func (l *NetLab) Secrets() map[string]string {
	return map[string]string{wire.SecretUsername: NetLabUser, wire.SecretPrivateKeyPEM: string(l.PrivateKeyPEM)}
}

// NetLabDevice is a node as a device: the shared inventory stub, holding
// the capabilities a test names, plus the SSH accessors Connect reads.
type NetLabDevice struct {
	*inventorytest.Stub
	host string
	port int
}

// SSHHost returns the node's published SSH address.
func (d *NetLabDevice) SSHHost() string { return d.host }

// SSHPort returns the node's published SSH port.
func (d *NetLabDevice) SSHPort() int { return d.port }

// Device returns the node as an active device holding caps.
func (n *NetLabNode) Device(caps ...capability.Name) inventory.InventoryItem {
	return &NetLabDevice{Stub: &inventorytest.Stub{
		StubID: inventory.DeviceID(n.Name), StubName: n.Name, StubState: inventory.StateActive, Caps: caps,
	}, host: n.Host, port: n.Port}
}

// Exec runs argv in node's container as root, for a test's own setup and
// inspection (never for what the method under test does), and returns its
// output.
func (n *NetLabNode) Exec(tb testing.TB, argv ...string) string {
	tb.Helper()
	code, r, err := n.Container.Exec(context.Background(), argv, tcexec.Multiplexed())
	if err != nil {
		tb.Fatalf("exec %v on %s: %v", argv, n.Name, err)
	}
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		k, rerr := r.Read(buf)
		b.Write(buf[:k])
		if rerr != nil {
			break
		}
	}
	if code != 0 {
		tb.Fatalf("exec %v on %s exited %d: %s", argv, n.Name, code, b.String())
	}
	return b.String()
}

// InterfaceWith returns the name of node's interface holding addr, read
// from the container rather than assumed from the order Docker attached
// its networks in.
func (n *NetLabNode) InterfaceWith(tb testing.TB, addr netip.Addr) string {
	tb.Helper()
	for _, line := range strings.Split(n.Exec(tb, "ip", "-o", "-4", "addr", "show"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && strings.HasPrefix(fields[3], addr.String()+"/") {
			return fields[1]
		}
	}
	tb.Fatalf("%s has no interface holding %s", n.Name, addr)
	return ""
}

// StartBatfish starts the pinned Batfish, capped at 2 GB, and returns its
// base URL once its API answers. It is removed when tb ends.
func StartBatfish(tb testing.TB) string {
	tb.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        BatfishImage,
			ExposedPorts: []string{"9996/tcp"},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Memory = 2 << 30
			},
			WaitingFor: wait.ForHTTP("/v2/version").WithPort("9996/tcp").WithStatusCodeMatcher(func(code int) bool { return code == http.StatusOK }).WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting Batfish: %v", err)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "9996/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprint(port.Num())))
}
