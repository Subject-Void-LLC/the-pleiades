package resolve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/lookup/hashivault"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
)

// Phase 78b's Release Gate: a credential whose input resolves out of a REAL
// HashiCorp Vault, through the real factory, the real resolver and a real
// database.
//
// # Why this exists when hashivault's own suite is already green
//
// That suite talks to a server this repository wrote, which can only ever
// confirm that this client agrees with this repository's idea of Vault. The
// two things it cannot establish are the two most likely to be wrong: that
// the URL path a v2 key/value read actually uses is the one built here, and
// that the JSON a real Vault returns nests where this code looks. Both are
// facts about Vault rather than about this code, and RULE 0 is explicit
// that a test which mocks the thing under test proves nothing about it.
//
// # What this gate deliberately does NOT re-prove
//
// Phase 22's Release Gate already asserts that an injected secret never
// reaches argv (checked against /proc/1/cmdline inside the container) and
// never reaches a log line. Those assertions are not repeated here, and the
// reason is structural rather than convenience: by the time injection
// happens, a Vault-sourced value is an ordinary entry in
// credtype.Credential.Inputs, byte for byte indistinguishable from a stored
// one, because resolution finished before the injector was called. There is
// no second code path for it to escape down. Re-running that stack here
// would test Phase 22's property again with a different value in it.
//
// What IS specific to this phase, and is asserted below: that the value is
// never at rest in the credential row, and that rotating the SOURCE takes
// effect on the next run with no edit to the target.

const (
	vaultImage     = "hashicorp/vault:1.18"
	vaultRootToken = "root-token-for-this-test-only"
)

// startVault brings up a real Vault in development mode and returns its
// base URL.
//
// Development mode is the right choice here and not a shortcut: it is a
// real Vault binary serving the real API over real HTTP, with an
// in-memory backend and a known root token. What this gate is checking is
// the wire contract, and the storage backend has nothing to do with it.
func startVault(t *testing.T) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        vaultImage,
			ExposedPorts: []string{"8200/tcp"},
			Env: map[string]string{
				"VAULT_DEV_ROOT_TOKEN_ID":  vaultRootToken,
				"VAULT_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
			},
			WaitingFor: wait.ForLog("Vault server started!").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		// Skipped locally, failed in CI. A container gate that quietly
		// skips everywhere is a gate that gates nothing, which is the
		// failure this repository's own CI split already records.
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the Vault release gate container (is Docker running?): %v", err)
		}
		t.Fatalf("starting the Vault release gate container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8200/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// writeSecret stores one key/value secret through Vault's own HTTP API.
func writeSecret(t *testing.T, base, path string, fields map[string]string) {
	t.Helper()

	body, err := json.Marshal(map[string]any{"data": fields})
	if err != nil {
		t.Fatalf("encoding the secret: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/secret/data/"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the write request: %v", err)
	}
	req.Header.Set("X-Vault-Token", vaultRootToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("writing the secret: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("writing the secret answered %d", resp.StatusCode)
	}
}

// TestReleaseGate_AnInputResolvesOutOfARealVault is the gate.
func TestReleaseGate_AnInputResolvesOutOfARealVault(t *testing.T) {
	base := startVault(t)
	writeSecret(t, base, "prod/api", map[string]string{"token": "the-real-vault-secret"})

	ctx := context.Background()
	_, store, client, orgID, targetTypeID := fixture(t)

	// The SHIPPED hashivault_kv type, installed the way the controller
	// installs it at startup, rather than a type this test invented. That
	// is what makes the namespace the factory selects on the real one.
	var shipped credtype.CredentialType
	for _, ct := range managed.Types() {
		if ct.Namespace == hashivault.Namespace {
			shipped = ct
		}
	}
	if shipped.Namespace == "" {
		t.Fatal("the hashivault_kv type is not in the shipped catalog")
	}
	sourceType, err := store.EnsureManagedType(ctx, shipped)
	if err != nil {
		t.Fatalf("EnsureManagedType() error = %v", err)
	}

	source, err := store.CreateCredential(ctx, orgID, sourceType.ID, "prod vault", "",
		map[string]string{"url": base, "token": vaultRootToken}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata: map[string]string{
				"secret_backend": "secret",
				"secret_path":    "prod/api",
				"secret_key":     "token",
			},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{hashivault.Factory{}})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}
	resolver := newResolver(client, lookups)

	got, err := resolver.Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got[0].Inputs["api_token"] != "the-real-vault-secret" {
		t.Fatalf("the bound input = %q, want the value the real Vault holds", got[0].Inputs["api_token"])
	}

	// The value is not at rest in the credential row. Read through the
	// store's own projection, which is what every read path holds and
	// which has no field a plaintext value could occupy.
	stored, err := store.GetCredential(ctx, target.ID)
	if err != nil {
		t.Fatalf("GetCredential() error = %v", err)
	}
	for id, v := range stored.Inputs {
		if v == "the-real-vault-secret" {
			t.Errorf("the credential row holds the resolved value under %q", id)
		}
	}

	// Rotating the SOURCE takes effect on the next run, with no edit to
	// the target. This is the property the whole row model exists for: a
	// rotated Vault token or a rotated secret reaches every credential
	// reading through it at once.
	writeSecret(t, base, "prod/api", map[string]string{"token": "the-rotated-secret"})

	after, err := resolver.Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() after rotation error = %v", err)
	}
	if after[0].Inputs["api_token"] != "the-rotated-secret" {
		t.Errorf("after rotation the input = %q, want the rotated value with no edit to the target",
			after[0].Inputs["api_token"])
	}
}
