// Starting the shared OpenSSH test server with settings of a test's own, and
// authorizing a test's own key on it.
package testsupport

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// StartSSHDConfigured is StartSSHD with config added as one more
// sshd_config.d file, for a test that needs the server set some other way: a
// server that takes a user key only with ssh-rsa (PubkeyAcceptedAlgorithms
// ssh-rsa), as an old device does. sshd takes the first occurrence of a
// keyword and the image includes that directory before its own defaults, so
// config wins over them. The caller must call Terminate.
func StartSSHDConfigured(ctx context.Context, config string) (*SSHD, error) {
	return startSSHD(ctx, config, nil)
}

// AuthorizeKey adds pub to SSHDUser's authorized_keys inside the container,
// so a test can log in with a key of whatever type it made.
func (s *SSHD) AuthorizeKey(ctx context.Context, pub ssh.PublicKey) error {
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	script := fmt.Sprintf("mkdir -p %[1]s/.ssh && printf '%%s\\n' '%[2]s' >> %[1]s/.ssh/authorized_keys && "+
		"chown -R 1000:1000 %[1]s/.ssh && chmod 700 %[1]s/.ssh && chmod 600 %[1]s/.ssh/authorized_keys",
		SSHDHome, authorized)
	_, err := s.RootExec(ctx, script)
	return err
}
