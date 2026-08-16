//go:build devtools

// Command devcert writes a development serving certificate, using the same
// generator the controller, the development server and the end-to-end
// harness all use.
//
// It is no longer required by anything. The controller provisions its own
// self-signed certificate when an operator has configured none, so
// `docker compose up` needs no preparatory command and `make ui-dev`
// generates its own. What this is still for is the OTHER arrangement: a
// certificate an operator hands the controller through TLS_CERT_FILE and
// TLS_KEY_FILE, which is the path a real deployment uses with a real
// certificate and which therefore deserves a way to be exercised locally.
// Use it when you want to test that path, or when you want one certificate
// shared by several processes rather than one per data directory.
//
// It carries //go:build devtools for the same reason tools/uidev does: it is
// a developer convenience invoked by path, never imported, and nothing in the
// shipped binaries can reach it, so it is kept out of the default build and
// out of the security scan that judges shipped server code.
//
// The tag is `devtools` rather than `ignore`, and that one word is the whole
// difference between a file CI compiles and a file CI cannot see. `make ci`
// runs `go build -tags devtools ./tools/...` and `go vet -tags devtools
// ./tools/...`, so a break here fails a build rather than waiting for a human
// to run `make dev-cert`. Invocation is unchanged: `go run` on an explicitly
// named file ignores build constraints, which is what makes the Makefile
// target work either way.
//
// All of the actual certificate logic lives in internal/tlscert, where
// `go build`, `go vet` and a real test can all see it; what is here is
// argument handling and one deliberate permission change.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// devCertTTL is how long the pair this command writes stays valid.
//
// A week, matching internal/testsupport's own throwaway certificates
// rather than internal/tlscert's year-long default, because this writes to
// a scratch directory on a developer's machine and a stray copy of a
// private key that still works months later is the thing worth avoiding.
// Re-run the command when it expires.
const devCertTTL = 7 * 24 * time.Hour

func main() {
	dir := flag.String("dir", ".dev-certs",
		"directory to write cert.pem and key.pem into (gitignored)")
	hosts := flag.String("hosts", "",
		"comma-separated extra subject alternative names beyond localhost, 127.0.0.1 and ::1")
	containerReadable := flag.Bool("container-readable", false,
		"relax the file mode to 0644 so a container running as another UID can read the PRIVATE KEY through a bind mount; development only, and off unless asked for")
	flag.Parse()

	cert, err := tlscert.Generate(*dir, tlscert.Options{
		TTL:        devCertTTL,
		ExtraNames: splitHosts(*hosts),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "devcert:", err)
		os.Exit(1)
	}

	if *containerReadable {
		// 0644 on a private key, and this is the one uncomfortable branch in
		// the file. It USED to be the default, which is how `make dev-cert`
		// came to leave a world-readable private key on a developer's disk
		// with nobody having asked for one. A private key at 0644 is wrong
		// regardless of convenience, so it now happens only when the flag
		// says so, and it says so out loud when it does.
		//
		// The convenience it buys is real. A container image commonly runs as
		// a UID that is not the host user, a bind mount carries the host
		// file's ownership straight into the container with no remapping, and
		// a 0600 key owned by the host user is therefore unreadable by the
		// process that has to present it: the server exits at startup with a
		// permission error. Relaxing the mode is the only fix that does not
		// require root on the host.
		//
		// What makes it acceptable is precisely and only that these are
		// throwaway development certificates: generated fresh by this
		// command, valid for a week, written to a gitignored directory, and
		// never an operator's real key. A real deployment mounts its key
		// through its platform's own secret mechanism, which sets ownership
		// correctly and never needs this.
		for _, path := range []string{cert.CertFile, cert.KeyFile} {
			if err := os.Chmod(path, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "devcert:", err)
				os.Exit(1)
			}
		}
		// The directory too, and forgetting it is the version of this
		// mistake that is hardest to read: with the files world readable
		// inside a directory the container's UID cannot traverse, the error
		// is still "permission denied" on a file whose mode looks fine.
		if err := os.Chmod(*dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "devcert:", err)
			os.Exit(1)
		}
	}

	fmt.Printf("devcert: wrote %s and %s\n", cert.CertFile, cert.KeyFile)
	fmt.Println("devcert: development only, valid for", devCertTTL)
	fmt.Println("devcert: self-signed, so it encrypts but does not authenticate the server;")
	fmt.Println("devcert: a browser warns once and an HTTP client needs --cacert " + cert.CertFile)
	fmt.Println("devcert: point TLS_CERT_FILE and TLS_KEY_FILE at these two files to serve them")
	if *containerReadable {
		// Said every time, because a world-readable private key is a state
		// somebody has to be able to notice they asked for.
		fmt.Println("devcert: -container-readable was set, so " + cert.KeyFile + " is mode 0644 and every user on this machine can read the private key")
	} else {
		fmt.Println("devcert: the key is mode 0600; add -container-readable if a container running as another UID has to read it through a bind mount")
	}
}

// splitHosts turns the comma-separated -hosts flag into the list
// internal/tlscert expects, leaving blank and duplicate entries for that
// package to drop so the two cannot disagree about what an empty value
// means.
func splitHosts(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}
