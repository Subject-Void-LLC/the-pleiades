// The Toxiproxy gate for reading an existing database: a severed connection is
// an error, never an empty result.
package ent_test

import (
	"context"
	"testing"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestOpenExisting_SeveredConnectionIsAnErrorNotAnEmptyDatabase is the
// Toxiproxy gate for the setup command's census.
//
// The census decides whether writing a new master key would make stored data
// unreadable, and it refuses when it finds any. The one answer it must never
// get wrong in the permissive direction is "this database holds nothing",
// and a severed connection is the realistic way to get that answer by
// accident: a read that fails partway and returns what it had so far looks,
// to a counter, exactly like a small or empty table.
//
// So the connection is cut with a live handle open, the read must fail, and
// after the link is restored the same handle must read the row again.
func TestOpenExisting_SeveredConnectionIsAnErrorNotAnEmptyDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("starts postgres and toxiproxy containers")
	}
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("creating the network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	pg, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testpg.BasicWaitStrategies(),
		network.WithNetwork([]string{"postgres"}, nw),
	)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })

	toxi, err := tctoxiproxy.Run(ctx,
		testsupport.ToxiproxyImage,
		tctoxiproxy.WithProxy("postgres", "postgres:5432"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
	)
	if err != nil {
		t.Fatalf("starting toxiproxy: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(toxi) })

	host, port, err := toxi.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatalf("reading the proxied endpoint: %v", err)
	}
	uri, err := toxi.URI(ctx)
	if err != nil {
		t.Fatalf("reading the toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(uri).Proxies()
	if err != nil {
		t.Fatalf("listing proxies: %v", err)
	}
	proxy, ok := proxies["postgres"]
	if !ok {
		t.Fatal("toxiproxy has no postgres proxy")
	}

	dsn := "postgres://pleiades:pleiades@" + host + ":" + port + "/pleiades?sslmode=disable"
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	client.Device.Create().
		SetName("router-1").SetType("cisco_router").
		SetProperties(map[string]any{"_encrypted": "sealed"}).
		SaveX(ctx)
	_ = client.Close()

	db, err := ent.OpenExisting(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenExisting() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A control: the read works before anything is cut.
	if rows, err := db.StoredValues(ctx, "devices", "properties"); err != nil || len(rows) != 1 {
		t.Fatalf("StoredValues() before the cut = %v, %v; want the one row", rows, err)
	}

	if err := proxy.Disable(); err != nil {
		t.Fatalf("severing the link: %v", err)
	}
	rows, err := db.StoredValues(ctx, "devices", "properties")
	if err == nil {
		t.Fatalf("StoredValues() over a severed link returned %d rows and no error; a census would read that as the truth", len(rows))
	}

	if err := proxy.Enable(); err != nil {
		t.Fatalf("restoring the link: %v", err)
	}
	if rows, err := db.StoredValues(ctx, "devices", "properties"); err != nil || len(rows) != 1 {
		t.Fatalf("StoredValues() after the link came back = %v, %v; want the one row", rows, err)
	}
}
