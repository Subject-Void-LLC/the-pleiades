// Phase 101c's bootstrap gate: the real binary's own output configures a
// real broker, and the credential it issues is one that broker accepts.
//
// Everything here runs through `controller mesh`, the command an operator
// actually types, rather than through internal/meshid. That matters
// because every interesting failure in a bootstrap lives between the
// pieces rather than inside them: a configuration that names an account
// the resolver does not carry, a credential signed by a key the account
// JWT does not list, a system account with JetStream enabled. Each of
// those is a working library call and a broker that refuses to start.
package main_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nkeys"
)

// meshEnv is setupEnv plus the master key these commands seal the signing
// key under.
//
// setupEnv strips MASTER_ENCRYPTION_KEY on purpose, because the setup
// command's whole job is to generate one and a test that inherited the
// developer's would not be testing that. These commands are the other
// case: they consume a key that already exists, exactly as they do in a
// running deployment.
func meshEnv(dbPath string) []string {
	// Thirty two bytes, base64, and obviously not a real key.
	const testKey = "dGVzdC1tYXN0ZXIta2V5LW5vdC1hLXJlYWwtb25lISE="
	return append(setupEnv(dbPath), "MASTER_ENCRYPTION_KEY="+testKey)
}

// TestMeshBootstrapReleaseGate_TheGeneratedConfigAndCredentialWorkTogether
// is the end to end claim: init, then issue, then connect.
func TestMeshBootstrapReleaseGate_TheGeneratedConfigAndCredentialWorkTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dbPath := filepath.Join(t.TempDir(), "mesh.db")
	outDir := filepath.Join(t.TempDir(), "mesh")

	// ---- Act 1: the real command mints the hierarchy. ----
	init := exec.Command(binPath, "mesh", "init", "--dir", outDir)
	init.Env = meshEnv(dbPath)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("controller mesh init failed: %v\n%s", err, out)
	}

	conf, err := os.ReadFile(filepath.Join(outDir, "nats.conf"))
	if err != nil {
		t.Fatalf("mesh init wrote no broker configuration: %v", err)
	}

	// The operator key is written for the operator to move offline, and
	// it must never have been stored.
	//
	// ASSERTED STRUCTURALLY, NOT BY SEARCHING THE DATABASE FILE. The
	// obvious version reads the database bytes and looks for the seed,
	// and it is worse than useless: the seed column is sealed by the
	// envelope hook before it is written, so the plaintext never appears
	// there whether or not the operator key was stored. That assertion
	// was written first here, and a deliberate leak walked straight past
	// it. A test that cannot fail is indistinguishable from one that
	// passes.
	//
	// What does work is the key's KIND. nkeys prefixes a public key by
	// what it is, "O" for an operator and "A" for an account, so every
	// row storing an account signing key must name an "A" key and a
	// stored operator key is visible as an "O" no matter how the seed
	// beside it is encrypted.
	opSeed, err := os.ReadFile(filepath.Join(outDir, "operator.nk"))
	if err != nil {
		t.Fatalf("mesh init wrote no operator key: %v", err)
	}
	if info, err := os.Stat(filepath.Join(outDir, "operator.nk")); err != nil {
		t.Fatalf("stat operator.nk: %v", err)
	} else if info.Mode().Perm() != 0o400 {
		t.Errorf("operator.nk is mode %o, want 400", info.Mode().Perm())
	}
	opKP, err := nkeys.FromSeed(opSeed)
	if err != nil {
		t.Fatalf("the operator key file does not hold a usable seed: %v", err)
	}
	opPub, err := opKP.PublicKey()
	if err != nil {
		t.Fatalf("reading the operator public key: %v", err)
	}

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + dbPath, Schema: ent.SchemaNoUpgrade})
	if err != nil {
		t.Fatalf("opening the database the command wrote: %v", err)
	}
	defer func() { _ = client.Close() }()
	rows, err := client.MeshSigningKey.Query().All(ctx)
	if err != nil {
		t.Fatalf("reading the stored keys: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("mesh init stored no signing key at all, so the Controller could mint nothing")
	}
	for _, row := range rows {
		if row.PublicKey == opPub {
			t.Errorf("the OPERATOR key is stored as signing key %q; a Controller compromise would be a mesh compromise rather than an account compromise", row.KeyID)
		}
		if !nkeys.IsValidPublicAccountKey(row.PublicKey) {
			t.Errorf("signing key %q stores a %q-kind public key, want an account key; only an account signing key may be held online",
				row.KeyID, row.PublicKey[:1])
		}
	}

	// ---- Act 2: a real broker takes that configuration. ----
	//
	// If the generated file is wrong in any of the ways that matter, the
	// server exits at boot and StartNATS reports its log.
	url := testsupport.StartNATS(t, testsupport.WithNATSConfig(string(conf))).URL()

	// ---- Act 3: the control. The broker really is enforcing. ----
	if conn, err := topology.Connect(ctx, url, logger, "anonymous"); err == nil {
		conn.Close()
		t.Fatal("an anonymous client connected to a broker configured by mesh init, so nothing below proves anything about authentication")
	}

	// ---- Act 4: the real command issues a credential. ----
	credPath := filepath.Join(outDir, "runner.creds")
	issue := exec.Command(binPath, "mesh", "issue", "--out", credPath, "--label", "gate-runner")
	issue.Env = meshEnv(dbPath)
	if out, err := issue.CombinedOutput(); err != nil {
		t.Fatalf("controller mesh issue failed: %v\n%s", err, out)
	}
	if info, err := os.Stat(credPath); err != nil {
		t.Fatalf("stat the credential: %v", err)
	} else if info.Mode().Perm() != 0o400 {
		t.Errorf("the credential is mode %o, want 400", info.Mode().Perm())
	}

	// ---- Act 5: the deliverable. That credential works on that broker. ----
	//
	// Read through the production path, CredentialsFromEnv, rather than
	// with os.ReadFile, so this covers what the composition roots do
	// rather than something adjacent to it.
	creds, err := topology.CredentialsFromEnv(credPath)
	if err != nil {
		t.Fatalf("the issued credential was refused while reading it: %v", err)
	}
	conn, err := topology.Connect(ctx, url, logger, "gate-runner", topology.WithCredentials(creds))
	if err != nil {
		t.Fatalf("the credential mesh issue produced was refused by the broker mesh init configured: %v", err)
	}
	defer conn.Close()
	if !conn.IsConnected() {
		t.Fatal("the connection is not live")
	}

	// ---- Act 6: init refuses to overwrite key material. ----
	//
	// Overwriting an operator key cannot be undone: every account JWT it
	// signed stops verifying and the mesh has to be re-minted from
	// nothing. A second run must refuse rather than help.
	again := exec.Command(binPath, "mesh", "init", "--dir", outDir)
	again.Env = meshEnv(dbPath)
	out, err := again.CombinedOutput()
	if err == nil {
		t.Error("a second mesh init overwrote the existing key material")
	}
	if !strings.Contains(string(out), "already exists") {
		t.Errorf("the refusal does not say what is in the way: %s", out)
	}
}

// TestMeshShowReportsNothingBeforeInit covers the ordinary state of a
// deployment that has not turned mesh authentication on, which must read
// as a plain report rather than as a failure.
func TestMeshShowReportsNothingBeforeInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dbPath := filepath.Join(t.TempDir(), "empty.db")

	show := exec.Command(binPath, "mesh", "show")
	show.Env = meshEnv(dbPath)
	out, err := show.CombinedOutput()
	if err != nil {
		t.Fatalf("mesh show failed on a deployment with no key, which is the ordinary state: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "no mesh signing key") {
		t.Errorf("mesh show does not say plainly that there is no key: %s", out)
	}

	// And issuing without a key has to name the command that fixes it.
	issue := exec.Command(binPath, "mesh", "issue", "--out", filepath.Join(t.TempDir(), "x.creds"))
	issue.Env = meshEnv(dbPath)
	out, err = issue.CombinedOutput()
	if err == nil {
		t.Fatal("mesh issue produced a credential with no signing key")
	}
	if !strings.Contains(string(out), "mesh init") {
		t.Errorf("the refusal does not name the command that fixes it: %s", out)
	}
}
