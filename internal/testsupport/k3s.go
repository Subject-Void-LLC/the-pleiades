// Kubernetes cluster fixtures: a real k3s API server in a container, at the
// oldest and newest supported Kubernetes minors, for the Kubernetes port's
// tests. k3s needs no CLI on the machine running the tests, only Docker.
package testsupport

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v3"
)

const (
	// K3sOldestImage is the oldest Kubernetes minor this platform supports,
	// pinned at Phase 119c's Pattern Entry (2026-10-14).
	K3sOldestImage = "rancher/k3s:v1.35.9-k3s1"
	// K3sNewestImage is the newest Kubernetes minor this platform supports,
	// pinned at the same time.
	K3sNewestImage = "rancher/k3s:v1.37.1-k3s1"
)

// k3sAPIPort is where the API server listens inside its container.
const k3sAPIPort = "6443/tcp"

// K3sOptions configures one k3s fixture.
type K3sOptions struct {
	// Image is K3sOldestImage or K3sNewestImage; empty means the newest.
	Image string
	// Networks are Docker networks the server joins, and Alias its name
	// on them, for a test that reaches it from another container (an sshd
	// bastion); the API server's certificate covers the alias.
	Networks []string
	Alias    string
	// MachineID, when set, is written to the node's /etc/machine-id before
	// it starts, as kind writes one into each node container; the node
	// then reports it as its machine ID. Without one a k3s node in a
	// container reports none.
	MachineID string
}

// K3sCluster is one running k3s server and what a test needs to reach it.
type K3sCluster struct {
	Container testcontainers.Container
	// Server is the API server's URL as this machine reaches it.
	Server string
	// Host and Port are Server's parts.
	Host string
	Port int
	// CAPEM is the cluster's certificate authority.
	CAPEM string
	// ClientCert and ClientKey are the PEM client certificate and key of
	// k3s's own administrator, from its kubeconfig.
	ClientCert, ClientKey string
	// Kubeconfig is k3s's own kubeconfig with its server rewritten to Server.
	Kubeconfig string
	// Alias is the server's name on opts.Networks, when it joined any.
	Alias string
}

// StartK3s starts a k3s server and waits until its API is ready and its
// node is Ready. It is terminated when tb ends. A machine with no docker
// command skips tb through Require.
func StartK3s(tb testing.TB, opts K3sOptions) *K3sCluster {
	tb.Helper()
	_, lookErr := exec.LookPath("docker")
	Require(tb, "docker", lookErr == nil, "the docker command is not on PATH")
	image := opts.Image
	if image == "" {
		image = K3sNewestImage
	}
	args := []string{"server",
		"--disable=traefik,metrics-server,servicelb,local-storage",
		"--tls-san=127.0.0.1", "--tls-san=localhost",
		"--write-kubeconfig-mode=644"}
	aliases := map[string][]string{}
	if opts.Alias != "" {
		args = append(args, "--tls-san="+opts.Alias)
		for _, n := range opts.Networks {
			aliases[n] = []string{opts.Alias}
		}
	}

	var files []testcontainers.ContainerFile
	if opts.MachineID != "" {
		files = append(files, testcontainers.ContainerFile{Reader: strings.NewReader(opts.MachineID + "\n"), ContainerFilePath: "/etc/machine-id", FileMode: 0o444})
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			Cmd:          args,
			ExposedPorts: []string{k3sAPIPort},
			// A Kubernetes node inside a container needs these; this is a
			// test fixture, never a deployment.
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Privileged = true
				hc.Tmpfs = map[string]string{"/run": "", "/var/run": ""}
			},
			Networks:       opts.Networks,
			NetworkAliases: aliases,
			Files:          files,
			// The first wait reads the log, never execs, on purpose. k3s
			// starts as pid 1 by moving each process in its root cgroup
			// into /init and then enabling every controller there, and a
			// process entering the root cgroup between those steps makes
			// each enable fail with EBUSY. k3s only warns, and kubelet
			// then dies on the missing controllers. A docker exec lands in
			// exactly that cgroup, so an exec probe sent the moment the
			// container starts loses that race on a loaded machine (seen
			// in CI, reproduced here with the container's CPU throttled).
			// k3s logs "Starting k3s" only after the move, so no exec
			// enters before it.
			//
			// The last wait is the one a client depends on: the API port
			// answering through its mapped host port. Every check before it
			// runs inside the container, and a server ready in there can
			// still be unreachable from the host until Docker forwards the
			// port (CONTRIBUTING's rule; FAILURE_PATTERNS 408's class): a
			// test's first request was refused that way on 2026-10-14. It
			// comes last, so a k3s that dies fails at the log wait in
			// seconds rather than as a port that never opens.
			WaitingFor: wait.ForAll(
				wait.ForLog("Starting k3s").WithStartupTimeout(ContainerStartupTimeout),
				wait.ForExec([]string{"kubectl", "get", "--raw", "/readyz"}).WithStartupTimeout(ContainerStartupTimeout),
				wait.ForExec([]string{"kubectl", "wait", "--for=condition=Ready", "node", "--all", "--timeout=5s"}).WithStartupTimeout(ContainerStartupTimeout),
				wait.ForListeningPort(k3sAPIPort).SkipInternalCheck().WithStartupTimeout(ContainerStartupTimeout),
			),
		},
		Started: true,
	})
	if err != nil {
		tail := ""
		if c != nil {
			// The server's own last words say why it stopped, which the
			// readiness error alone never does.
			if logs, lerr := c.Logs(context.Background()); lerr == nil {
				b, _ := io.ReadAll(logs)
				_ = logs.Close()
				tail = k3sLastWords(string(b))
			}
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting the k3s fixture (%s): %v\n%s", image, err, tail)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatalf("k3s fixture address: %v", err)
	}
	mapped, err := c.MappedPort(ctx, k3sAPIPort)
	if err != nil {
		tb.Fatalf("k3s fixture port: %v", err)
	}
	k := &K3sCluster{Container: c, Host: host, Port: int(mapped.Num()), Alias: opts.Alias}
	k.Server = "https://" + net.JoinHostPort(host, strconv.Itoa(k.Port))

	raw, err := k.exec(ctx, "cat", "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		tb.Fatalf("reading k3s's kubeconfig: %v", err)
	}
	if err := k.readKubeconfig(raw); err != nil {
		tb.Fatalf("k3s's kubeconfig: %v", err)
	}
	return k
}

