package filters

import (
	"net/url"
	"strconv"
	"strings"
)

// urlDefaultPorts names the schemes this file knows a default port for.
// A scheme not listed here with no explicit port in the URL has no
// answer URLPort can give, and returns -1 rather than guessing.
var urlDefaultPorts = map[string]int{
	"http":  80,
	"https": 443,
}

// FQDNToHostname extracts the hostname (first label) from a fully
// qualified domain name (e.g. "host1.example.com" -> "host1"). An input
// with no dot is already a bare hostname and is returned unchanged,
// rather than treated as malformed: a caller checking "is this FQDN or
// bare" first would find this function useless if it refused the bare
// case.
func FQDNToHostname(fqdn string) string {
	if len(fqdn) > MaxInputBytes {
		return ""
	}
	if i := strings.IndexByte(fqdn, '.'); i >= 0 {
		return fqdn[:i]
	}
	return fqdn
}

// HostnameToFQDN joins a bare hostname with a domain suffix into a
// fully qualified domain name (e.g. "host1", "example.com" ->
// "host1.example.com"). Returns "" for an empty hostname. An empty
// domain returns hostname unchanged, matching FQDNToHostname's own
// "already in the target shape, pass it through" convention. A leading
// dot on domain is trimmed first, so both "example.com" and
// ".example.com" produce the identical result.
func HostnameToFQDN(hostname, domain string) string {
	if len(hostname) > MaxInputBytes || len(domain) > MaxInputBytes {
		return ""
	}
	if hostname == "" {
		return ""
	}
	domain = strings.TrimPrefix(domain, ".")
	if domain == "" {
		return hostname
	}
	return hostname + "." + domain
}

// URLDomain extracts the host, without port, from a URL (e.g.
// "https://example.com:8443/path" -> "example.com"). Returns "" for an
// over-length input or one with no "://" scheme separator: net/url's
// own Parse treats a schemeless "host:port/path" string as a
// scheme-and-opaque-data pair rather than a host, which would otherwise
// silently misparse "example.com:8080/path" into scheme "example.com"
// and no host at all. A real URL, the input this filter is documented
// to take, always carries a scheme.
func URLDomain(rawURL string) string {
	u, ok := parseURLWithScheme(rawURL)
	if !ok {
		return ""
	}
	return u.Hostname()
}

// URLPort extracts the port from a URL, or its scheme's default port
// (80 for http, 443 for https) when none is written explicitly (e.g.
// "https://example.com/path" -> 443). Returns -1 for an over-length
// input, one with no "://" scheme separator (see URLDomain's own doc
// comment), or a scheme with no explicit port and no known default.
func URLPort(rawURL string) int {
	u, ok := parseURLWithScheme(rawURL)
	if !ok {
		return -1
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return -1
		}
		return n
	}
	if def, ok := urlDefaultPorts[strings.ToLower(u.Scheme)]; ok {
		return def
	}
	return -1
}

// parseURLWithScheme parses s as a URL, refusing an over-length input
// or one with no "://" scheme separator up front (see URLDomain's own
// doc comment for why a schemeless input is refused rather than
// guessed at).
func parseURLWithScheme(s string) (*url.URL, bool) {
	if len(s) > MaxInputBytes {
		return nil, false
	}
	if !strings.Contains(s, "://") {
		return nil, false
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	return u, true
}
