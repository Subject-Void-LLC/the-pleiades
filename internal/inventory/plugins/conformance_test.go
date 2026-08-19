package plugins_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"
	awsplugin "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/catalystcenter"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/staticyaml"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This is the sync plugin conformance suite, the direct analogue of
// internal/inventory's repositoryBackend table: one set of assertions
// driving every real Plugin implementation through identical call
// sequences.
//
// Its purpose is specific. PLAN.md Section 6a's port was deliberately not
// built for the static YAML plugin alone, on the grounds that one consumer
// is not enough evidence to design a four-method interface around. This
// suite is what makes the second consumer count: if the port had been
// shaped around Catalyst Center's specifics, the file-backed plugin would
// fail these assertions, and if it had been shaped around a local file, the
// network-backed one would. Both passing the same suite is the evidence.
//
// The two backends are deliberately unalike. One reads a local document
// with no authentication, no paging, and a classification the file states
// outright; the other authenticates, pages, and derives its classification
// from raw upstream fields. A new plugin joins by adding one entry to
// pluginBackends, never by editing a test function.

// pluginBackend names one Plugin implementation under conformance test,
// plus how to construct a connected instance pointed at a controllable
// upstream holding exactly the devices given.
type pluginBackend struct {
	name string

	// newPlugin returns a connected plugin whose upstream reports one
	// device per entry in hosts, plus whatever else that plugin
	// inherently reports (the Catalyst one also reports the controller).
	newPlugin func(t *testing.T, hosts []conformanceHost) (syncplugin.Plugin, syncplugin.Config)

	// extraDevices is how many devices this plugin reports beyond the
	// requested hosts. The Catalyst plugin emits the controller itself, so
	// its counts are offset by one; the file-backed one emits exactly what
	// the document lists.
	extraDevices int

	// unclassifiable builds a host this plugin cannot place, for the
	// Section 6g quarantine assertion. Leave the zero value and set
	// unclassifiableUnsupported instead if this backend's upstream has no
	// way to produce one through this suite's harness.
	unclassifiable conformanceHost

	// unclassifiableUnsupported, when non-empty, is the reason
	// TestPluginConformance_QuarantinesUnclassifiable skips this backend
	// rather than asserting against unclassifiable. The "aws" backend
	// needs this: EC2's Platform field, the only signal this plugin's
	// Classify refuses on, is derived from the launched AMI's real
	// metadata, not something a RunInstances caller can set directly, and
	// LocalStack (confirmed empirically) does not infer it from a
	// fabricated AMI id either -- it reports Platform as either "" or
	// "windows", both of which now classify (linux_server/windows_server),
	// so there is no realistic Platform value left for RunInstances to
	// produce that would even ask Classify to quarantine. The quarantine
	// behavior itself is still proven, directly, by aws's own
	// TestClassify_UnrecognizedPlatform_Quarantines against a hand-built
	// record naming a Platform EC2 does not define — RULE 0 does not
	// require a live upstream for logic that is pure Go over an
	// already-discovered value.
	unclassifiableUnsupported string

	// checkIP validates the "ip" property recorded for hosts[0] once
	// synced. Nil means the default: an exact match against hosts[0].IP,
	// which static_yaml and catalyst_center's upstreams both honor
	// exactly since this suite controls what they report directly. "aws"
	// supplies its own: LocalStack (confirmed empirically) always assigns
	// its own public IP on top of any requested private one, so no
	// fixture can dictate the exact value a real cloud upstream reports.
	checkIP func(t *testing.T, hosts []conformanceHost, ip string)
}

// defaultCheckIP is checkIP's default: hosts[0].IP must come back exactly.
func defaultCheckIP(t *testing.T, hosts []conformanceHost, ip string) {
	t.Helper()
	if ip != hosts[0].IP {
		t.Errorf("ip = %q, want %q", ip, hosts[0].IP)
	}
}

// conformanceHost is one device the suite asks a backend's upstream to
// report, in terms general enough for both to express.
type conformanceHost struct {
	Name string
	IP   string

	// SoftwareType drives the network-backed plugin's classification. The
	// file-backed plugin ignores it and uses DeviceType instead.
	SoftwareType string

	// DeviceType is what the file-backed plugin's document states outright.
	DeviceType string
}

// conformanceHosts is the standard fixture: two devices both backends can
// classify.
func conformanceHosts() []conformanceHost {
	return []conformanceHost{
		{Name: "sw1", IP: "10.0.0.1", SoftwareType: "IOS-XE", DeviceType: "cisco_switch"},
		{Name: "sw2", IP: "10.0.0.2", SoftwareType: "IOS-XE", DeviceType: "cisco_switch"},
	}
}

