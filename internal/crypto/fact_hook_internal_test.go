// Tests of the fact payload's seal (fact_hook.go): what the hook lets reach
// the table, what opens and what does not, and a rotation over more rows
// than one page.
package crypto

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// factService is an envelope service over one repeated key byte.
func factService(t *testing.T, key byte, version string, previous byte, previousVersion string) *EnvelopeService {
	t.Helper()
	var prev []byte
	if previousVersion != "" {
		prev = []byte(strings.Repeat(string(previous), 32))
	}
	svc, err := NewEnvelopeService([]byte(strings.Repeat(string(key), 32)), version, prev, previousVersion)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// factClient opens a real database file, with the fact hook under svc when
// svc is not nil, and returns the client and one device's id.
func factClient(t *testing.T, path string, svc *EnvelopeService) (*ent.Client, int) {
	t.Helper()
	ctx := context.Background()
	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if svc != nil {
		client.Fact.Use(FactPayloadHook(svc))
	}
	dev, err := client.Device.Query().First(ctx)
	if ent.IsNotFound(err) {
		dev, err = client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).Save(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	return client, dev.ID
}

// newFact starts a fact row for token on device holding payload.
func newFact(client *ent.Client, device int, token string, payload map[string]any) *ent.FactCreate {
	return client.Fact.Create().SetKeyToken(token).SetKeyedBy("test:0").SetHash("h").SetPayload(payload).SetDeviceID(device)
}

// TestFactPayloadHook_SealsWhatReachesTheTableUnsealed: a payload created
// unsealed is stored sealed and bound to its row's token, one created sealed
// is stored as it came, and one with no token to bind to is refused.
func TestFactPayloadHook_SealsWhatReachesTheTableUnsealed(t *testing.T) {
	ctx := context.Background()
	svc := factService(t, 'k', "v1", 0, "")
	client, dev := factClient(t, filepath.Join(t.TempDir(), "facts.db"), svc)
	plain := map[string]any{"key": "package.openssh", "json": `"9.6"`}
	row, err := newFact(client, dev, "token-a", plain).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := client.Fact.GetX(ctx, row.ID)
	envelope, sealed := sealedFactEnvelope(stored.Payload)
	if !sealed || strings.Contains(envelope, "openssh") {
		t.Fatalf("stored payload = %v, want one sealed envelope", stored.Payload)
	}
	opened, err := OpenFactPayload(svc, "token-a", stored.Payload)
	if err != nil || opened["key"] != "package.openssh" || opened["json"] != `"9.6"` {
		t.Fatalf("OpenFactPayload = %v, %v", opened, err)
	}
	// Already sealed, as the store hands it over: stored as it came.
	pre, err := SealFactPayload(svc, "token-b", plain)
	if err != nil {
		t.Fatal(err)
	}
	again := newFact(client, dev, "token-b", pre).SetChainPrev("x").SaveX(ctx)
	if got := client.Fact.GetX(ctx, again.ID).Payload[EncryptedKeyMarker]; got != pre[EncryptedKeyMarker] {
		t.Error("a payload that arrived sealed was sealed again")
	}
	// Nothing to bind to.
	if _, err := newFact(client, dev, "", plain).SetChainPrev("y").Save(ctx); err == nil {
		t.Error("a fact with no token was stored")
	}
	if _, err := SealFactPayload(svc, "", plain); err == nil {
		t.Error("a payload was sealed for no token")
	}
	if _, err := SealFactPayload(nil, "token-a", plain); err == nil {
		t.Error("a payload was sealed with no service")
	}
	if _, err := SealFactPayload(svc, "token-a", map[string]any{"json": make(chan int)}); err == nil {
		t.Error("a payload that does not encode was sealed")
	}
}

// TestFactPayloadHook_RefusesAnUpdateThatIsNotSealed: a stored payload can be
// replaced only by a sealed one, by one row or by many; an update that sets
// no payload passes.
func TestFactPayloadHook_RefusesAnUpdateThatIsNotSealed(t *testing.T) {
	ctx := context.Background()
	svc := factService(t, 'k', "v1", 0, "")
	client, dev := factClient(t, filepath.Join(t.TempDir(), "facts.db"), svc)
	row := newFact(client, dev, "token-a", map[string]any{"key": "k", "json": `"v"`}).SaveX(ctx)
	forged := map[string]any{"key": "k", "json": `"forged"`}
	if err := client.Fact.UpdateOneID(row.ID).SetPayload(forged).Exec(ctx); !errors.Is(err, ErrFactPayloadUpdate) {
		t.Errorf("a plaintext update of one row: %v, want ErrFactPayloadUpdate", err)
	}
	if err := client.Fact.Update().SetPayload(forged).Exec(ctx); !errors.Is(err, ErrFactPayloadUpdate) {
		t.Errorf("a plaintext update of every row: %v, want ErrFactPayloadUpdate", err)
	}
	// Something that names the marker and is no envelope is not sealed.
	if err := client.Fact.UpdateOneID(row.ID).SetPayload(map[string]any{EncryptedKeyMarker: "not an envelope"}).Exec(ctx); !errors.Is(err, ErrFactPayloadUpdate) {
		t.Errorf("an update to a marker with no envelope: %v, want ErrFactPayloadUpdate", err)
	}
	if opened, err := OpenFactPayload(svc, "token-a", client.Fact.GetX(ctx, row.ID).Payload); err != nil || opened["json"] != `"v"` {
		t.Fatalf("after the refused updates the row holds %v, %v", opened, err)
	}
	resealed, err := SealFactPayload(svc, "token-a", map[string]any{"key": "k", "json": `"v"`})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Fact.UpdateOneID(row.ID).SetPayload(resealed).Exec(ctx); err != nil {
		t.Errorf("an update to a sealed payload: %v", err)
	}
	if err := client.Fact.UpdateOneID(row.ID).SetUpdatedAt(row.UpdatedAt).Exec(ctx); err != nil {
		t.Errorf("an update that sets no payload: %v", err)
	}
}

// TestOpenFactPayload_OpensOnlyItsOwnRowsSealedPayload: another row's token,
// another key, a payload never sealed, one that opens to something else and
// no service at all are each an error.
func TestOpenFactPayload_OpensOnlyItsOwnRowsSealedPayload(t *testing.T) {
	svc := factService(t, 'k', "v1", 0, "")
	sealed, err := SealFactPayload(svc, "token-a", map[string]any{"key": "k", "json": `"v"`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFactPayload(svc, "token-b", sealed); err == nil {
		t.Error("a payload opened under another row's token")
	}
	if _, err := OpenFactPayload(factService(t, 's', "v1", 0, ""), "token-a", sealed); err == nil {
		t.Error("a payload opened under another key")
	}
	if _, err := OpenFactPayload(nil, "token-a", sealed); err == nil {
		t.Error("a payload opened with no service")
	}
	for name, payload := range map[string]map[string]any{
		"plaintext":            {"key": "k", "json": `"v"`},
		"nothing":              nil,
		"a marker among more":  {EncryptedKeyMarker: sealed[EncryptedKeyMarker], "key": "k"},
		"a marker of a number": {EncryptedKeyMarker: 7},
		"an unbound envelope":  {EncryptedKeyMarker: "v1$AES256GCM$a$b"},
	} {
		if _, err := OpenFactPayload(svc, "token-a", payload); !errors.Is(err, ErrFactNotSealed) {
			t.Errorf("%s: %v, want ErrFactNotSealed", name, err)
		}
	}
	// Sealed for this row, and not a fact's key and value.
	envelope, err := svc.EncryptBound([]byte(`["not","an","object"]`), factBinding("token-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFactPayload(svc, "token-a", map[string]any{EncryptedKeyMarker: envelope}); err == nil || errors.Is(err, ErrFactNotSealed) {
		t.Errorf("a sealed payload that is not an object: %v", err)
	}
}

// TestRotateFactPayloads_RotatesEveryPageAndThenWritesNothing: more rows than
// one page, all under the old key, are each sealed again under the new one;
// a row that was never sealed and one under a key the service does not hold
// are counted unreadable and left; a second pass writes nothing; and with the
// old key gone every rotated row still opens.
func TestRotateFactPayloads_RotatesEveryPageAndThenWritesNothing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facts.db")
	old := factService(t, 'o', "v1", 0, "")
	const seeded = factRotationPage + 3
	func() {
		client, dev := factClient(t, path, old)
		bulk := make([]*ent.FactCreate, seeded)
		for i := range bulk {
			bulk[i] = newFact(client, dev, fmt.Sprintf("token-%d", i), map[string]any{"key": fmt.Sprintf("package.p%d", i), "json": `"1.0"`}).SetChainPrev(fmt.Sprintf("c%d", i))
		}
		for start := 0; start < seeded; start += 100 {
			client.Fact.CreateBulk(bulk[start:min(start+100, seeded)]...).SaveX(ctx)
		}
		_ = client.Close()
		// One row a writer with no seal left, and one under a stranger's key.
		bare, dev := factClient(t, path, nil)
		newFact(bare, dev, "token-plain", map[string]any{"key": "k", "json": `"v"`}).SetChainPrev("plain").SaveX(ctx)
		stranger, err := SealFactPayload(factService(t, 's', "v0", 0, ""), "token-stranger", map[string]any{"key": "k", "json": `"v"`})
		if err != nil {
			t.Fatal(err)
		}
		newFact(bare, dev, "token-stranger", stranger).SetChainPrev("stranger").SaveX(ctx)
		_ = bare.Close()
	}()

	rotating := factService(t, 'n', "v2", 'o', "v1")
	client, _ := factClient(t, path, rotating)
	count, err := RotateFactPayloads(ctx, client, rotating)
	if err != nil || count != (RotationCount{Rotated: seeded, Unreadable: 2}) {
		t.Fatalf("the first pass = %+v, %v; want %d rotated and the two unreadable", count, err, seeded)
	}
	if again, err := RotateFactPayloads(ctx, client, rotating); err != nil || again != (RotationCount{Unreadable: 2}) {
		t.Fatalf("the second pass = %+v, %v; want nothing written", again, err)
	}
	_ = client.Close()

	newOnly := factService(t, 'n', "v2", 0, "")
	after, _ := factClient(t, path, newOnly)
	opened := 0
	for _, row := range after.Fact.Query().AllX(ctx) {
		if got, err := OpenFactPayload(newOnly, row.KeyToken, row.Payload); err == nil && got["json"] == `"1.0"` {
			opened++
		}
	}
	if opened != seeded {
		t.Errorf("%d of %d rows open with the previous key gone", opened, seeded)
	}
	// A table that cannot be listed is an error, never a count.
	_ = after.Close()
	if _, err := RotateFactPayloads(ctx, after, newOnly); err == nil {
		t.Error("a rotation over a closed database reported a count")
	}
}

// TestRotateFactPayloads_LeavesARowReKeyedSinceItWasRead: a change of the
// facts key (internal/facts/entstore's rekey.go) gives a row a new token and
// a payload sealed for it between this rotation's read of the row and its
// write. The rotation's payload, sealed for the old token, must not replace
// it: the row is skipped for a later pass, and still opens as its own.
func TestRotateFactPayloads_LeavesARowReKeyedSinceItWasRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facts.db")
	content := map[string]any{"key": "package.openssh", "json": `"9.6"`}
	func() {
		client, dev := factClient(t, path, factService(t, 'o', "v1", 0, ""))
		newFact(client, dev, "token-a", content).SaveX(ctx)
		_ = client.Close()
	}()
	rotating := factService(t, 'n', "v2", 'o', "v1")
	client, dev := factClient(t, path, rotating)
	rekeyed := false
	client.Fact.Use(func(next ent.Mutator) ent.Mutator {
		return hook.FactFunc(func(ctx context.Context, m *ent.FactMutation) (ent.Value, error) {
			id, ok := m.ID()
			if !m.Op().Is(ent.OpUpdateOne) || !ok || rekeyed {
				return next.Mutate(ctx, m)
			}
			rekeyed = true
			// What a re-key commits between the rotation's read and its write.
			tx, err := client.Tx(ctx)
			if err != nil {
				return nil, err
			}
			was := ent.FactKeyed{KeyToken: "token-a", Hash: "h", KeyedBy: "test:0"}
			if err := tx.RekeyFact(ctx, id, dev, was, ent.FactKeyed{KeyToken: "token-b", Hash: "h2", KeyedBy: "test:1"}); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			sealed, err := SealFactPayload(rotating, "token-b", content)
			if err == nil {
				err = tx.Fact.UpdateOneID(id).SetPayload(sealed).Exec(ctx)
			}
			if err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return next.Mutate(ctx, m)
		})
	})
	count, err := RotateFactPayloads(ctx, client, rotating)
	if err != nil || !rekeyed || count != (RotationCount{Skipped: 1}) {
		t.Fatalf("the pass = %+v, %v (re-keyed between: %v); want the row skipped", count, err, rekeyed)
	}
	row := client.Fact.Query().OnlyX(ctx)
	if got, err := OpenFactPayload(rotating, row.KeyToken, row.Payload); row.KeyToken != "token-b" || err != nil || got["json"] != `"9.6"` {
		t.Errorf("the re-keyed row holds token %q and opens as %v, %v; want its own payload kept", row.KeyToken, got, err)
	}
	if again, err := RotateFactPayloads(ctx, client, rotating); err != nil || again != (RotationCount{}) {
		t.Errorf("a later pass = %+v, %v; want the row already under the current key", again, err)
	}
}