// k3sLastWords is what a k3s server that stopped said about why: its last
// fifteen lines, after any earlier warning that it could not enable a
// cgroup controller. k3s prints those at its very start and only warns,
// and they are the cause when kubelet dies on missing controllers minutes
// of log later.
func k3sLastWords(logs string) string {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	start := max(len(lines)-15, 0)
	var said []string
	for _, l := range lines[:start] {
		if strings.Contains(l, "failed to enable controller") {
			said = append(said, l)
		}
	}
	return strings.Join(append(said, lines[start:]...), "\n")
}

// readKubeconfig takes the authority and the administrator's identity from
// k3s's own kubeconfig, and keeps the kubeconfig with its server rewritten
// to the address this machine reaches.
func (k *K3sCluster) readKubeconfig(raw string) error {
	var cfg struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CAData string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				CertData string `yaml:"client-certificate-data"`
				KeyData  string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return err
	}
	if len(cfg.Clusters) == 0 || len(cfg.Users) == 0 {
		return fmt.Errorf("no cluster or user in it")
	}
	decode := func(what, s string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(b) == 0 {
			return "", fmt.Errorf("its %s does not decode: %v", what, err)
		}
		return string(b), nil
	}
	var err error
	if k.CAPEM, err = decode("authority", cfg.Clusters[0].Cluster.CAData); err != nil {
		return err
	}
	if k.ClientCert, err = decode("client certificate", cfg.Users[0].User.CertData); err != nil {
		return err
	}
	if k.ClientKey, err = decode("client key", cfg.Users[0].User.KeyData); err != nil {
		return err
	}
	k.Kubeconfig = strings.Replace(raw, cfg.Clusters[0].Cluster.Server, k.Server, 1)
	return nil
}

// Kubectl runs kubectl inside the server and returns its combined output; a
// non-zero exit is an error carrying it. It is a test's independent path to
// the cluster, never the code under test.
func (k *K3sCluster) Kubectl(ctx context.Context, args ...string) (string, error) {
	return k.exec(ctx, append([]string{"kubectl"}, args...)...)
}

// exec runs argv inside the server container.
func (k *K3sCluster) exec(ctx context.Context, argv ...string) (string, error) {
	code, reader, err := k.Container.Exec(ctx, argv, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("exec in the k3s fixture: %w", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("reading exec output: %w", err)
	}
	if code != 0 {
		return string(out), fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), code, out)
	}
	return string(out), nil
}

// ServiceAccountToken creates a service account named name in namespace,
// binds it to clusterRole there (or cluster-wide when namespace is empty;
// not at all when clusterRole is empty, for a caller binding a role of its
// own), and returns a bearer token for it, valid for an hour.
func (k *K3sCluster) ServiceAccountToken(tb testing.TB, namespace, name, clusterRole string) string {
	tb.Helper()
	ctx := context.Background()
	ns := namespace
	if ns == "" {
		ns = "default"
	}
	if _, err := k.Kubectl(ctx, "create", "serviceaccount", name, "-n", ns); err != nil {
		tb.Fatalf("creating service account %s: %v", name, err)
	}
	if clusterRole == "" {
		// The caller binds it itself.
		token, err := k.Kubectl(ctx, "create", "token", name, "-n", ns, "--duration=1h")
		if err != nil {
			tb.Fatalf("creating a token for %s: %v", name, err)
		}
		return strings.TrimSpace(token)
	}
	var bind []string
	if namespace == "" {
		bind = []string{"create", "clusterrolebinding", name, "--clusterrole=" + clusterRole, "--serviceaccount=" + ns + ":" + name}
	} else {
		bind = []string{"create", "rolebinding", name, "-n", namespace, "--clusterrole=" + clusterRole, "--serviceaccount=" + ns + ":" + name}
	}
	if _, err := k.Kubectl(ctx, bind...); err != nil {
		tb.Fatalf("binding service account %s: %v", name, err)
	}
	token, err := k.Kubectl(ctx, "create", "token", name, "-n", ns, "--duration=1h")
	if err != nil {
		tb.Fatalf("creating a token for %s: %v", name, err)
	}
	return strings.TrimSpace(token)
}
