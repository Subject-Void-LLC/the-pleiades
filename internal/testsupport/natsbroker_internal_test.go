// Tests for the request a test broker is started from.
//
// These run with no Docker anywhere near them, which is the whole reason
// natsRequest is a pure function separate from StartNATS. The field they
// exist for is Cmd: the defect they guard against is a server that boots
// without reading its configuration, which no assertion inside a test
// that used such a server could ever catch, because that server works
// fine. It just is not the one the test meant to talk to.
package testsupport

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// settingsFor resolves options the way StartNATS does, so these tests
// exercise the same defaulting the real starter applies.
func settingsFor(opts ...NATSOption) natsSettings {
	s := natsSettings{image: NATSImage, ports: []string{defaultNATSPort}}
	for _, opt := range opts {
		opt(&s)
	}
	return s
}

// readFile returns a mounted file's content.
func readFile(t *testing.T, f testcontainers.ContainerFile) string {
	t.Helper()
	b, err := io.ReadAll(f.Reader)
	if err != nil {
		t.Fatalf("reading mounted file %s: %v", f.ContainerFilePath, err)
	}
	return string(b)
}

func TestNatsRequestBuildsTheCommand(t *testing.T) {
	deployment := NATSCommand()

	tests := []struct {
		name    string
		opts    []NATSOption
		wantCmd []string
	}{
		{
			name:    "no options runs exactly the deployment's flags",
			wantCmd: deployment,
		},
		{
			name:    "a configuration prepends the flag that reads it",
			opts:    []NATSOption{WithNATSConfig("operator: X")},
			wantCmd: append([]string{"-c", NATSConfigPath}, deployment...),
		},
		{
			name:    "an image override does not touch the command",
			opts:    []NATSOption{WithNATSImage(NATSImageBeforeLimitMarkerTTL)},
			wantCmd: deployment,
		},
		{
			name:    "ports and networks do not touch the command",
			opts:    []NATSOption{WithNATSExposedPorts("8080"), WithNATSNetwork(nil)},
			wantCmd: deployment,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := natsRequest(settingsFor(tt.opts...)).Cmd
			if !reflect.DeepEqual(got, tt.wantCmd) {
				t.Errorf("Cmd = %q, want %q", got, tt.wantCmd)
			}
		})
	}
}

// TestNatsRequestNeverSeparatesTheConfigFlagFromTheConfigFile is the
// ordering hazard stated as a property.
//
// The flag and the file are one branch, so a request can never hold the
// flag without the file (a server that exits at boot) or the file without
// the flag (a server that silently ignores it, which is the dangerous
// half: it starts, it works, and it is not configured).
func TestNatsRequestNeverSeparatesTheConfigFlagFromTheConfigFile(t *testing.T) {
	for _, conf := range []string{"", "operator: X", "authorization { user: a }"} {
		req := natsRequest(settingsFor(WithNATSConfig(conf)))

		hasFlag := false
		for i, arg := range req.Cmd {
			if arg == "-c" {
				hasFlag = true
				if i+1 >= len(req.Cmd) || req.Cmd[i+1] != NATSConfigPath {
					t.Fatalf("-c is not followed by %s: %q", NATSConfigPath, req.Cmd)
				}
			}
		}

		hasFile := false
		for _, f := range req.Files {
			if f.ContainerFilePath == NATSConfigPath {
				hasFile = true
			}
		}

		if hasFlag != hasFile {
			t.Errorf("conf %q: flag present = %v but file present = %v; the two must agree", conf, hasFlag, hasFile)
		}
		if (conf != "") != hasFlag {
			t.Errorf("conf %q: wanted the flag present = %v, got %v", conf, conf != "", hasFlag)
		}
	}
}

// TestNatsRequestAlwaysPublishesSomething pins the property behind
// FAILURE_PATTERNS 299: an empty ExposedPorts publishes MORE, not fewer.
func TestNatsRequestAlwaysPublishesSomething(t *testing.T) {
	tests := []struct {
		name string
		opts []NATSOption
		want []string
	}{
		{"the default is the client port", nil, []string{"4222/tcp"}},
		{"a bare port gains its protocol", []NATSOption{WithNATSExposedPorts("8080")}, []string{"8080/tcp"}},
		{"a qualified port is left alone", []NATSOption{WithNATSExposedPorts("8080/tcp")}, []string{"8080/tcp"}},
		{"several ports are all published", []NATSOption{WithNATSExposedPorts("4222", "8222")}, []string{"4222/tcp", "8222/tcp"}},
		{"naming ports REPLACES the default", []NATSOption{WithNATSExposedPorts("8080")}, []string{"8080/tcp"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := natsRequest(settingsFor(tt.opts...)).ExposedPorts
			if len(got) == 0 {
				t.Fatal("ExposedPorts is empty, which publishes every port the image declares rather than none")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExposedPorts = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNatsRequestMountsEveryFile(t *testing.T) {
	req := natsRequest(settingsFor(
		WithNATSConfig("tls { cert_file: \"/etc/nats/tls/cert.pem\" }"),
		WithNATSFile("/etc/nats/tls/cert.pem", []byte("CERT"), 0o400),
	))

	if len(req.Files) != 2 {
		t.Fatalf("mounted %d files, want the configuration and the certificate", len(req.Files))
	}
	if req.Files[0].ContainerFilePath != NATSConfigPath {
		t.Errorf("first file is %s, want the configuration at %s", req.Files[0].ContainerFilePath, NATSConfigPath)
	}
	if got := readFile(t, req.Files[1]); got != "CERT" {
		t.Errorf("certificate content = %q, want %q", got, "CERT")
	}
	if req.Files[1].FileMode != 0o400 {
		t.Errorf("certificate mode = %o, want 400", req.Files[1].FileMode)
	}
}

func TestNatsRequestCarriesTheNetworkAlias(t *testing.T) {
	nw := &testcontainers.DockerNetwork{Name: "chaos-net"}
	req := natsRequest(settingsFor(WithNATSNetwork(nw, "nats")))

	if !reflect.DeepEqual(req.Networks, []string{"chaos-net"}) {
		t.Errorf("Networks = %q, want [chaos-net]", req.Networks)
	}
	if got := req.NetworkAliases["chaos-net"]; !reflect.DeepEqual(got, []string{"nats"}) {
		t.Errorf("aliases = %q, want [nats]; a Toxiproxy or nginx upstream written as nats:4222 depends on it", got)
	}
}

func TestNatsRequestWaitsForTheReadyLine(t *testing.T) {
	req := natsRequest(settingsFor())
	if req.WaitingFor == nil {
		t.Fatal("no wait strategy: a malformed configuration would hang rather than fail")
	}
	if got := strings.ToLower(readyStrategyString(req)); !strings.Contains(got, "server is ready") {
		t.Errorf("wait strategy %q does not look for the ready line", got)
	}
}

// readyStrategyString renders the wait strategy for the assertion above.
func readyStrategyString(req testcontainers.ContainerRequest) string {
	type described interface{ String() string }
	if d, ok := req.WaitingFor.(described); ok {
		return d.String()
	}
	return reflect.TypeOf(req.WaitingFor).String()
}
