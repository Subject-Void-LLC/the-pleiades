// Package netconf: net.netconf.config's check, through the device's candidate
// datastore, strictly gated (owner decision 2026-11-14, with the conditions of
// the edge-case review of that decision). A check never writes running or
// startup, but it is not footprint-free: it opens a session, holds the
// candidate's lock for at most checkLockBound, stages the edit there, has the
// device validate it and discards it, proving the candidate as it was. Each
// condition below refuses with the reason the task is reported unchecked, and
// no condition is decided from a stored fact: the session's own hello and
// library decide.
package netconf

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// checkLockBound is the longest a check holds the candidate's lock: past it
// the session is closed, which releases the lock and, on a qualified server,
// the staged edit with it. checkThroughCandidate takes it as a parameter, so
// a test shortens its own call's bound and no test changes a package value.
const checkLockBound = 30 * time.Second

// statCandidateCheck is the stat a check records what it proved under.
const statCandidateCheck = "candidate_check"

// checkProved is what a check that passed proved, and what it did not.
const checkProved = "validated; commit not exercised"

// checkedTypes are the device types a check runs on: those whose NETCONF
// server is one of checkedServers. A Cisco IOS XE router (cisco_router) is
// not among them: its behavior on a dirty candidate, a killed session and a
// discard has not been measured.
var checkedTypes = []string{"generic_netconf"}

// checkedServer is a NETCONF server implementation that passed the
// conformance test a check leans on (pkg/netconf's TestAssurance_* suite): a
// candidate holding another session's changes is not locked, a session killed
// after its edit leaves the candidate as running, and discard-changes
// restores it. It is known by modules its own library lists.
type checkedServer struct {
	// name names the implementation.
	name string
	// modules are module names its library lists, each with its namespace
	// when that is fixed.
	modules map[string]string
}

// checkedServers are the implementations a check runs against.
var checkedServers = []checkedServer{{
	name: "Netopeer2 over sysrepo",
	modules: map[string]string{
		"sysrepo":                "http://www.sysrepo.org/yang/sysrepo",
		"netopeer-notifications": "",
	},
}}

// noCandidateCheck is why a call without candidate_check is not checked.
const noCandidateCheck = "net.netconf.config is checked only through the device's candidate datastore, and only when the task asks for it with candidate_check: true, since that check stages the edit on the device; without it only applying the document says what the device makes of it"

// configCheckCall is net.netconf.config's CheckCall: the conditions a call's
// parameters alone decide.
func configCheckCall(params map[string]any) error {
	req, err := readRequest(params)
	if err != nil {
		// The run refuses it as it is; the check is the run's to refuse.
		return nil
	}
	return checkableRequest(req)
}

// checkableRequest refuses, from the call alone, what no candidate check can
// check.
func checkableRequest(req configRequest) error {
	switch {
	case !req.candidateCheck:
		return collection.CannotCheck(noCandidateCheck)
	case req.target == netconf.Startup:
		return collection.CannotCheck("a check through the candidate datastore cannot check a write to startup, which the candidate does not stand in for")
	case secretref.Has(req.content):
		return collection.CannotCheck("content names a secret, which a check would place in the candidate datastore, where every session that reads it sees it until the discard")
	}
	return nil
}

// checkingDevices holds the devices a check is running on in this process,
// so at most one check per device runs at a time: a second would find the
// candidate locked by the first, for a reason unrelated to its change.
var checkingDevices sync.Map

// ConfigCheck is net.netconf.config's check: the edit staged in the device's
// locked candidate datastore, validated and discarded, with the candidate
// proven as it was. Changed is the candidate before against the candidate
// after the edit, read through the filter a real run compares running with.
func ConfigCheck(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if err := checkableRequest(req); err != nil {
		return collection.Result{}, err
	}
	if err := checkableHere(rc, device); err != nil {
		return collection.Result{}, err
	}
	release, err := oneCheckPerDevice(ctx, device)
	if err != nil {
		return collection.Result{}, err
	}
	defer release()

	session, err := openSession(ctx, rc, device, params, configFQCN, netconf.Candidate)
	if errors.Is(err, netconf.ErrDatastoreNotAdvertised) {
		// The session's own hello decides, and a device with no candidate is
		// reported unchecked, never failed.
		return collection.Result{}, collection.CannotCheck("this session's hello does not advertise :candidate, which a check through the candidate datastore needs")
	}
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()
	if err := checkableSession(session); err != nil {
		return collection.Result{}, err
	}
	// From here no RPC a check does not send can leave this process.
	session.LimitToCheck()
	warnings, err := recordDiscovery(rc, params, session.Discovery())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
		}
	}
	changed, err := checkThroughCandidate(ctx, session, req, checkLockBound)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if err := rc.SetStat(statCandidateCheck, checkProved); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: changed && req.commit}, nil
}

// checkableHere refuses a check anywhere but the command line, after an
// earlier checked task on the device predicted a change (or could not be
// checked, or failed), on a simulate-locked device, and on a device type no
// server of which passed the conformance test.
func checkableHere(rc sdk.RunbookContext, device inventory.InventoryItem) error {
	switch {
	case !sdk.StagedChecksAllowed(rc):
		return collection.CannotCheck("a check through the candidate datastore runs only from the command line (pleiades run or adhoc): on the Walk tier a check job must send no write, and the Runner does not yet hold a check to the candidate's RPCs")
	case sdk.EarlierChangePredicted(rc):
		return collection.CannotCheck("an earlier task of this check, on this device, predicted a change (or could not be checked, or failed), which a real run makes before this task, so the device as it is now is not what this task would meet")
	case device == nil:
		return collection.CannotCheck("the task has no target device")
	case device.State() == inventory.StateSimulateLocked:
		return collection.CannotCheck("the device is simulate-locked, and a check through the candidate datastore writes the candidate")
	}
	typed, ok := device.(interface{ DeviceType() string })
	if !ok || !slices.Contains(checkedTypes, typed.DeviceType()) {
		return collection.CannotCheck(fmt.Sprintf("a check through the candidate datastore runs only on %v, whose servers passed the conformance test it leans on", checkedTypes))
	}
	return nil
}

// checkableSession refuses a session whose own hello does not advertise
// :validate (one without :candidate never opened: openSession refused its
// target), or whose server is not one that passed the conformance test.
func checkableSession(session netconfSession) error {
	if !session.CanValidate() {
		return collection.CannotCheck("this session's hello does not advertise :validate, which a check through the candidate datastore needs")
	}
	if _, ok := qualifiedServer(session.Discovery()); !ok {
		names := make([]string, 0, len(checkedServers))
		for _, s := range checkedServers {
			names = append(names, s.name)
		}
		return collection.CannotCheck(fmt.Sprintf("the device's NETCONF server is not one that passed the conformance test a check through the candidate datastore leans on (%v)", names))
	}
	return nil
}

// qualifiedServer is the checked server d's library shows, if any.
func qualifiedServer(d netconf.Discovery) (string, bool) {
	for _, s := range checkedServers {
		found := 0
		for _, m := range d.Modules {
			if ns, want := s.modules[m.Name]; want && (ns == "" || ns == m.Namespace) {
				found++
			}
		}
		if found == len(s.modules) {
			return s.name, true
		}
	}
	return "", false
}
