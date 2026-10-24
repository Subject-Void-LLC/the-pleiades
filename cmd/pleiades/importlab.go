// What the lab importers share: placing a node in the inventory, reading a
// server's password without it riding argv, and the plaintext HTTP rule.
package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// labPlace puts spec in hosts. A new name is added. One already there is
// refused unless replace, and then only when it is spec's type: the
// replacement keeps the record's identity, tags and every property, with
// spec's properties (the address) written over them, so what onboarding
// proved about the old address goes stale, as it should. spec is built
// before it is placed, so a node the type refuses is never written.
func labPlace(hosts []inventory.HostSpec, spec inventory.HostSpec, replace bool) ([]inventory.HostSpec, error) {
	index := slices.IndexFunc(hosts, func(h inventory.HostSpec) bool { return h.Name == spec.Name })
	switch {
	case index >= 0 && !replace:
		return nil, fmt.Errorf("host %q already exists; pass --replace to replace its address", spec.Name)
	case index >= 0 && hosts[index].Type != spec.Type:
		return nil, fmt.Errorf("host %q is a %s, not a %s, so an import cannot replace it", spec.Name, hosts[index].Type, spec.Type)
	case index >= 0:
		kept := map[string]pkginventory.PropertyValue{}
		for k, v := range hosts[index].Properties {
			kept[k] = v
		}
		for k, v := range spec.Properties {
			kept[k] = v
		}
		spec.ID, spec.Tags, spec.Properties = hosts[index].ID, hosts[index].Tags, kept
	}
	if _, err := inventory.NewItemFactory().Build(record.Record{ID: pkginventory.DeviceID(spec.ID), Name: spec.Name, Type: spec.Type, Properties: spec.Properties}); err != nil {
		return nil, fmt.Errorf("node %s not imported: %w", spec.Name, err)
	}
	if index >= 0 {
		hosts[index] = spec
		return hosts, nil
	}
	return append(hosts, spec), nil
}

// maxPassword bounds a password read from standard input.
const maxPassword = 4096

// labPassword is a server's password: the first line of standard input
// when fromStdin, else the environment variable env. It never comes from
// a flag, since a command line is visible to every process on the machine.
func labPassword(fromStdin bool, env string, stdin io.Reader) (string, error) {
	if !fromStdin {
		return os.Getenv(env), nil
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, maxPassword+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the password from standard input: %w", err)
	}
	if len(line) > maxPassword {
		return "", fmt.Errorf("the password on standard input is over %d bytes", maxPassword)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// labPlaintext refuses a password sent over plain HTTP to anything but
// this machine's loopback unless allow, and returns the warning a run then
// prints. A lab server on a private network is often plain HTTP; the
// person decides that, by name, every time.
func labPlaintext(u *url.URL, withPassword, allow bool) (string, error) {
	if u.Scheme != "http" || !withPassword {
		return "", nil
	}
	if isLoopbackHost(u.Hostname()) {
		return "", nil
	}
	if !allow {
		return "", fmt.Errorf("%s is plain HTTP, so the password would cross the network unencrypted; use https, or pass --allow-plaintext to send it anyway", u.Redacted())
	}
	return fmt.Sprintf("warning: the password for %s went over plain HTTP, unencrypted; change it if that network is not yours alone", u.Host), nil
}

// labHTTPClient bounds every step of a call to a lab server: the dial,
// the TLS handshake, waiting for an answer, and the whole call. tlsConfig
// is nil for the system's CAs.
func labHTTPClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig:       tlsConfig,
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: time.Minute,
		},
		// A redirect could carry the login elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
