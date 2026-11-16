// Package netconf_test: the assurance conformance suite, against the same real Netopeer2 server
// (over sysrepo, in the notconf image) the rest of this package's container
// tests use: the read net.netconf.config compares before and after an edit,
// and the three things a check through the candidate datastore leans on
// (owner decision 2026-11-14): a candidate holding another session's changes
// is not locked, a session killed after its edit leaves the candidate as
// running, and a discard restores the candidate. A server qualifies for the
// candidate check only by passing all three here.
package netconf_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// nacmNamespace is ietf-netconf-acm's namespace, the module these tests edit.
const nacmNamespace = "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"

// rawSession opens a session and returns it with the connection it rides, so
// a test can cut the connection the way a dropped client does.
func rawSession(t *testing.T, opts netconf.Options) (*netconf.Session, *remoteexec.Conn) {
	t.Helper()
	host, port := startNotconf(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port}, remoteexec.PasswordAuth(notconfUser, notconfPassword))
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatalf("opening the netconf subsystem: %v", err)
	}
	s, err := netconf.Open(ctx, sub, opts)
	if err != nil {
		t.Fatalf("opening the session: %v", err)
	}
	return s, conn
}

// nacmGroup is one NACM group holding one user, as edit-config content.
func nacmGroup(name, user string) string {
	return `<nacm xmlns="` + nacmNamespace + `"><groups><group><name>` + name + `</name><user-name>` + user + `</user-name></group></groups></nacm>`
}

// canonicalRead reads source through the filter content selects and returns
// it canonical.
func canonicalRead(t *testing.T, s *netconf.Session, source netconf.Datastore, content string) string {
	t.Helper()
	filter, err := netconf.EditFilter(content, false)
	if err != nil {
		t.Fatal(err)
	}
	els, err := s.ReadConfig(context.Background(), source, filter)
	if err != nil {
		t.Fatalf("ReadConfig(%s): %v", source, err)
	}
	return yangdoc.CanonicalElements(els)
}

// TestAssurance_TheEditFilterReadsWhatTheEditChanges: the filter derived from
// an edit selects the group the edit creates, a read before and after differ,
// and a second read after reads the same. The server reads in explicit
// with-defaults mode.
func TestAssurance_TheEditFilterReadsWhatTheEditChanges(t *testing.T) {
	s, _ := rawSession(t, netconf.Options{})
	ctx := context.Background()
	if got := s.WithDefaults(); got != netconf.WithDefaultsExplicit {
		t.Errorf("WithDefaults() = %q, want explicit (Netopeer2 advertises it)", got)
	}
	var names []string
	for _, m := range s.Discovery().Modules {
		if strings.Contains(m.Name, "netopeer") || strings.Contains(m.Name, "sysrepo") || strings.Contains(m.Name, "notification") {
			names = append(names, m.Name+" "+m.Namespace)
		}
	}
	t.Logf("server modules naming its implementation: %v", names)

	content := nacmGroup("assurance-read", "alice")
	t.Cleanup(func() {
		_ = s.EditConfig(context.Background(), netconf.EditConfigRequest{DefaultOperation: netconf.DefaultOperationNone,
			Config: `<nacm xmlns="` + nacmNamespace + `"><groups><group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="remove"><name>assurance-read</name></group></groups></nacm>`})
	})
	before := canonicalRead(t, s, netconf.Running, content)
	if strings.Contains(before, "assurance-read") {
		t.Fatalf("the group is there before the test made it: %s", before)
	}
	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: content}); err != nil {
		t.Fatal(err)
	}
	after := canonicalRead(t, s, netconf.Running, content)
	if after == before || !strings.Contains(after, "assurance-read") {
		t.Fatalf("the read after the edit does not show it:\nbefore %s\nafter  %s", before, after)
	}
	if again := canonicalRead(t, s, netconf.Running, content); again != after {
		t.Errorf("two reads of an unchanged datastore differ:\n%s\n%s", after, again)
	}
	// The filter is a superset by name: the edit's group, not the whole of
	// nacm (enable-nacm is outside it).
	if strings.Contains(after, "enable-nacm") {
		t.Errorf("the read selected more than the groups: %s", after)
	}
}

// TestAssurance_ACandidateHoldingAnotherSessionsChangesIsNotLocked is the
// first condition: RFC 6241 section 8.3.5.2 refuses the lock on a candidate
// that holds uncommitted changes, so a check that takes the lock first never
// discards another operator's staged work. The RFC names no error-tag for
// it; measured 2026-11-16, Netopeer2 answers operation-failed ("candidate
// datastore data have already been modified"), not lock-denied, so a check
// reads any refused lock as not checked.
func TestAssurance_ACandidateHoldingAnotherSessionsChangesIsNotLocked(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	other, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	ctx := context.Background()
	if err := other.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmGroup("assurance-dirty", "bob")}); err != nil {
		t.Fatalf("staging another session's change: %v", err)
	}
	t.Cleanup(func() { _ = other.DiscardChanges(context.Background()) })

	check, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	err := check.Lock(ctx)
	if err == nil {
		_ = check.Unlock(ctx)
		t.Fatal("the server locked a candidate holding another session's uncommitted change")
	}
	var refused netconf.RPCErrors
	if !errors.As(err, &refused) {
		t.Fatalf("the lock failed with %v, which is not the server's refusal", err)
	}
	t.Logf("the server refused the lock: %v", err)
}

