// The Toxiproxy gate for backup and restore: a connection cut partway
// through leaves no backup file behind and the live database as it was.
package backup_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// proxied is a server reached two ways: directly, for the test's own
// checks, and through a proxy the test can cut, for the code under test.
type proxied struct {
	direct, through server
	proxy           *toxiproxyclient.Proxy
}

// startProxied starts PostgreSQL behind Toxiproxy on one network.
func startProxied(t *testing.T) proxied {
	t.Helper()
	if testing.Short() {
		t.Skip("starts postgres and toxiproxy containers")
	}
	requireTools(t)
	ctx := context.Background()
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("creating the network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	pg, err := testpg.Run(ctx, testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"), testpg.WithUsername("pleiades"), testpg.WithPassword("password"),
		testpg.BasicWaitStrategies(), network.WithNetwork([]string{"postgres"}, nw))
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	toxi, err := tctoxiproxy.Run(ctx, testsupport.ToxiproxyImage,
		tctoxiproxy.WithProxy("postgres", "postgres:5432"), network.WithNetwork([]string{"toxiproxy"}, nw))
	if err != nil {
		t.Fatalf("starting toxiproxy: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(toxi) })

	host, port, err := toxi.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := toxi.URI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := toxiproxyclient.NewClient(uri).Proxies()
	if err != nil {
		t.Fatal(err)
	}
	direct, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return proxied{
		direct:  server{dsn: direct},
		through: server{dsn: "postgres://pleiades:password@" + host + ":" + port + "/pleiades?sslmode=disable"},
		proxy:   proxies["postgres"],
	}
}

// slowThenCut limits the link to a trickle so the transfer in flight is
// still running, then cuts it after delay. The returned channel closes once
// the cut is made; the toxiproxy client is not safe to use from two
// goroutines at once, so nothing else touches the proxy before it closes.
func slowThenCut(t *testing.T, p proxied, stream string, delay time.Duration) <-chan struct{} {
	t.Helper()
	if _, err := p.proxy.AddToxic("trickle", "bandwidth", stream, 1, toxiproxyclient.Attributes{"rate": 4}); err != nil {
		t.Fatalf("adding the bandwidth toxic: %v", err)
	}
	cut := make(chan struct{})
	go func() {
		defer close(cut)
		time.Sleep(delay)
		_ = p.proxy.Disable()
	}()
	return cut
}

// heal waits for the cut, removes the toxic and reopens the link.
func heal(t *testing.T, p proxied, cut <-chan struct{}) {
	t.Helper()
	<-cut
	_ = p.proxy.RemoveToxic("trickle")
	if err := p.proxy.Enable(); err != nil {
		t.Fatalf("reopening the link: %v", err)
	}
}

// TestTake_ACutConnectionLeavesNoBackup cuts the link while pg_dump is
// reading. A backup that stopped partway is the worst kind to keep, because
// it looks like one; none may be left under any name.
func TestTake_ACutConnectionLeavesNoBackup(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, ignore) })
	p := startProxied(t)
	key := testKey('p')
	p.direct.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))

	cut := slowThenCut(t, p, "downstream", 1500*time.Millisecond)
	_, err := backup.Take(context.Background(), backup.Options{DSN: p.through.dsn, SetupDir: d.setup, BackupDir: d.backups}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Take() over a link cut partway through succeeded")
	}
	// Measured: the cut lands inside pg_dump, not before it, so this is the
	// failure a dump stopped partway produces.
	if !strings.Contains(err.Error(), "pg_dump failed") || !strings.Contains(err.Error(), "no backup was written") {
		t.Errorf("Take() error = %v, want pg_dump's failure and that no backup was written", err)
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 0 {
		t.Fatalf("a cut backup left %d files behind: %v", len(entries), entries)
	}

	heal(t, p, cut)
	if _, err := backup.Take(context.Background(), backup.Options{DSN: p.through.dsn, SetupDir: d.setup, BackupDir: d.backups}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Take() after the link came back: %v", err)
	}
}

// TestRestore_ACutConnectionChangesNothingAndTheNextRunCleansUp cuts the
// link while pg_restore is writing the scratch database. The live database
// must be untouched, and the restore run after the link comes back removes
// what the cut one left and succeeds.
func TestRestore_ACutConnectionChangesNothingAndTheNextRunCleansUp(t *testing.T) {
	p := startProxied(t)
	key := testKey('q')
	p.direct.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))
	file, _ := takeBackup(t, p.direct, d)
	before := liveFingerprint(t, p.direct)

	cut := slowThenCut(t, p, "upstream", 1500*time.Millisecond)
	_, out, err := restore(p.through, d, file, nil)
	if err == nil {
		t.Fatalf("Restore() over a link cut partway through succeeded\n%s", out)
	}
	// Measured: the cut lands inside pg_restore, while the scratch database
	// is being written.
	if !strings.Contains(err.Error(), "pg_restore failed") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("Restore() error = %v, want pg_restore's failure and that nothing was changed", err)
	}
	if liveFingerprint(t, p.direct) != before {
		t.Fatal("a restore cut partway through changed the live database")
	}

	heal(t, p, cut)
	if _, out, err := restore(p.through, d, file, nil); err != nil {
		t.Fatalf("Restore() after the link came back: %v\n%s", err, out)
	}
	p.direct.assertNoLeftovers(t)
	for column, ok := range p.direct.readBack(t, key, crypto.DefaultKeyVersion) {
		if !ok {
			t.Errorf("%s did not read back after the second restore", column)
		}
	}
}
