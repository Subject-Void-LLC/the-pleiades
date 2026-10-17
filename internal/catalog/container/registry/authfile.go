// The engines' registry login file, read and changed as data: Docker's
// config.json and Podman's auth.json share its "auths" shape.
package registry

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// dockerHubKey is the key the docker CLI files Docker Hub's login under.
const dockerHubKey = "https://index.docker.io/v1/"

// authFile is a login file: every top-level key kept as it was, and the
// auths map's entries kept as they were but the one a method changes.
type authFile struct {
	top   map[string]json.RawMessage
	auths map[string]json.RawMessage
}

// authEntry is the part of one registry's entry this reads.
type authEntry struct {
	Auth string `json:"auth"`
}

// configKey is the key kind's engine files host's login under: Docker's
// CLI keeps Docker Hub's under its old index address, and everything else,
// and everything of Podman's, under the host.
func configKey(kind capability.EngineKind, host string) string {
	if kind == capability.EngineDocker && host == "docker.io" {
		return dockerHubKey
	}
	return host
}

// encodeAuth is an entry's auth value: base64 of user:password.
func encodeAuth(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// parseAuthFile reads data, an absent file being empty. Its content is
// never quoted in an error, since it holds passwords.
func parseAuthFile(data []byte) (*authFile, error) {
	f := &authFile{top: map[string]json.RawMessage{}, auths: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f.top); err != nil {
		return nil, fmt.Errorf("it is not a JSON object")
	}
	if raw, ok := f.top["auths"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &f.auths); err != nil {
			return nil, fmt.Errorf("its auths is not a JSON object")
		}
	}
	return f, nil
}

// helperFor names the credential helper the file sends key's login to,
// where the engine would look instead of the file: Docker's credsStore and
// credHelpers. "" when there is none.
func (f *authFile) helperFor(key string) string {
	var helpers map[string]string
	if raw, ok := f.top["credHelpers"]; ok {
		_ = json.Unmarshal(raw, &helpers)
	}
	if h := helpers[key]; h != "" {
		return h
	}
	var store string
	if raw, ok := f.top["credsStore"]; ok {
		_ = json.Unmarshal(raw, &store)
	}
	return store
}

// entry returns key's auth value and whether there is an entry at all.
func (f *authFile) entry(key string) (string, bool) {
	raw, ok := f.auths[key]
	if !ok {
		return "", false
	}
	var e authEntry
	_ = json.Unmarshal(raw, &e)
	return e.Auth, true
}

// username is the account an entry's auth value names, "" when it does
// not decode.
func username(auth string) string {
	data, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return ""
	}
	user, _, ok := strings.Cut(string(data), ":")
	if !ok {
		return ""
	}
	return user
}

// set makes key's entry hold auth alone.
func (f *authFile) set(key, auth string) {
	raw, _ := json.Marshal(authEntry{Auth: auth})
	f.auths[key] = raw
}

// remove drops key's entry, reporting whether there was one.
func (f *authFile) remove(key string) bool {
	_, ok := f.auths[key]
	delete(f.auths, key)
	return ok
}

// keys lists the registries the file holds a login for, sorted.
func (f *authFile) keys() []string {
	out := make([]string, 0, len(f.auths))
	for k := range f.auths {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bytes encodes the file, its keys sorted and indented as the engines'
// own tools write it.
func (f *authFile) bytes() ([]byte, error) {
	auths, err := json.Marshal(f.auths)
	if err != nil {
		return nil, err
	}
	f.top["auths"] = auths
	out, err := json.MarshalIndent(f.top, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
