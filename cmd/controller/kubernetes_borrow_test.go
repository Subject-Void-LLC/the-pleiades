// Tests of the facts the Controller reads from its records about a cluster
// that has no credential of its own.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// TestBorrowingCluster reads a cluster's organization, pinned authority and
// route from the Controller's real records: a route that resolves through a
// stored bastion is Routed, and one naming a hop that does not exist is an
// error, so the fallback is refused rather than taken without knowing.
func TestBorrowingCluster(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "bastion", credential.Credential{Username: "jump", Password: "jump-pass"}); err != nil {
		t.Fatal(err)
	}
	creds := credential.NewLazyFileStore(dir)

	add := func(rec record.Record) pkginventory.InventoryItem {
		t.Helper()
		item, err := factory.Build(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	add(record.Record{ID: "b", Name: "bastion", Type: "linux_server", Properties: map[string]pkginventory.PropertyValue{"host": "10.0.0.9"}})
	ca := throwawayCAPEM(t)
	direct := add(record.Record{ID: "c1", Name: "direct", Type: kubernetes.TypeCluster, Properties: map[string]pkginventory.PropertyValue{
		kubernetes.APIServerProperty: "https://k.example:6443", devicetls.CAPEMProperty: ca}})
	if err := repo.(inventory.OrganizationAssigner).AssignDeviceOrganization(ctx, "direct", org.ID); err != nil {
		t.Fatal(err)
	}
	routed := add(record.Record{ID: "c2", Name: "routed", Type: kubernetes.TypeCluster, Properties: map[string]pkginventory.PropertyValue{
		kubernetes.APIServerProperty: "https://k.example:6443", "route": []any{"bastion"}}})
	broken := add(record.Record{ID: "c3", Name: "broken", Type: kubernetes.TypeCluster, Properties: map[string]pkginventory.PropertyValue{
		kubernetes.APIServerProperty: "https://k.example:6443", "route": []any{"no-such-hop"}}})

	b, err := borrowingCluster(ctx, repo, creds, direct)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "direct" || b.APIServer != "https://k.example:6443" || b.OrganizationID != org.ID || b.RecordCAPEM == "" || b.Routed {
		t.Errorf("direct = %+v", b)
	}
	if b, err := borrowingCluster(ctx, repo, creds, routed); err != nil || !b.Routed || b.OrganizationID != 0 {
		t.Errorf("routed = %+v, %v; want Routed and no organization", b, err)
	}
	if _, err := borrowingCluster(ctx, repo, creds, broken); err == nil {
		t.Error("a route naming a missing hop was read as no route")
	}
}

// throwawayCAPEM is a self-signed authority, as PEM, that nothing trusts.
func throwawayCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cluster ca"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
