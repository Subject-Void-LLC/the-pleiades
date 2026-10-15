// Tests for what a timed-out greeting wait reports about the container.
package testsupport

import (
	"context"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// procNetTCP is /proc/net/tcp text: sshd listening on 2222 (08AE), and a
// connection to 2222 that is not a listener.
const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:08AE 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:08AE 0100007F:9C40 01 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 20 4 30 10 -1
`

// TestListensOn reads the LISTEN state for the port and nothing else.
func TestListensOn(t *testing.T) {
	if !listensOn(procNetTCP, 2222) {
		t.Error("a listener on 2222 was not found")
	}
	established := strings.Replace(procNetTCP, " 0A ", " 06 ", 1)
	if listensOn(established, 2222) {
		t.Error("a connection that is not listening was read as a listener")
	}
	if listensOn(procNetTCP, 22) {
		t.Error("a listener on 2222 was read as one on 22")
	}
}

// evidenceTarget is a fakeTarget that Docker publishes on hostPort and
// whose /proc/net/tcp holds sockets.
type evidenceTarget struct {
	fakeTarget
	hostPort string
	sockets  string
	execErr  error
}

func (e evidenceTarget) Inspect(context.Context) (*container.InspectResponse, error) {
	port, err := network.ParsePort("2222/tcp")
	if err != nil {
		return nil, err
	}
	return &container.InspectResponse{NetworkSettings: &container.NetworkSettings{Ports: network.PortMap{
		port: {{HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: e.hostPort}, {HostIP: netip.MustParseAddr("::"), HostPort: e.hostPort}},
	}}}, nil
}

func (e evidenceTarget) Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error) {
	if e.execErr != nil {
		return 0, nil, e.execErr
	}
	return 0, strings.NewReader(e.sockets), nil
}

// TestForGreeting_ATimeoutSaysWhatItFoundInside: a mapped port that refuses
// every connection times out naming Docker's bindings, the port it dialed,
// and whether a server listens inside, for each of the two answers.
func TestForGreeting_ATimeoutSaysWhatItFoundInside(t *testing.T) {
	refusing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := refusing.Addr().String()
	_ = refusing.Close()
	_, hostPort, _ := net.SplitHostPort(addr)

	for name, tc := range map[string]struct {
		sockets string
		want    string
	}{
		"a server inside":             {procNetTCP, "something inside listens on 2222"},
		"no server inside":            {"  sl  local_address rem_address   st\n", "nothing inside listens on 2222"},
		"a container no exec reaches": {"", "could not read the container's sockets"},
	} {
		t.Run(name, func(t *testing.T) {
			target := evidenceTarget{fakeTarget: fakeTarget{addr: addr, running: true}, hostPort: hostPort, sockets: tc.sockets}
			if tc.sockets == "" {
				target.execErr = io.ErrUnexpectedEOF
			}
			g := &greeting{port: "2222/tcp", prefix: "SSH-", protocol: "SSH", timeout: 300 * time.Millisecond}
			err := g.WaitUntilReady(context.Background(), target)
			if err == nil {
				t.Fatal("a refusing port was ready")
			}
			for _, want := range []string{"sent no SSH greeting", "Docker publishes it at [0.0.0.0:" + hostPort + " [::]:" + hostPort + "]", "the mapped port is now " + hostPort, tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v\nwant it to say %q", err, want)
				}
			}
		})
	}
}
