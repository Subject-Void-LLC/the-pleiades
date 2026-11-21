// Fuzzing what an inspection parses (inspect_check.go, sealed.go): a stored
// row as a database edited by anyone can hold it, a payload of any JSON, a
// token, a hash, a chain link and a kind. The check never panics, gives
// every failure one of the kinds the report publishes, and never names a
// fact it was not asked to.
package entstore

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// FuzzRowCheck holds one arbitrary stored row to the check an inspection
// makes of it under the store's keys.
func FuzzRowCheck(f *testing.F) {
	keys, env := factstest.Keys(), factstest.Envelope()
	token, _ := keys.Token("dev1", "os.kernel")
	hash, _ := keys.Hash("dev1", "os.kernel", "6.1")
	sealed, err := crypto.SealFactPayload(env, token, map[string]any{payloadFactKey: "os.kernel", payloadKey: `"6.1"`})
	if err != nil {
		f.Fatal(err)
	}
	real, _ := json.Marshal(sealed)
	chain, _ := keys.ChainHash("dev1", "", "os.kernel", hash, time.Unix(0, 0), facts.Provenance{})
	f.Add(string(real), token, hash, "", chain, true)
	f.Add(`{"json":"1"}`, token, hash, "", chain, true)
	f.Add(`{"enc":"v2:not-an-envelope"}`, "", "", "x", "", false)
	f.Add(`null`, "zz", "zz", "\x00", "\x1b[2J", true)
	f.Fuzz(func(t *testing.T, payload, token, hash, prev, chain string, stable bool) {
		var decoded map[string]any
		_ = json.Unmarshal([]byte(payload), &decoded)
		row := &ent.Fact{ID: 1, KeyToken: token, Payload: decoded, Hash: hash, KeyedBy: keys.ID(), Kind: entfact.KindVolatile, Chain: chain, CreatedAt: time.Unix(0, 0)}
		if stable {
			row.Kind, row.ChainPrev = entfact.KindStable, &prev
		}
		s := New(nil, keys, env)
		for _, show := range []bool{false, true} {
			c := newUnderCheck(factsreport.RoleCurrent, keys, "dev1", show)
			opened, openErr := s.openPayload(row)
			c.see(row, 0, opened, openErr)
			if (c.report.Opened+c.report.Failed != 1) || (c.report.Failure == nil) != (c.report.Checked == 1) {
				t.Fatalf("one row checked as %+v", c.report)
			}
			if f := c.report.Failure; f != nil {
				if !slices.Contains(factsreport.KnownFailureKinds, string(f.Kind)) {
					t.Fatalf("a failure of kind %q, which the report does not publish", f.Kind)
				}
				if f.Fact != "" && (!show || openErr != nil) {
					t.Fatalf("a fact named %q without being asked, or from a row that did not open", f.Fact)
				}
				if (f.ChainIndex != nil) != stable || f.KeyID != keys.ID() {
					t.Fatalf("failure %+v of a row stable %v", f, stable)
				}
			}
		}
	})
}
