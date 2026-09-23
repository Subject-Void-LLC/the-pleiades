// The options a NATS test broker can vary on, and the pure function that
// turns them into a container request.
//
// The option set is deliberately closed. It covers the four axes the 33
// container starts in this repository actually varied on when they were
// surveyed (image, network alias, exposed ports, configuration file),
// plus mounting a file the configuration names, and nothing else. A
// caller cannot pass a testcontainers customizer through it, which is
// what makes the hazard in natsRequest's own comment unrepresentable
// rather than merely discouraged.
package testsupport

import (
	"strings"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NATSConfigPath is where a configuration supplied through WithNATSConfig
// is mounted inside the container.
//
// Exported because a configuration that names another file (a TLS
// certificate, say) has to agree with the path the file is mounted at,
// and two string literals that must agree are a drift waiting to happen.
const NATSConfigPath = "/etc/nats/nats.conf"

// defaultNATSPort is the client port a broker publishes unless a caller
// names its own set.
//
// It is never empty, and that is a correctness property rather than a
// convenience. An empty ExposedPorts does not publish nothing: testcontainers
// inspects the IMAGE and publishes everything its EXPOSE lines declare,
// which for this image is 4222, 6222 and 8222. A fixture whose whole claim
// is that some port is unreachable therefore has to name what it publishes,
// never leave the field off.
const defaultNATSPort = "4222/tcp"

// NATSOption adjusts one axis of a test broker.
type NATSOption func(*natsSettings)

// natsSettings is the resolved configuration a request is built from.
type natsSettings struct {
	image   string
	config  string
	ports   []string
	network *testcontainers.DockerNetwork
	aliases []string
	files   []testcontainers.ContainerFile
}

// WithNATSImage starts a different image from the pinned NATSImage.
//
// There is one legitimate caller shape: a test that asserts this
// repository's code REFUSES an unsupported server. Use the named constant
// for the version, never a literal, so the pin test can see it.
func WithNATSImage(ref string) NATSOption {
	return func(s *natsSettings) { s.image = ref }
}

// WithNATSConfig mounts conf as the server's configuration file and adds
// the flag that reads it.
//
// The configuration is TEXT rather than a path on the host. Every caller
// in this repository builds the text in the test, and two of them used to
// write it to a temp directory purely to satisfy an API that wanted a
// path. More importantly, an operator-mode configuration is where a seed
// or a password will eventually be written, and a host file outlives the
// process, survives a crash, and lands in whatever backs that directory.
// This keeps the only copy inside a container that is thrown away.
//
// A setting that nats-server accepts as a command line flag does NOT
// belong in here. The flag list is passed as well, and nats-server 2.14.4
// refuses a file that repeats one of them: giving it a jetstream block
// naming store_dir while the flags already carry "-sd /data" produced
// "Duplicate 'store_dir' configuration" and the server exited at boot.
// Put in the file only what the flags cannot express, which is what the
// Helm chart's own nats.conf says about itself.
func WithNATSConfig(conf string) NATSOption {
	return func(s *natsSettings) { s.config = conf }
}

// WithNATSExposedPorts REPLACES the published port set.
//
// Replaces rather than appends, because a fixture whose whole claim is
// that some port is unreachable has to be able to take the default away.
// Each port may be written "4222" or "4222/tcp"; the protocol is filled
// in when absent.
//
// NAMING NO PORTS PUBLISHES MORE, NOT FEWER, which reads backwards until
// you know why and was measured rather than assumed. When ExposedPorts is
// empty, testcontainers inspects the IMAGE and publishes every port its
// own EXPOSE declares (lifecycle.go's configureExposedPorts); the nats
// image declares 4222, 6222 and 8222, so an empty list puts the plain
// client port one dial away from the host. A HostConfigModifier cannot
// undo it either, because the modifier runs before the port merge rather
// than after. That is why the default here is a real port and why this
// option cannot clear the set to nothing.
func WithNATSExposedPorts(ports ...string) NATSOption {
	return func(s *natsSettings) {
		s.ports = nil
		for _, p := range ports {
			if !strings.Contains(p, "/") {
				p += "/tcp"
			}
			s.ports = append(s.ports, p)
		}
	}
}

// WithNATSNetwork joins the broker to nw under the given aliases.
//
// The aliases are load bearing wherever another container resolves this
// one by name: a Toxiproxy upstream written as "nats:4222", or an nginx
// upstream that resolves through Docker's embedded DNS when its
// configuration loads and refuses to start if the name is not live yet.
func WithNATSNetwork(nw *testcontainers.DockerNetwork, aliases ...string) NATSOption {
	return func(s *natsSettings) {
		s.network = nw
		s.aliases = aliases
	}
}

// WithNATSFile mounts one more file, for whatever the configuration names.
//
// A configuration naming a certificate is useless without the certificate,
// so this is the other half of WithNATSConfig rather than a separate idea.
// It takes bytes for the same reason the configuration does.
func WithNATSFile(containerPath string, content []byte, mode int64) NATSOption {
	return func(s *natsSettings) {
		s.files = append(s.files, testcontainers.ContainerFile{
			Reader:            strings.NewReader(string(content)),
			ContainerFilePath: containerPath,
			FileMode:          mode,
		})
	}
}

// natsRequest turns resolved settings into the container request.
//
// It is a pure function, separately from StartNATS, so the assembly can be
// asserted in a table test with no Docker anywhere near it. That matters
// for one field in particular.
//
// THE COMMAND IS BUILT HERE AND NOWHERE ELSE. The hazard this removes is
// specific and was measured in the driver: testcontainers' WithCmd option
// ASSIGNS the command, while the NATS module's WithConfigFile option
// APPENDS to it. Two independent customizers writing one field means their
// order decides whether the server ever reads its configuration, and the
// losing order is silent: the server boots with defaults, every assertion
// passes, and a test that exists to prove authentication is enforced
// proves nothing. There is no option here that writes Cmd, and StartNATS
// accepts no customizer, so there is no second writer and therefore no
// order.
//
// The flag list is the deployment's own, from NATSCommand, so a test
// broker and the broker an operator runs cannot differ in what they
// enable. "-c" is PREPENDED to that list rather than replacing it, which
// is the same additive shape the Helm chart's StatefulSet uses.
func natsRequest(s natsSettings) testcontainers.ContainerRequest {
	cmd := NATSCommand()
	files := s.files
	if s.config != "" {
		cmd = append([]string{"-c", NATSConfigPath}, cmd...)
		files = append([]testcontainers.ContainerFile{{
			Reader:            strings.NewReader(s.config),
			ContainerFilePath: NATSConfigPath,
			FileMode:          0o644,
		}}, files...)
	}

	req := testcontainers.ContainerRequest{
		Image:        s.image,
		ExposedPorts: s.ports,
		Cmd:          cmd,
		Files:        files,
		// ForLog, never ForListeningPort. A malformed configuration makes
		// nats-server print one line and exit, which this strategy
		// surfaces as a failure in about a second. Waiting for the port
		// instead turns the same mistake into a silent two minute hang
		// that reports a timeout and names nothing.
		WaitingFor: wait.ForLog("Server is ready").WithStartupTimeout(ContainerStartupTimeout),
	}
	if s.network != nil {
		req.Networks = []string{s.network.Name}
		if len(s.aliases) > 0 {
			req.NetworkAliases = map[string][]string{s.network.Name: s.aliases}
		}
	}
	return req
}
