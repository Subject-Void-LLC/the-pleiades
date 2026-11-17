// Package capture implements virt.cml.capture.start and
// virt.cml.capture.stop: a packet capture on one link of a CML lab, taken by
// the controller while traffic crosses the link, and written into the
// project as a pcap file, so what crossed the wire (ESP or cleartext, a
// routing protocol's hellos) is proven from packets rather than counters.
package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods.
const (
	startFQCN = "virt.cml.capture.start"
	stopFQCN  = "virt.cml.capture.stop"
)

// The parameters.
const (
	paramLab     = "lab"
	paramPackets = "packets"
	paramSeconds = "seconds"
	paramFilter  = "filter"
	paramDest    = "dest"
	paramTimeout = "timeout"
)

// defaultStopTimeout is how many seconds a stop waits, by default, for the
// controller to finish writing the file: a real CML 2.7 controller took
// about four minutes for 9 MB (2026-11-17), so a file at the 16 MiB bound
// takes about eight.
const defaultStopTimeout = 900

// awaitPoll is how often a stop reads the file's length while it waits. It
// is a variable only so this package's tests can wait in milliseconds
// (export_test.go); nothing else sets it.
var awaitPoll = cml.DefaultAwaitPoll

// manifest is what both methods share, but for reversibility, check
// support and documentation.
func manifest(rev collection.Reversibility, doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{"https"},
		RequiredCapabilities: []capability.Name{capability.Name("CMLAPICapable")},
		ExecutionContext: collection.ExecutionContext{
			RequiresElevation: false,
			Site:              collection.SiteController,
			Device:            collection.DeviceRequired,
		},
		PlatformTargets: nil,
		EngineVersion:   ">=0.2.0",
		Status:          collection.StatusImplemented,
		Reversibility:   rev,
		SupportsCheck:   true,
		Doc:             doc,
	}
}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: startFQCN,
		Manifest: manifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that started a capture emits virt.cml.capture.stop of the same link, which ends it and writes nothing.",
			Inverses: []sdk.InverseSpec{
				{FQCN: stopFQCN, Record: []string{paramLab, cml.ParamLink}, MayKeepOnFailure: true},
			},
		}, StartDoc()),
		Invoke: Start,
		Check:  CheckStart,
	})
	collection.MustRegister(collection.Descriptor{
		Name: stopFQCN,
		Manifest: manifest(collection.Reversibility{
			Reversible: false,
			Notes:      "A stopped capture cannot be resumed, and the file it wrote is the record of what crossed the link, which no undo should take away.",
		}, StopDoc()),
		Invoke: Stop,
		Check:  CheckStop,
	})
}

// linkParams are the parameters naming the lab and the link.
func linkParams() []collection.Param {
	return []collection.Param{
		{Name: paramLab, Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
		{Name: cml.ParamLink, Type: "string", Description: "The link's id."},
		{Name: cml.ParamA, Type: "string", Description: "One node, by label or id, with a_interface, b and b_interface, naming the link by its ends."},
		{Name: cml.ParamAInterface, Type: "string", Description: "Its interface, by label."},
		{Name: cml.ParamB, Type: "string", Description: "The other node, by label or id."},
		{Name: cml.ParamBInterface, Type: "string", Description: "Its interface, by label."},
	}
}

// StartDoc is virt.cml.capture.start's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func StartDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Starts a packet capture on a link of a CML lab.",
		Description: "Starts the controller capturing every packet that crosses the link named by link, or the one between a's a_interface and b's b_interface, until packets have been taken or seconds have passed (at least one is required; CML allows 1 to 1000000 packets and 1 to 86400 seconds), optionally only those a Berkeley packet filter (tcpdump's syntax) admits. The capture runs on the controller while the run goes on, so start it, send the traffic to prove (net.ios.ping), then stop it with virt.cml.capture.stop, which writes the file into the project for net.pcap.summary or another analyzer to read. A link that is already capturing is refused: stop that capture first. Its undo is virt.cml.capture.stop of the same link, which writes nothing. The link must be started (its lab started); the task's target is a cml_controller device. A check reads the link and its capture and changes nothing.",
		Params: append(linkParams(),
			collection.Param{Name: paramPackets, Type: "int", Description: "Stop after this many packets."},
			collection.Param{Name: paramSeconds, Type: "int", Description: "Stop after this many seconds."},
			collection.Param{Name: paramFilter, Type: "string", Description: "A Berkeley packet filter, at most 128 characters, such as \"esp or udp port 500 or udp port 4500 or icmp\"."},
		),
		Returns: []collection.ReturnField{
			{Name: "id", Type: "string", Returned: "always", Description: "The link's id."},
		},
		Examples: []collection.Example{
			{Name: "Prove a WAN link carries only encrypted traffic", RunbookYAML: "- name: Capture CORE1's WAN link\n  virt.cml.capture.start:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n    packets: 2000\n    seconds: 60\n- name: Ping across the tunnel from a branch\n  net.ios.ping:\n    target: BR1\n    dest: 10.0.0.1\n    source: 10.1.1.1\n    count: 50\n- name: Stop it and keep the file\n  virt.cml.capture.stop:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n    dest: captures/core1-wan.pcap\n- name: Count what crossed\n  net.pcap.summary:\n    src: captures/core1-wan.pcap\n"},
		},
		SeeAlso: []string{"virt.cml.capture.stop", "net.pcap.summary", "virt.cml.link.stop"},
	}
}