// pluginBackends lists every Plugin implementation this suite runs against.
func pluginBackends() []pluginBackend {
	return []pluginBackend{
		{
			name:      "static_yaml",
			newPlugin: newStaticYAMLBackend,
			// The document lists exactly what it lists.
			extraDevices:   0,
			unclassifiable: conformanceHost{Name: "mystery1", IP: "10.0.0.9"},
		},
		{
			name:      "catalyst_center",
			newPlugin: newCatalystBackend,
			// Plus the controller itself, which this plugin emits so a sync
			// leaves inventory able to run net.catalyst.* runbooks.
			extraDevices:   1,
			unclassifiable: conformanceHost{Name: "mystery1", IP: "10.0.0.9", SoftwareType: "PlanetExpressOS"},
		},
		{
			name:      "aws",
			newPlugin: newAWSBackend,
			// Plus the account/region itself, which this plugin emits so a
			// sync leaves inventory able to run cloud.aws.* runbooks, the
			// same reasoning catalyst_center's controller record states.
			extraDevices: 1,
			// LocalStack does not infer an instance's Platform from a
			// fabricated AMI id (confirmed empirically), so this backend
			// cannot produce a naturally-unclassifiable host through
			// RunInstances the way the other two backends can through a
			// document field or a raw upstream field. See
			// unclassifiableUnsupported's own doc comment.
			unclassifiableUnsupported: "LocalStack cannot be made to report an EC2 instance's Platform as anything but empty or \"windows\" for a fabricated AMI id, and both of those now classify (linux_server/windows_server), so this backend has no way to produce an unclassifiable host through RunInstances; see aws's own TestClassify_UnrecognizedPlatform_Quarantines for the direct proof of that behavior against a hand-built record",
			// A real cloud upstream assigns its own addressing; see
			// checkIP's own doc comment for why this cannot be
			// hosts[0].IP.
			checkIP: func(t *testing.T, _ []conformanceHost, ip string) {
				t.Helper()
				if ip == "" {
					t.Error("ip is empty, want the real address DescribeInstances reported")
				}
			},
		},
	}
}

// newStaticYAMLBackend writes an upstream document and connects a plugin to
// it.
func newStaticYAMLBackend(t *testing.T, hosts []conformanceHost) (syncplugin.Plugin, syncplugin.Config) {
	t.Helper()

	specs := make([]inv.HostSpec, 0, len(hosts))
	for _, h := range hosts {
		specs = append(specs, inv.HostSpec{
			ID:         "id-" + h.Name,
			Name:       h.Name,
			Type:       h.DeviceType,
			Properties: map[string]interface{}{"host": h.IP, "ip": h.IP},
		})
	}

	path := filepath.Join(t.TempDir(), "upstream.yaml")
	if err := inv.WriteHosts(path, specs); err != nil {
		t.Fatalf("writing upstream document: %v", err)
	}

	cfg := syncplugin.Config{
		Name:     staticyaml.Name,
		Endpoint: (&url.URL{Scheme: "file", Path: path}).String(),
	}
	p := staticyaml.New(inv.NewItemFactory())
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return p, cfg
}

// staticStore is a credential.Store returning one fixed credential.
type staticStore struct{ cred credential.Credential }

func (s staticStore) Lookup(context.Context, string) (credential.Credential, error) {
	return s.cred, nil
}

// newCatalystBackend stands up a fake controller reporting hosts and
// connects a plugin to it.
func newCatalystBackend(t *testing.T, hosts []conformanceHost) (syncplugin.Plugin, syncplugin.Config) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "devnetuser" || pass != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
			return
		}
		if r.Header.Get("X-Auth-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			fmt.Fprintf(w, `{"response":%d}`, len(hosts))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			_, _ = w.Write(catalystDevicePage(hosts, r))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := syncplugin.Config{
		Name:     catalystcenter.Name,
		Endpoint: srv.URL,
		ReadOnly: true,
		// Below the fixture size, so the multi-page path runs.
		PageSize: 1,
	}
	p := catalystcenter.New(catalystcenter.WithCredentialStore(
		staticStore{credential.Credential{Username: "devnetuser", Password: "secret"}}))
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, cfg
}

