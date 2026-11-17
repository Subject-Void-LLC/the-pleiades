// Tests of a Controller and a Runner of different releases over the
// certificate facts: a Runner of this release records tls.certificate.*
// under net.gnmi.facts, net.restconf.facts and their config methods, which a
// Controller of an earlier release does not declare for them.
package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
)

// olderCatalog is the embedded module catalog with every tls.certificate.*
// declaration removed, which is the catalog of a Controller from before the
// facts methods recorded the certificate.
func olderCatalog(t *testing.T) []byte {
	t.Helper()
	var manifests map[string]map[string]any
	if err := json.Unmarshal(wellknown.ModuleCatalog, &manifests); err != nil {
		t.Fatal(err)
	}
	removed := 0
	for name, m := range manifests {
		declared, ok := m["facts"].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(declared))
		for _, f := range declared {
			if key, _ := f.(map[string]any)["key"].(string); strings.HasPrefix(key, "tls.certificate.") {
				removed++
				continue
			}
			kept = append(kept, f)
		}
		m["facts"] = kept
		manifests[name] = m
	}
	if removed == 0 {
		t.Fatal("control: this catalog declares no tls.certificate.* fact, so it is no newer than the older one")
	}
	data, err := json.Marshal(manifests)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// certificateBatch is the facts batch a Runner of this release publishes for
// one net.gnmi.facts read: a gNMI fact its method has always declared, and
// the certificate's facts as tlsfacts.Record writes them, for a real
// certificate (pkg/devicetls's OpenSSL vector), so the values are the shapes
// a secret guard sees in a real batch.
func certificateBatch(t *testing.T) ([]byte, int) {
	t.Helper()
	data, err := os.ReadFile("../../pkg/devicetls/testdata/spki-vector.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("the vector holds no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	c := devicetls.Describe(cert)
	observed := map[string]any{
		"gnmi.version": "0.7.0", tlsfacts.FactFingerprint: c.Fingerprint, tlsfacts.FactKeyFingerprint: c.KeyFingerprint,
		tlsfacts.FactSubject: c.Subject, tlsfacts.FactIssuer: c.Issuer, tlsfacts.FactNotAfter: c.NotAfterText(),
	}
	if len(observed) != len(tlsfacts.FactKeys)+1 {
		t.Fatalf("the batch holds %d certificate facts, tlsfacts records %d", len(observed)-1, len(tlsfacts.FactKeys))
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		t.Fatalf("preparing the batch: %v, %+v", err, problems)
	}
	batch, err := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Method: "net.gnmi.facts", Node: "tasks[0]"}})
	if err != nil {
		t.Fatal(err)
	}
	return batch, len(observed)
}

// gnmiFactsNode resolves every node to net.gnmi.facts, as main's resolver
// does for the one task of a facts read.
func gnmiFactsNode(context.Context, string, string, string) (journal.NodeMethod, error) {
	return journal.NodeMethod{Resolved: true, Method: "net.gnmi.facts"}, nil
}

// TestFactsSkew_AnOlderControllerKeepsTheCertificateFacts: a Controller whose
// catalog does not declare tls.certificate.* for net.gnmi.facts, a method it
// does know, keeps every fact of the batch, the certificate's with the kind
// the Runner sent and the method's own as declared, and warns about nothing:
// facts.Verify holds a key the catalog does not declare to the Runner's kind
// rather than refusing it, and a first-party method's facts are kept whole.
// A Controller of this release, which declares them, keeps the same batch.
func TestFactsSkew_AnOlderControllerKeepsTheCertificateFacts(t *testing.T) {
	older := olderCatalog(t)
	index, err := entstore.CatalogIndex(older)
	if err != nil {
		t.Fatal(err)
	}
	if _, declared := index.Lookup(tlsfacts.FactFingerprint); declared {
		t.Fatal("control: the older catalog still declares the certificate")
	}
	builtins, err := entstore.BuiltinMethods(older)
	if err != nil || !builtins["net.gnmi.facts"] {
		t.Fatalf("the older catalog does not name net.gnmi.facts: %v", err)
	}
	batch, want := certificateBatch(t)

	store := &recordingStore{}
	var logged bytes.Buffer
	sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil,
		entstore.WithDeclarations(index), entstore.WithBuiltinProvenance(builtins), entstore.WithNodeResolver(gnmiFactsNode))
	if err := sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: batch}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != want || strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("the older Controller stored %d of %d facts:\n%s", len(store.got), want, logged.String())
	}
	for _, e := range store.got {
		if e.Kind != facts.KindStable {
			t.Errorf("%s stored as %s, want the stable kind the Runner sent", e.Key, e.Kind)
		}
	}

	current := &recordingStore{}
	logged.Reset()
	sub, err = newFactsSubscriber(current, slog.New(slog.NewTextHandler(&logged, nil)), nil, entstore.WithNodeResolver(gnmiFactsNode))
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Handle(event.Event{ID: "e2", Type: facts.EventType, Data: batch}); err != nil {
		t.Fatal(err)
	}
	if len(current.got) != want || strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("this release's Controller stored %d of %d facts:\n%s", len(current.got), want, logged.String())
	}
}