// StopDoc is virt.cml.capture.stop's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func StopDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Stops a packet capture on a link of a CML lab and writes its file into the project.",
		Description: "Stops the capture on the link named by link, or the one between a's a_interface and b's b_interface, and, with dest, fetches its pcap file from the controller and writes it into the project at dest (a path under the project directory, never .pleiades; up to 16 MiB, so bound a long capture with packets or seconds; a file the controller says is larger is refused with its size before it is read). The controller writes the file behind the capture (CML 2.7 took about four minutes to write 25,593 packets, 9 MB, after the stop, and served it short until then), so the stop waits, up to timeout, until the file is whole: its length held still across one five-second poll, its last record complete, and at least as many packets in it as the capture reported just before this task stopped it. For a capture that had already stopped (at its own packets or seconds bound) that count is not known, and the length must hold still across three polls in a row (fifteen seconds) instead. Each poll reads the length from the headers of a request for the file whose download is then cut off (CML refuses HEAD and serves no byte ranges), and the file is downloaded whole only once its length holds, not on every poll. A capture that stopped at its own bound reports itself running until its file is whole (measured: 15,000 packets taken in 20 seconds, the file written for another 63 seconds at about 70 KB a second without a pause, and the capture reported stopped at the moment it was whole). A download the controller cuts off partway (it resets the HTTP/2 stream when the file grows during one) is asked for again, from where it stopped when the controller serves byte ranges and from the start otherwise, four tries in all; a download is bounded by a minute without a byte, not by its length. A capture that already stopped at its bound is not stopped again, and its file is still fetched. The file is the packets as they crossed the wire: any cleartext that crossed is in it, so keep it with the project's other private files. Its result names the file, its size and SHA-256. The task's target is a cml_controller device; it writes only on the command line (pleiades run and adhoc), which holds the project. A check reads the capture and reports what it would do, writing nothing.",
		Params: append(linkParams(),
			collection.Param{Name: paramDest, Type: "string", Description: "Where in the project to write the capture file, a name ending in .pcap or .pcapng, such as captures/core1-wan.pcap. Without it the capture is stopped and its file left on the controller.", Format: collection.ParamFormatProjectPath},
			collection.Param{Name: paramTimeout, Type: "int", Default: "900", Description: "How many seconds to wait for the controller to finish writing the file, 1 to 86400; it also bounds a check's one fetch. A file still being written then is refused, naming how far it had got and how the last fetch ended, and nothing is written."},
		),
		Returns: []collection.ReturnField{
			{Name: "id", Type: "string", Returned: "always", Description: "The link's id."},
			{Name: "packets", Type: "int", Returned: "when a capture was running", Description: "How many packets the capture had taken when it was stopped."},
			{Name: "dest", Type: "string", Returned: "when dest is set", Description: "The file written."},
			{Name: "bytes", Type: "int", Returned: "when dest is set", Description: "Its size."},
			{Name: "sha256", Type: "string", Returned: "when dest is set", Description: "Its SHA-256, in hex."},
		},
		Examples: []collection.Example{{Name: "Keep a capture for net.pcap.summary", RunbookYAML: "- name: Stop the capture and keep the file\n  virt.cml.capture.stop:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n    dest: captures/core1-wan.pcap\n"}},
		SeeAlso:  []string{"virt.cml.capture.start", "net.pcap.summary"},
	}
}

// Start implements "virt.cml.capture.start".
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStart is "virt.cml.capture.start"'s check.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeCheck)
}

// readLink reads the lab and link a task names and finds them.
func readLink(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (*cml.Client, cml.Lab, cml.Link, error) {
	labRef, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return nil, cml.Lab{}, cml.Link{}, err
	}
	ref, err := cml.ReadLinkRef(params)
	if err != nil {
		return nil, cml.Lab{}, cml.Link{}, err
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return nil, cml.Lab{}, cml.Link{}, err
	}
	lab, link, err := client.LabAndLink(ctx, labRef, ref)
	if err != nil {
		return nil, cml.Lab{}, cml.Link{}, cml.Unchecked(mode, err)
	}
	if err := rc.SetStat("id", link.ID); err != nil {
		return nil, cml.Lab{}, cml.Link{}, err
	}
	return client, lab, link, nil
}