// catalystDevicePage renders the requested page of hosts in the
// controller's wire shape.
func catalystDevicePage(hosts []conformanceHost, r *http.Request) []byte {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = len(hosts)
	}

	start := offset - 1
	if start > len(hosts) {
		start = len(hosts)
	}
	end := start + limit
	if end > len(hosts) {
		end = len(hosts)
	}

	var b strings.Builder
	b.WriteString(`{"response":[`)
	for i, h := range hosts[start:end] {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"id-%s","hostname":"%s","managementIpAddress":"%s",`+
			`"family":"Switches and Hubs","softwareType":"%s","softwareVersion":"17.12.1",`+
			`"reachabilityStatus":"Reachable","collectionStatus":"Managed"}`,
			h.Name, h.Name, h.IP, h.SoftwareType)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// newAWSBackend launches one real EC2 instance per host in a shared
// LocalStack container and connects a plugin to it.
//
// Unlike the two backends above, this one cannot fake its upstream with an
// httptest.Server: the AWS SDK's real request signing and wire protocol
// are what this plugin actually has to work against, and a hand-rolled
// fake would stop being representative the moment a real one is needed
// again. See pkg/awscloud's own tests for why that means a real
// LocalStack container (and LOCALSTACK_AUTH_TOKEN) rather than an
// anonymous one.
func newAWSBackend(t *testing.T, hosts []conformanceHost) (syncplugin.Plugin, syncplugin.Config) {
	t.Helper()
	endpoint := requireLocalStackForConformance(t)

	client, err := awscloud.New(awsConformanceRegion, awsConformanceKey, awsConformanceSecret, "", awscloud.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("awscloud.New: %v", err)
	}
	ctx := context.Background()
	for _, h := range hosts {
		launched, err := client.RunInstance(ctx, h.Name, "ami-12345678", "t2.micro")
		if err != nil {
			t.Fatalf("RunInstance(%s): %v", h.Name, err)
		}
		// Every subtest across this suite shares one LocalStack account
		// (one real container, started once), unlike the other two
		// backends' own fresh-per-call fake server or temp file. Without
		// terminating what this call launched, an EARLIER subtest's
		// instances would still be discoverable when a LATER one runs,
		// which is exactly the duplicate-name pollution a fresh fake or
		// temp file never has to guard against.
		instanceID := launched.ID
		t.Cleanup(func() {
			if _, err := client.TerminateInstance(context.Background(), instanceID); err != nil {
				t.Logf("cleanup: terminating %s: %v", instanceID, err)
			}
		})
	}

	cfg := syncplugin.Config{
		Name:     awsplugin.Name,
		Endpoint: endpoint,
		ReadOnly: true,
		// Below the fixture size (once minPageSize's floor is accounted
		// for further tests won't all land on one page), matching
		// catalystDevicePage's own PageSize: 1 intent: force the
		// multi-page path to run rather than only ever exercising a
		// single-page fetch.
		PageSize: 1,
	}
	p := awsplugin.New(
		awsplugin.WithRegion(awsConformanceRegion),
		awsplugin.WithCredentialStore(staticStore{credential.Credential{Username: awsConformanceKey, Password: awsConformanceSecret}}),
	)
	if err := p.Connect(ctx, cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, cfg
}

// LocalStack accepts any non-empty static credential by default; this is
// a throwaway container started fresh for this test run, never a real
// account, so a hardcoded key pair here is not a secret leak.
const (
	awsConformanceKey    = "test"
	awsConformanceSecret = "test"
	awsConformanceRegion = "us-east-1"
)

// Shared LocalStack container for the "aws" backend, mirroring the exact
// pattern pkg/awscloud's, cloud.aws.{ec2,s3}'s, and the aws plugin's own
// package tests already established this session: lazy, exactly-once
// startup guarded by sync.Once, torn down once by TestMain after every
// test in this package has run.
var (
	awsConformanceContainerOnce sync.Once
	awsConformanceContainerErr  error
	awsConformanceContainer     *localstack.LocalStackContainer
	awsConformanceEndpoint      string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if awsConformanceContainer != nil {
		_ = awsConformanceContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

func requireLocalStackForConformance(tb testing.TB) string {
	tb.Helper()
	token := os.Getenv("LOCALSTACK_AUTH_TOKEN")
	if token == "" {
		tb.Skip("set LOCALSTACK_AUTH_TOKEN to run the conformance suite's aws backend")
	}
	awsConformanceContainerOnce.Do(func() {
		ctx := context.Background()
		ctr, err := localstack.Run(ctx, testsupport.LocalStackImage,
			testcontainers.WithEnv(map[string]string{"LOCALSTACK_AUTH_TOKEN": token}),
		)
		if err != nil {
			awsConformanceContainerErr = fmt.Errorf("failed to start localstack container: %w", err)
			return
		}
		awsConformanceContainer = ctr

		mappedPort, err := ctr.MappedPort(ctx, "4566/tcp")
		if err != nil {
			awsConformanceContainerErr = fmt.Errorf("failed to get mapped port: %w", err)
			return
		}
		host, err := ctr.Host(ctx)
		if err != nil {
			awsConformanceContainerErr = fmt.Errorf("failed to get container host: %w", err)
			return
		}
		awsConformanceEndpoint = fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
	})
	if awsConformanceContainerErr != nil {
		tb.Fatalf("shared LocalStack container setup failed: %v", awsConformanceContainerErr)
	}
	return awsConformanceEndpoint
}

// newConformanceRepo builds an empty project inventory to reconcile into.
func newConformanceRepo(t *testing.T) inv.Repository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating project inventory: %v", err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

// TestPluginConformance_SyncOnboardsEveryDevice proves a first sync into an
// empty project adds every discovered device, with provenance stamped.
func TestPluginConformance_SyncOnboardsEveryDevice(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			hosts := conformanceHosts()
			p, cfg := backend.newPlugin(t, hosts)
			repo := newConformanceRepo(t)

			report, err := p.Sync(context.Background(), repo)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}

			want := len(hosts) + backend.extraDevices
			if report.Total() != want {
				t.Fatalf("discovered %d devices, want %d (report: %+v)", report.Total(), want, report.Results)
			}
			if got := report.Count(syncplugin.OutcomeAdded); got != want {
				t.Fatalf("added %d, want %d (report: %+v)", got, want, report.Results)
			}

			item, err := repo.GetByName(context.Background(), "sw1")
			if err != nil {
				t.Fatalf("GetByName(sw1): %v", err)
			}
			if item.Source().Plugin != cfg.Name {
				t.Errorf("Source().Plugin = %q, want %q", item.Source().Plugin, cfg.Name)
			}
			if item.Source().SyncedAt.IsZero() {
				t.Error("sync did not stamp SyncedAt")
			}
			checkIP := backend.checkIP
			if checkIP == nil {
				checkIP = defaultCheckIP
			}
			ip, _ := item.Properties().String("ip")
			checkIP(t, hosts, ip)
		})
	}
}

// TestPluginConformance_ResyncIsIdempotent proves a second sync of an
// unchanged upstream writes nothing. This is what makes a scheduled sync
// safe on a timer.
func TestPluginConformance_ResyncIsIdempotent(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			p, _ := backend.newPlugin(t, conformanceHosts())
			repo := newConformanceRepo(t)
			ctx := context.Background()

			first, err := p.Sync(ctx, repo)
			if err != nil {
				t.Fatalf("first Sync: %v", err)
			}
			before, err := repo.GetByName(ctx, "sw1")
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}

			second, err := p.Sync(ctx, repo)
			if err != nil {
				t.Fatalf("second Sync: %v", err)
			}
			if got := second.Count(syncplugin.OutcomeUnchanged); got != first.Total() {
				t.Fatalf("second sync reported %d of %d unchanged, want all (report: %+v)",
					got, first.Total(), second.Results)
			}

			after, err := repo.GetByName(ctx, "sw1")
			if err != nil {
				t.Fatalf("GetByName after re-sync: %v", err)
			}
			if after.Version() != before.Version() {
				t.Errorf("version moved from %d to %d on an unchanged re-sync", before.Version(), after.Version())
			}
		})
	}
}

// TestPluginConformance_QuarantinesUnclassifiable proves Section 6g holds
// for both: a device neither can place is reported with a reason rather
// than erroring the sync or being guessed at.
func TestPluginConformance_QuarantinesUnclassifiable(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			if backend.unclassifiableUnsupported != "" {
				t.Skip(backend.unclassifiableUnsupported)
			}
			p, _ := backend.newPlugin(t, []conformanceHost{backend.unclassifiable})

			report, err := p.Sync(context.Background(), newConformanceRepo(t))
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if got := report.Count(syncplugin.OutcomeQuarantined); got != 1 {
				t.Fatalf("quarantined %d, want 1 (report: %+v)", got, report.Results)
			}
			for _, res := range report.Results {
				if res.Outcome == syncplugin.OutcomeQuarantined && res.Reason == "" {
					t.Errorf("device %s was quarantined with no reason", res.Name)
				}
			}
		})
	}
}

// TestPluginConformance_ForeignOwnerIsAConflict proves Section 11's One
// Authority Per Item holds for both.
func TestPluginConformance_ForeignOwnerIsAConflict(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			hosts := conformanceHosts()
			p, _ := backend.newPlugin(t, hosts)
			repo := newConformanceRepo(t)
			ctx := context.Background()

			// Onboard under a different plugin's name using the same real
			// discovery, which is exactly what a competing source would do.
			if _, err := syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "netbox"}, repo, inv.NewItemFactory()); err != nil {
				t.Fatalf("seeding under a foreign source: %v", err)
			}

			report, err := p.Sync(ctx, repo)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			want := len(hosts) + backend.extraDevices
			if got := report.Count(syncplugin.OutcomeConflict); got != want {
				t.Fatalf("conflicts = %d, want %d (report: %+v)", got, want, report.Results)
			}

			item, err := repo.GetByName(ctx, "sw1")
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if item.Source().Plugin != "netbox" {
				t.Errorf("the original owner was overwritten: Source().Plugin = %q", item.Source().Plugin)
			}
		})
	}
}

// TestPluginConformance_ReadOnlyInventoryIsADryRun proves both plugins
// report what would change and write nothing when the local inventory is
// open read-only.
func TestPluginConformance_ReadOnlyInventoryIsADryRun(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			hosts := conformanceHosts()
			p, _ := backend.newPlugin(t, hosts)
			inner := newConformanceRepo(t)

			report, err := p.Sync(context.Background(), inv.NewReadOnlyRepository(inner))
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}

			want := len(hosts) + backend.extraDevices
			if got := report.Count(syncplugin.OutcomeWouldAdd); got != want {
				t.Fatalf("would-add = %d, want %d (report: %+v)", got, want, report.Results)
			}
			if _, err := inner.GetByName(context.Background(), "sw1"); !errors.Is(err, inv.ErrItemNotFound) {
				t.Errorf("a device reached storage despite read-only: %v", err)
			}
		})
	}
}

// TestPluginConformance_DiscoverStreamsUnclassifiedRecords proves Discover
// yields records before classification has run, which is the contract that
// lets Classify be a separate, testable step rather than something Discover
// silently did already.
func TestPluginConformance_DiscoverStreamsUnclassifiedRecords(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			hosts := conformanceHosts()
			p, _ := backend.newPlugin(t, hosts)
			ctx := context.Background()

			it, err := p.Discover(ctx)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			defer func() { _ = it.Close() }()

			var seen int
			names := map[string]bool{}
			for it.Next(ctx) {
				rec := it.Record()
				seen++
				if rec.Name == "" {
					t.Error("Discover yielded a record with no name")
				}
				if names[rec.Name] {
					t.Errorf("Discover yielded %q twice", rec.Name)
				}
				names[rec.Name] = true

				// State is set by Classify, not Discover. The zero value is
				// StateDiscovered, so anything else means Discover already
				// decided something it should not have.
				if rec.State != inventory.StateDiscovered {
					t.Errorf("record %q arrived from Discover already in state %v", rec.Name, rec.State)
				}
			}
			if err := it.Error(); err != nil {
				t.Fatalf("iterating: %v", err)
			}

			want := len(hosts) + backend.extraDevices
			if seen != want {
				t.Errorf("Discover yielded %d records, want %d", seen, want)
			}
		})
	}
}

// TestPluginConformance_CloseIsSafe proves Close may be called on a
// connected plugin and again afterwards, which is what makes it usable in a
// defer.
func TestPluginConformance_CloseIsSafe(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			p, _ := backend.newPlugin(t, conformanceHosts())

			if err := p.Close(); err != nil {
				t.Fatalf("first Close: %v", err)
			}
			if err := p.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	}
}

// TestPluginConformance_RegisteredAndReachable proves every backend in this
// suite is reachable through the shared registry from a binary that imports
// the plugins composition root. That is the FAILURE_PATTERNS.md #52 check
// applied to sync plugins.
func TestPluginConformance_RegisteredAndReachable(t *testing.T) {
	for _, backend := range pluginBackends() {
		t.Run(backend.name, func(t *testing.T) {
			desc, ok := syncplugin.Lookup(backend.name)
			if !ok {
				t.Fatalf("plugin %q is not registered; internal/inventory/plugins/builtins.go may be missing its blank import", backend.name)
			}
			if desc.New == nil || desc.New() == nil {
				t.Fatal("registered descriptor cannot construct a plugin")
			}
			if desc.Description == "" {
				t.Error("registered descriptor has no description")
			}
			if !desc.Implemented() {
				t.Errorf("plugin %q is in the conformance suite but registered as %q", backend.name, desc.Status)
			}
		})
	}
}
