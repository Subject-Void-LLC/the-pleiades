package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestFQDNToHostname(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"three_labels", "host1.example.com", "host1"},
		{"two_labels", "host1.local", "host1"},
		{"bare_hostname", "host1", "host1"},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.FQDNToHostname(tc.in); got != tc.want {
				t.Errorf("FQDNToHostname(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHostnameToFQDN(t *testing.T) {
	cases := []struct {
		name, hostname, domain, want string
	}{
		{"basic", "host1", "example.com", "host1.example.com"},
		{"leading_dot_domain", "host1", ".example.com", "host1.example.com"},
		{"empty_domain", "host1", "", "host1"},
		{"empty_hostname", "", "example.com", ""},
		{"over_cap_hostname", strings.Repeat("1", filters.MaxInputBytes+1), "example.com", ""},
		{"over_cap_domain", "host1", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.HostnameToFQDN(tc.hostname, tc.domain); got != tc.want {
				t.Errorf("HostnameToFQDN(%q, %q) = %q, want %q", tc.hostname, tc.domain, got, tc.want)
			}
		})
	}
}

func TestFQDNRoundTrip(t *testing.T) {
	fqdn := filters.HostnameToFQDN("host1", "example.com")
	if got := filters.FQDNToHostname(fqdn); got != "host1" {
		t.Errorf("FQDNToHostname(HostnameToFQDN(...)) = %q, want %q", got, "host1")
	}
}

func TestURLDomain(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"https_with_port", "https://example.com:8443/path", "example.com"},
		{"http_no_port", "http://example.com/path", "example.com"},
		{"no_path", "https://example.com", "example.com"},
		{"no_scheme_rejected", "example.com:8080/path", ""},
		{"malformed", "garbage", ""},
		{"malformed_with_scheme", "http://example.com/%zz", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.URLDomain(tc.in); got != tc.want {
				t.Errorf("URLDomain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestURLPort(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"explicit_port", "https://example.com:8443/path", 8443},
		{"https_default", "https://example.com/path", 443},
		{"http_default", "http://example.com/path", 80},
		{"unknown_scheme_no_port", "ftp://example.com/path", -1},
		{"no_scheme_rejected", "example.com:8080/path", -1},
		{"malformed", "garbage", -1},
		{"malformed_with_scheme", "http://example.com/%zz", -1},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.URLPort(tc.in); got != tc.want {
				t.Errorf("URLPort(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