// TestAssurance_ALockHeldByAnotherSessionNamesItsHolder: RFC 6241 section
// 7.5's lock-denied, whose error-info names the session holding the lock,
// which a check reports rather than taking the lock from it.
func TestAssurance_ALockHeldByAnotherSessionNamesItsHolder(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	ctx := context.Background()
	holder, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	if err := holder.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Unlock(context.Background()) })
	check, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	err := check.Lock(ctx)
	id, denied := netconf.LockHolder(err)
	if !denied || id != holder.SessionID() {
		t.Fatalf("Lock() = %v: LockHolder = %q, %v, want lock-denied naming session %s", err, id, denied, holder.SessionID())
	}
}

// TestAssurance_AKilledSessionLeavesTheCandidateAsRunning is the second
// condition: a session that locked the candidate, edited it and was cut off
// before discarding leaves nothing staged for the next commit by anyone.
func TestAssurance_AKilledSessionLeavesTheCandidateAsRunning(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	ctx := context.Background()
	content := nacmGroup("assurance-killed", "carol")
	running := canonicalRead(t, probe, netconf.Running, content)

	doomed, conn := rawSession(t, netconf.Options{Target: netconf.Candidate})
	if err := doomed.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := doomed.EditConfig(ctx, netconf.EditConfigRequest{Config: content}); err != nil {
		t.Fatal(err)
	}
	if staged := canonicalRead(t, doomed, netconf.Candidate, content); !strings.Contains(staged, "assurance-killed") {
		t.Fatalf("the edit is not in the candidate: %s", staged)
	}
	_ = conn.Close()

	// The server notices the dropped session in its own time; the lock
	// coming free is the sign it has.
	next, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := next.Lock(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the killed session's lock was never released: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { _ = next.DiscardChanges(context.Background()); _ = next.Unlock(context.Background()) })
	if candidate := canonicalRead(t, next, netconf.Candidate, content); candidate != running {
		t.Fatalf("the killed session's edit stayed in the candidate:\nrunning   %s\ncandidate %s", running, candidate)
	}
}

// TestAssurance_DiscardRestoresTheCandidate is the third condition, read
// through the same filter a check proves restoration with.
func TestAssurance_DiscardRestoresTheCandidate(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	ctx := context.Background()
	content := nacmGroup("assurance-discard", "dave")
	s, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	if err := s.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Unlock(context.Background()) })
	before := canonicalRead(t, s, netconf.Candidate, content)
	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: content}); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(ctx); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if edited := canonicalRead(t, s, netconf.Candidate, content); edited == before {
		t.Fatal("the edit did not reach the candidate")
	}
	if err := s.DiscardChanges(ctx); err != nil {
		t.Fatal(err)
	}
	if after := canonicalRead(t, s, netconf.Candidate, content); after != before {
		t.Fatalf("discard-changes left the candidate different:\nbefore %s\nafter  %s", before, after)
	}
}

// TestAssurance_ASessionLimitedToACheckSendsNothingElse: once limited, every
// RPC a check does not send is refused before it is written, and the session
// is still in step with the server afterwards (a refused RPC that had been
// half written would leave it waiting for a reply).
func TestAssurance_ASessionLimitedToACheckSendsNothingElse(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	ctx := context.Background()
	s, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	s.LimitToCheck()
	if err := s.Commit(ctx); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("Commit() on a check session = %v, want the refusal", err)
	}
	running, _ := rawSession(t, netconf.Options{})
	running.LimitToCheck()
	for name, call := range map[string]func() error{
		"edit running": func() error {
			return running.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmGroup("assurance-guard", "erin")})
		},
		"lock running":   func() error { return running.Lock(ctx) },
		"validate other": func() error { return running.Validate(ctx) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
			t.Errorf("%s on a check session = %v, want the refusal", name, err)
		}
	}
	if before := canonicalRead(t, probe, netconf.Running, nacmGroup("assurance-guard", "erin")); strings.Contains(before, "assurance-guard") {
		t.Fatal("a refused edit reached running")
	}
	// Still in step: a read and the candidate's own RPCs go through.
	if _, err := s.ReadConfig(ctx, netconf.Candidate, `<nacm xmlns="`+nacmNamespace+`"/>`); err != nil {
		t.Fatalf("a read after the refusals: %v", err)
	}
	if err := s.Lock(ctx); err != nil {
		t.Fatalf("the candidate's lock on a check session: %v", err)
	}
	if err := s.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	var rpcErrs netconf.RPCErrors
	if err := s.Commit(ctx); errors.As(err, &rpcErrs) {
		t.Errorf("the commit reached the server, which answered %v", err)
	}
}
