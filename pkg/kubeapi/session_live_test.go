// Live tests for the port against a real k3s API server: each identity
// form reaches it, and a cluster refuses what it should.
package kubeapi_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

var services = schema.GroupVersionResource{Version: "v1", Resource: "services"}

// TestSession_EachIdentityReachesTheCluster reads the server's version and
// lists Services with a ServiceAccount token, with the administrator's
// client certificate, and with a minted token, against the newest k3s.
func TestSession_EachIdentityReachesTheCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})
	// An explicit role, not the built-in view: view's rules are aggregated
	// by a controller after startup, so a test asking at once can find it
	// empty.
	if _, err := k.Kubectl(context.Background(), "create", "clusterrole", "service-reader", "--verb=get,list,watch", "--resource=services"); err != nil {
		t.Fatal(err)
	}
	token := k.ServiceAccountToken(t, "", "reader", "service-reader")
	ep := kubeapi.Endpoint{Server: k.Server, CAPEM: []byte(k.CAPEM)}
	minted := 0
	for name, id := range map[string]kubeapi.Identity{
		"token":              {Token: token},
		"client certificate": {ClientCertPEM: []byte(k.ClientCert), ClientKeyPEM: []byte(k.ClientKey)},
		"minted token": {Mint: func(context.Context) (string, time.Time, error) {
			minted++
			return token, time.Now().Add(time.Hour), nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := kubeapi.NewSession(ep, id, kubeapi.Limits{}, "")
			if err != nil {
				t.Fatal(err)
			}
			v, err := s.Discovery.ServerVersionWithContext(context.Background())
			if err != nil || !strings.HasPrefix(v.GitVersion, "v1.37.") {
				t.Fatalf("ServerVersion = %v, %v; want the newest pinned minor", v, err)
			}
			// The kubernetes Service exists in default as soon as the API
			// server does, so the list is never empty when the read worked.
			list, err := s.Dynamic.Resource(services).Namespace(s.Namespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("listing services: %v", err)
			}
			if len(list.Items) == 0 {
				t.Fatal("listing services found none")
			}
		})
	}
	if minted != 1 {
		t.Errorf("minted %d tokens for one session's two clients, a token an hour from expiry, want 1: one cache serves them all", minted)
	}
}

// TestSession_AnUnpinnedAuthorityIsRefused proves the cluster's own CA is
// not trusted unless pinned: k3s's authority is its own, so a session with
// the system's roots refuses the handshake.
func TestSession_AnUnpinnedAuthorityIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})
	s, err := kubeapi.NewSession(kubeapi.Endpoint{Server: k.Server}, kubeapi.Identity{ClientCertPEM: []byte(k.ClientCert), ClientKeyPEM: []byte(k.ClientKey)}, kubeapi.Limits{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Discovery.ServerVersionWithContext(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("ServerVersion without the pinned CA = %v, want a certificate refusal", err)
	}
}

// TestSession_TwoHundredConcurrentReads is 119c's stress gate: 200 reads at
// once through one session against a real API server, every one answered,
// and the session's own limiter (DefaultLimits) pacing them, so a burst
// from one task cannot flood a shared cluster.
func TestSession_TwoHundredConcurrentReads(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})
	s, err := kubeapi.NewSession(kubeapi.Endpoint{Server: k.Server, CAPEM: []byte(k.CAPEM)},
		kubeapi.Identity{ClientCertPEM: []byte(k.ClientCert), ClientKeyPEM: []byte(k.ClientKey)}, kubeapi.Limits{}, "")
	if err != nil {
		t.Fatal(err)
	}
	const reads = 200
	errs := make(chan error, reads)
	begun := time.Now()
	var wg sync.WaitGroup
	for range reads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Dynamic.Resource(services).Namespace(s.Namespace).List(context.Background(), metav1.ListOptions{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent read failed: %v", err)
		}
	}
	took := time.Since(begun)
	// The burst goes at once and the rest at the steady rate.
	floor := time.Duration(float64(reads-kubeapi.DefaultLimits.Burst)/float64(kubeapi.DefaultLimits.QPS)*float64(time.Second)) * 9 / 10
	t.Logf("%d concurrent reads in %s (the limiter's floor is about %s)", reads, took, floor)
	if took < floor {
		t.Errorf("%d reads took %s, faster than the session's limiter allows (%s): it is not pacing them", reads, took, floor)
	}
}