// runStart starts a capture on the link a task names.
func runStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", startFQCN, err)
	}
	packets, _, err := sdk.IntParam(params, paramPackets)
	if err != nil {
		return fail(err)
	}
	seconds, _, err := sdk.IntParam(params, paramSeconds)
	if err != nil {
		return fail(err)
	}
	spec := cml.CaptureSpec{Packets: packets, Seconds: seconds, Filter: sdk.StringParam(params, paramFilter)}
	if err := spec.Validate(); err != nil {
		return fail(err)
	}
	client, lab, link, err := readLink(ctx, rc, device, params, mode)
	if err != nil {
		return fail(err)
	}
	if link.State != cml.StateStarted {
		return fail(fmt.Errorf("link %s is %s, and a capture needs a started link (start the lab, or virt.cml.link.start)", link.ID, link.State))
	}
	state, err := client.CaptureStatus(ctx, lab.ID, link.ID)
	if err != nil {
		return fail(err)
	}
	if state.Running {
		return fail(fmt.Errorf("link %s is already capturing (%d packets so far, since %s): stop it with virt.cml.capture.stop first", link.ID, state.Packets, state.Started))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if err := client.StartCapture(ctx, lab.ID, link.ID, spec); err != nil {
		return fail(err)
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          stopFQCN,
		Params:        map[string]any{paramLab: lab.ID, cml.ParamLink: link.ID},
		Description:   fmt.Sprintf("Stop the capture this task started on link %s, writing nothing.", link.ID),
		KeepOnFailure: true,
	}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

// Stop implements "virt.cml.capture.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStop is "virt.cml.capture.stop"'s check.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeCheck)
}

// runStop stops the capture on the link a task names and, with dest, writes
// its file into the project.
func runStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", stopFQCN, err)
	}
	dest := sdk.StringParam(params, paramDest)
	timeout, set, err := sdk.IntParam(params, paramTimeout)
	switch {
	case err != nil:
		return fail(err)
	case !set:
		timeout = defaultStopTimeout
	case timeout < 1 || timeout > cml.MaxCaptureSeconds:
		return fail(fmt.Errorf("%s must be 1 to %d seconds", paramTimeout, cml.MaxCaptureSeconds))
	}
	// A capture file, never another of the project's files: a runbook a
	// later rollback reads as it ran must not be replaced by a pcap.
	if dest != "" && !strings.HasSuffix(dest, ".pcap") && !strings.HasSuffix(dest, ".pcapng") {
		return fail(fmt.Errorf("%s %q must name a .pcap or .pcapng file", paramDest, dest))
	}
	var writer sdk.ProjectFileWriter
	if dest != "" {
		w, ok := rc.(sdk.ProjectFileWriter)
		if !ok {
			return fail(sdk.ErrNoProjectFiles)
		}
		writer = w
	}
	client, lab, link, err := readLink(ctx, rc, device, params, mode)
	if err != nil {
		return fail(err)
	}
	state, err := client.CaptureStatus(ctx, lab.ID, link.ID)
	if err != nil {
		return fail(err)
	}
	changed := false
	if state.Running {
		if err := rc.SetStat("packets", state.Packets); err != nil {
			return collection.Result{}, err
		}
		changed = true
		if mode != collection.ModeCheck {
			if err := client.StopCapture(ctx, lab.ID, link.ID); err != nil {
				return fail(err)
			}
		}
	}
	if dest == "" {
		return collection.Result{Changed: changed}, nil
	}
	key, err := client.CaptureKey(ctx, lab.ID, link.ID)
	var status *cml.StatusError
	if errors.As(err, &status) && status.Status == 404 {
		return fail(fmt.Errorf("link %s has no capture to write: start one with virt.cml.capture.start", link.ID))
	}
	if err != nil {
		return fail(err)
	}
	// The timeout bounds a check's one fetch as well as a run's wait: a
	// fetch is otherwise bounded only by idle time.
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	var data []byte
	if mode == collection.ModeCheck {
		// A check stops nothing, so the file is read as it stands.
		data, err = client.FetchPCAP(waitCtx, key, sdk.MaxProjectFileWrite)
	} else {
		// The controller writes the file behind the capture: wait until
		// it holds every packet the capture took (state.Packets, 0 when
		// the capture had already stopped and the count is not known).
		data, err = client.AwaitPCAP(waitCtx, key, sdk.MaxProjectFileWrite, cml.Await{Packets: state.Packets, Poll: awaitPoll})
	}
	if err != nil {
		return fail(err)
	}
	wrote, err := writer.WriteProjectFile(dest, data, 0o600, mode != collection.ModeCheck)
	if err != nil {
		return fail(err)
	}
	sum := sha256.Sum256(data)
	for k, v := range map[string]any{"dest": dest, "bytes": len(data), "sha256": hex.EncodeToString(sum[:])} {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: changed || wrote}, nil
}
