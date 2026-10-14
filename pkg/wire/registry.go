// A container registry credential on its way to the one task that may use
// it.
package wire

import "strings"

// RegistryCredential authenticates image pulls and pushes against one
// registry. It travels beside a dispatch's machine credential rather than in
// its secrets map, so a method that never touches a registry never sees it,
// and a method that does gets it only for the host it names
// (sdk.RegistryCredentials). External Collections never receive one.
type RegistryCredential struct {
	// Host is the registry the credential belongs to, such as "ghcr.io" or
	// "registry.example.com:5000"; "docker.io" for Docker Hub.
	Host string `json:"host"`
	// Username is the registry account.
	Username string `json:"username,omitempty"`
	// Password is its password or token.
	Password string `json:"password,omitempty"`
}

// RegistryHost normalizes a registry host for comparison: lowercase, and
// Docker Hub's several names folded into "docker.io", the name an image
// reference with no registry resolves to.
func RegistryHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	switch h {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}
	return h
}

// For returns the credential when it belongs to host, compared through
// RegistryHost, and false otherwise, including for a nil credential.
func (r *RegistryCredential) For(host string) (username, password string, ok bool) {
	if r == nil || r.Host == "" || RegistryHost(r.Host) != RegistryHost(host) {
		return "", "", false
	}
	return r.Username, r.Password, true
}
