// A real net-snmp agent, for the SNMP client's container tests and the
// Crawl tier's SNMP release gate: v1 and v2c communities, an SNMPv3 user at
// every security level and for every algorithm the agent implements, and
// one object that accepts a SET and keeps nothing.
package testsupport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// SNMPDBaseImage is the Alpine release the test agent installs net-snmp on,
// pinned by digest; the agent image itself is built (testdata/snmpd).
// Alpine 3.22's net-snmp is 5.9.4, built with both AES-192/256 key
// extensions, measured 2026-11-15.
const SNMPDBaseImage = "alpine:3.22.8@sha256:e1011cfb1c3f0d0a3a06d5371ef866b5713e035a866d84a419e0e3d73ca0dfd8"

// snmpdTag is the local tag the test agent is built under.
const snmpdTag = "pleiades/snmpd:3.22.8"

// SNMPDDropOID is the agent's object that answers every SET as done and keeps
// none of them (testdata/snmpd/dropset.sh).
const SNMPDDropOID = "1.3.6.1.4.1.8072.9999.1.0"

// SNMPDUser is one SNMPv3 user the test agent knows, with its credential.
type SNMPDUser struct {
	Cred wire.SNMPCredential
	// Write says the user may SET (rwuser); every other user reads only.
	Write bool
}

// SNMPD is a running test agent.
type SNMPD struct {
	Host string
	Port int
	// ReadCommunity and WriteCommunity serve SNMPv1 and SNMPv2c alike.
	ReadCommunity, WriteCommunity string
	// Users are the SNMPv3 users by name.
	Users map[string]SNMPDUser
}

// netSNMPAuth and netSNMPPriv name each protocol as net-snmp's createUser
// does. 3DES has no row: net-snmp 5.9.4 refuses it ("unknown privProtocol").
var (
	netSNMPAuth = map[wire.SNMPAuthProtocol]string{
		wire.SNMPAuthMD5: "MD5", wire.SNMPAuthSHA: "SHA", wire.SNMPAuthSHA224: "SHA-224",
		wire.SNMPAuthSHA256: "SHA-256", wire.SNMPAuthSHA384: "SHA-384", wire.SNMPAuthSHA512: "SHA-512",
	}
	netSNMPPriv = map[wire.SNMPPrivProtocol]string{
		wire.SNMPPrivDES: "DES", wire.SNMPPrivAES128: "AES", wire.SNMPPrivAES192: "AES-192", wire.SNMPPrivAES256: "AES-256",
		wire.SNMPPrivAES192Reeder: "AES-192-C", wire.SNMPPrivAES256Reeder: "AES-256-C",
	}
)

// SNMPDUserName names the test agent's user for auth and priv: noauth,
// auth-<auth>, or priv-<auth>-<priv>.
func SNMPDUserName(auth wire.SNMPAuthProtocol, priv wire.SNMPPrivProtocol) string {
	switch {
	case auth == "":
		return "noauth"
	case priv == "":
		return "auth-" + string(auth)
	}
	return "priv-" + string(auth) + "-" + string(priv)
}

// snmpdUsers are the agent's users: one noAuthNoPriv; one authNoPriv per
// authentication protocol; one authPriv per privacy protocol under SHA-1,
// whose 20-byte key is too short for AES-192 and AES-256 and so is extended,
// which is what tells Blumenthal's scheme from Reeder's; MD5 with DES, the
// legacy pair; and SHA-512 with AES-256, the one that may write.
func snmpdUsers() map[string]SNMPDUser {
	users := map[string]SNMPDUser{}
	add := func(auth wire.SNMPAuthProtocol, priv wire.SNMPPrivProtocol, write bool) {
		name := SNMPDUserName(auth, priv)
		c := wire.SNMPCredential{User: name}
		if auth != "" {
			c.AuthProtocol, c.AuthPassphrase = auth, throwaway()
		}
		if priv != "" {
			c.PrivProtocol, c.PrivPassphrase = priv, throwaway()
		}
		users[name] = SNMPDUser{Cred: c, Write: write}
	}
	add("", "", false)
	for auth := range netSNMPAuth {
		add(auth, "", false)
	}
	for priv := range netSNMPPriv {
		add(wire.SNMPAuthSHA, priv, false)
	}
	add(wire.SNMPAuthMD5, wire.SNMPPrivDES, false)
	add(wire.SNMPAuthSHA512, wire.SNMPPrivAES256, true)
	return users
}

// throwaway is a fresh passphrase or community for one throwaway agent.
func throwaway() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "t-" + hex.EncodeToString(b)
}

// snmpdConfig is the agent's snmpd.conf for its communities and users.
func snmpdConfig(a *SNMPD) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agentAddress udp:1161\nrocommunity %s\nrwcommunity %s\n", a.ReadCommunity, a.WriteCommunity)
	for name, u := range a.Users {
		line := "createUser " + name
		level := "noauth"
		if u.Cred.AuthProtocol != "" {
			line += " " + netSNMPAuth[u.Cred.AuthProtocol] + " " + u.Cred.AuthPassphrase
			level = "auth"
		}
		if u.Cred.PrivProtocol != "" {
			line += " " + netSNMPPriv[u.Cred.PrivProtocol] + " " + u.Cred.PrivPassphrase
			level = "priv"
		}
		access := "rouser"
		if u.Write {
			access = "rwuser"
		}
		fmt.Fprintf(&b, "%s\n%s %s %s\n", line, access, name, level)
	}
	b.WriteString("pass .1.3.6.1.4.1.8072.9999 /bin/sh /usr/local/bin/dropset.sh\n")
	return b.String()
}

// StartSNMPD builds and starts the test agent, unprivileged, with throwaway
// communities and passphrases, and returns it once it serves. It is removed
// when tb ends.
func StartSNMPD(tb testing.TB) *SNMPD {
	tb.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		tb.Skip("docker not found on PATH")
	}
	src := filepath.Join(RepoRoot(tb), "internal", "testsupport", "testdata", "snmpd")
	build := exec.Command("docker", "build", "--build-arg", "BASE="+SNMPDBaseImage, "-t", snmpdTag, src) // #nosec G204 -- fixed arguments and a path in this repository
	if out, err := build.CombinedOutput(); err != nil {
		tb.Fatalf("building %s: %v\n%s", snmpdTag, err, out)
	}
	agent := &SNMPD{ReadCommunity: "ro-" + throwaway(), WriteCommunity: "rw-" + throwaway(), Users: snmpdUsers()}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        snmpdTag,
			ExposedPorts: []string{"1161/udp"},
			Env:          map[string]string{"SNMPD_CONF": snmpdConfig(agent)},
			WaitingFor:   wait.ForLog("NET-SNMP version").WithStartupTimeout(ContainerStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting snmpd: %v", err)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "1161/udp")
	if err != nil {
		tb.Fatal(err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	agent.Host, agent.Port = host, int(port.Num())
	return agent
}
