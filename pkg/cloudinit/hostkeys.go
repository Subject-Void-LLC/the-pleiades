// Reading a machine's SSH host keys from what cloud-init printed on its
// console at first boot.
package cloudinit

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// The lines cloud-init's keys-to-console module prints around the host
// keys it generated.
const (
	beginKeys = "-----BEGIN SSH HOST KEY KEYS-----"
	endKeys   = "-----END SSH HOST KEY KEYS-----"
)

// ErrNoHostKeys is returned while the console holds no complete block of
// host keys, as it does until cloud-init has finished generating them.
var ErrNoHostKeys = errors.New("cloudinit: the console shows no complete block of SSH host keys yet")

// keyStart finds where a public key begins on a console line: its
// algorithm name, then a space and base64. What comes before it (a
// syslog tag, a timestamp) is not part of the key.
var keyStart = regexp.MustCompile(`(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(?:256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/=]+`)

// escape matches a terminal escape sequence a console log can carry.
var escape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// HostKeys returns the host keys in the last complete block cloud-init
// printed on console, each as "algorithm base64", the form a known_hosts
// line holds after its host names.
//
// The last block is the one taken because a console log can span more
// than one boot, and the newest keys are the machine's. A line inside the
// block that holds no key is skipped; a line that holds one that does not
// parse is refused, since a block half read is not a block to trust.
func HostKeys(console string) ([]string, error) {
	lines := strings.Split(escape.ReplaceAllString(console, ""), "\n")
	end := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], endKeys) {
			end = i
			break
		}
	}
	begin := -1
	for i := end - 1; i >= 0; i-- {
		if strings.Contains(lines[i], beginKeys) {
			begin = i
			break
		}
	}
	if end < 0 || begin < 0 {
		return nil, ErrNoHostKeys
	}
	var keys []string
	for _, line := range lines[begin+1 : end] {
		found := keyStart.FindString(line)
		if found == "" {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(found))
		if err != nil {
			return nil, errors.New("cloudinit: the console's host key block holds a key that does not parse")
		}
		keys = append(keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
	}
	if len(keys) == 0 {
		return nil, ErrNoHostKeys
	}
	return keys, nil
}
