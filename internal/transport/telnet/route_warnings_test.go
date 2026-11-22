// Tests that a bastion's legacy SSH warnings reach the task though Telnet,
// not SSH, rides the route.
package telnet_test

import (
	"context"
	"strings"
	"testing"
	"time"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// TestExec_ABastionsSkippedKeyIsWarnedOnTheTask proves a bastion whose key
// and password credential logged in by password, because it takes the RSA
// key only with ssh-rsa, is warned about in the task's collector, naming the
// bastion and the device behind it.
func TestExec_ABastionsSkippedKeyIsWarnedOnTheTask(t *testing.T) {
	keyPEM, pub, err := remoteexectest.RSAUserKey()
	if err != nil {
		t.Fatal(err)
	}
	bastion, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: pub, PublicKeyAuthAlgorithms: []string{cryptossh.KeyAlgoRSA}})
	if err != nil {
		t.Fatalf("starting the bastion: %v", err)
	}
	defer bastion.Close()

	tr := telnettransport.New(
		telnetexec.Options{ReadTimeout: 300 * time.Millisecond},
		remoteexec.Options{KnownHostsPath: writeKnownHostsFor(t, bastion), MaxRetries: 1},
	)
	target := transport.Target{
		Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: startDeviceServer(t, "hello\r\n")},
		Route: []transport.Hop{{
			Host:       bastion.Host,
			Port:       bastion.Port,
			DeviceName: "bastion",
			Credential: credential.Credential{Username: bastion.Username, Password: bastion.Password, PrivateKeyPEM: keyPEM},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = sdk.WithConnectionNotes(ctx, "console-sw")
	if _, err := tr.Exec(ctx, target, credential.Credential{}, "show version"); err != nil {
		t.Fatalf("Exec through the bastion: %v", err)
	}
	want := `device "bastion ` + bastion.Addr() + ` carrying console-sw" did not use the stored RSA key`
	if w := strings.Join(sdk.ConnectionWarnings(ctx), "\n"); !strings.Contains(w, want) {
		t.Errorf("the task's warnings %q do not say %q", w, want)
	}
	if bastion.KeyLogins() != 0 || bastion.Logins() != 1 {
		t.Errorf("the bastion saw %d logins, %d by key; want one by password", bastion.Logins(), bastion.KeyLogins())
	}
}
