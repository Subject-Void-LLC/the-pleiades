package topology_test

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func TestValidateNatsURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"plain nats", "nats://localhost:4222", ""},
		{"tls", "tls://broker.example.com:4222", ""},
		{"websocket", "ws://broker.example.com:443", ""},
		{"websocket over tls", "wss://broker.example.com:443", ""},
		{"a cluster of one kind", "nats://a:4222,nats://b:4222", ""},
		{"a tls cluster", "tls://a:4222,tls://b:4222", ""},

		{"empty", "", "no NATS URL was given"},
		{"whitespace only", "   ", "no NATS URL was given"},
		// The quiet failure this allowlist exists for: nats.go treats a
		// bare host as plaintext, so an omitted scheme silently downgrades.
		{"a bare host and port", "localhost:4222", "looks like a bare host and port"},
		{"no scheme at all", "//localhost:4222", "names no scheme"},
		{"a plausible typo", "tsl://broker:4222", "does not implement"},
		{"a copied web scheme", "https://broker:4222", "does not implement"},
		{"scheme but no host", "nats://", "no host"},
		{"trailing comma", "nats://a:4222,", "empty entry"},
		// The dangerous list: the plaintext member decides what an
		// attacker sees, and which member a client picks is not the
		// caller's choice.
		{"mixing tls and plaintext", "tls://a:4222,nats://b:4222", "mixes encrypted and plaintext"},
		{"mixing wss and ws", "wss://a:443,ws://b:443", "mixes encrypted and plaintext"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := topology.ValidateNatsURL(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateNatsURL(%q) = %v, want nil", tt.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateNatsURL(%q) = nil, want an error mentioning %q", tt.raw, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestNatsURLIsEncrypted pins which schemes this module considers
// encrypted, because that answer decides what a startup log tells an
// operator about their own deployment.
func TestNatsURLIsEncrypted(t *testing.T) {
	for scheme, want := range map[string]bool{
		"nats": false, "ws": false, "tls": true, "wss": true, "nonsense": false,
	} {
		if got := topology.NatsURLIsEncrypted(scheme); got != want {
			t.Errorf("NatsURLIsEncrypted(%q) = %v, want %v", scheme, got, want)
		}
	}
}

func TestNatsURLScheme(t *testing.T) {
	for raw, want := range map[string]string{
		"nats://a:4222":             "nats",
		"tls://a:4222,tls://b:4222": "tls",
		"  wss://a:443  ":           "wss",
		"localhost:4222":            "localhost",
		"":                          "",
	} {
		if got := topology.NatsURLScheme(raw); got != want {
			t.Errorf("NatsURLScheme(%q) = %q, want %q", raw, got, want)
		}
	}
}

// FuzzValidateNatsURL asserts the properties that make the allowlist a
// security control rather than a formatting check: it must never panic on
// operator input, and it must never accept a value whose first scheme is
// not one nats.go implements. The second is the one that matters, because
// accepting an unknown scheme is exactly the silent downgrade this exists
// to prevent.
func FuzzValidateNatsURL(f *testing.F) {
	for _, seed := range []string{
		"nats://localhost:4222", "tls://a:4222", "wss://a:443",
		"", "   ", "localhost:4222", "tsl://a:1", "nats://a,tls://b",
		"nats://", "://", "%zz", "nats://a:4222,,nats://b:4222",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		err := topology.ValidateNatsURL(raw)
		if err != nil {
			return
		}
		// Accepted, so every claim the validator makes must hold.
		scheme := topology.NatsURLScheme(raw)
		switch scheme {
		case "nats", "tls", "ws", "wss":
		default:
			t.Fatalf("ValidateNatsURL accepted %q whose first scheme is %q, which nats.go does not implement", raw, scheme)
		}
		// And an accepted value must not mix encryption, because the
		// plaintext member would decide what an attacker sees.
		var sawEncrypted, sawPlaintext bool
		for _, member := range strings.Split(raw, ",") {
			s := topology.NatsURLScheme(member)
			if topology.NatsURLIsEncrypted(s) {
				sawEncrypted = true
			} else {
				sawPlaintext = true
			}
		}
		if sawEncrypted && sawPlaintext {
			t.Fatalf("ValidateNatsURL accepted %q, which mixes encrypted and plaintext entries", raw)
		}
	})
}

// TestTLSFromEnv covers the arrangement checks, which exist because the
// failure they prevent is a process that starts, looks healthy, and
// encrypts nothing.
func TestTLSFromEnv(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ca.pem")
	cert, err := tlscert.Generate(dir, tlscert.Options{ExtraNames: []string{"localhost"}})
	if err != nil {
		t.Fatalf("generating a certificate: %v", err)
	}
	pem, err := os.ReadFile(filepath.Join(dir, tlscert.CertFileName))
	if err != nil {
		t.Fatalf("reading the generated certificate: %v", err)
	}
	if err := os.WriteFile(good, pem, 0o600); err != nil {
		t.Fatalf("writing the CA file: %v", err)
	}
	_ = cert

	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("this is not a certificate"), 0o600); err != nil {
		t.Fatalf("writing the junk file: %v", err)
	}

	t.Run("no CA and a plaintext URL configures nothing", func(t *testing.T) {
		cfg, err := topology.TLSFromEnv("nats://localhost:4222", "", nil)
		if err != nil || cfg != nil {
			t.Fatalf("TLSFromEnv = (%v, %v), want (nil, nil)", cfg, err)
		}
	})

	t.Run("no CA and an encrypted URL falls back to the system pool", func(t *testing.T) {
		cfg, err := topology.TLSFromEnv("tls://broker:4222", "", nil)
		if err != nil || cfg != nil {
			t.Fatalf("TLSFromEnv = (%v, %v), want (nil, nil): a publicly signed broker verifies against the system pool", cfg, err)
		}
	})

	t.Run("a CA with an encrypted URL builds a pinned pool", func(t *testing.T) {
		cfg, err := topology.TLSFromEnv("tls://broker:4222", good, nil)
		if err != nil {
			t.Fatalf("TLSFromEnv: %v", err)
		}
		if cfg == nil || cfg.RootCAs == nil {
			t.Fatal("TLSFromEnv returned no root pool for a named CA")
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Errorf("MinVersion = %x, want the module's TLS 1.2 floor", cfg.MinVersion)
		}
	})

	t.Run("a CA with a plaintext URL is refused", func(t *testing.T) {
		_, err := topology.TLSFromEnv("nats://broker:4222", good, nil)
		if err == nil {
			t.Fatal("TLSFromEnv accepted a CA file for a plaintext URL, which reads as protected and is not")
		}
		if !strings.Contains(err.Error(), "does not encrypt") {
			t.Errorf("error = %v, want it to name the contradiction", err)
		}
	})

	t.Run("a missing CA file is refused", func(t *testing.T) {
		if _, err := topology.TLSFromEnv("tls://broker:4222", filepath.Join(dir, "absent.pem"), nil); err == nil {
			t.Fatal("TLSFromEnv accepted a CA path that does not exist")
		}
	})

	t.Run("a CA file with no certificate in it is refused", func(t *testing.T) {
		_, err := topology.TLSFromEnv("tls://broker:4222", junk, nil)
		if err == nil {
			t.Fatal("TLSFromEnv accepted a file containing no PEM certificate")
		}
		if !strings.Contains(err.Error(), "no PEM certificate") {
			t.Errorf("error = %v, want it to say the file held no certificate", err)
		}
	})
}
