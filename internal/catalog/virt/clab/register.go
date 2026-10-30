// Registration of the virt.clab methods: each one's reference text comes from
// pkg/clabapi, its behavior from the table here, and the manifest is made from
// the two.
package clab

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clabapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// handler is one method's body. check is true for a check, which reads what a
// real run reads, decides what it would do, and changes nothing. It returns
// whether the run changed (or, in a check, would change) the server's state.
type handler func(ctx context.Context, s *session, a *args, check bool) (changed bool, err error)

// behavior is how one method runs and what it declares about itself.
type behavior struct {
	run handler
	// anonymous is set for a call that needs no login.
	anonymous bool
	// timeout is how long the run may take; shortTimeout when zero.
	timeout time.Duration
	// checks says the method has a check: run is called with check true.
	checks bool
	// noCheck is why it has none, for a method that cannot predict itself.
	noCheck string
	// rev is its reversibility declaration.
	rev collection.Reversibility
}

// readOnly is the declaration of a method that only reads.
func readOnly() collection.Reversibility {
	return collection.Reversibility{ReadOnly: true, Notes: "It only reads the server, so there is nothing to undo."}
}

// irreversible is the declaration of a method that changes the server and
// cannot say how to put it back.
func irreversible(why string) collection.Reversibility {
	return collection.Reversibility{Reversible: false, Notes: why}
}

// reversible is the declaration of a method that records an exact undo.
func reversible(notes string, inverses ...sdk.InverseSpec) collection.Reversibility {
	return collection.Reversibility{Reversible: true, Notes: notes, Inverses: inverses}
}

// inverse names one undo a method may record.
func inverse(fqcn string, partial bool, keys ...string) sdk.InverseSpec {
	return sdk.InverseSpec{FQCN: fqcn, Record: keys, MayBePartial: partial, MayKeepOnFailure: true}
}

// predicts is a method whose check reads what a run reads and decides.
func predicts(run handler, rev collection.Reversibility) behavior {
	return behavior{run: run, checks: true, rev: rev}
}

// reads is a method that only reads: its check is its run.
func reads(run handler) behavior {
	return behavior{run: run, checks: true, rev: readOnly()}
}

// acts is a method that changes the server and cannot predict how: it has no
// check, for the reason given.
func acts(run handler, noCheck string, rev collection.Reversibility) behavior {
	return behavior{run: run, noCheck: noCheck, rev: rev}
}

// unknownEffect is why a command cannot be checked.
const unknownEffect = "what it changes is known only once it has run"

// behaviors is every method's behavior, by name. A test holds it equal to the
// methods pkg/clabapi documents.
var behaviors = map[string]behavior{
	"virt.clab.lab.list":       reads(labList),
	"virt.clab.lab.info":       reads(labInfo),
	"virt.clab.lab.links":      reads(labRead("links", "links", "node")),
	"virt.clab.lab.interfaces": reads(labRead("interfaces", "interfaces", "node")),
	"virt.clab.lab.topology":   reads(labRead("topology/yaml", "topology", "")),
	"virt.clab.lab.validate":   reads(labValidate),
	"virt.clab.lab.deploy": {run: labDeploy, checks: true, timeout: longTimeout,
		rev: reversible("Its undo destroys the lab, and is recorded only when this task created it, before the deploy runs, since a deploy that fails partway leaves containers behind. A lab that was already deployed is left alone and records no undo.",
			inverse(fqcnDestroy, false, "lab", "cleanup"))},
	"virt.clab.lab.destroy": {run: labDestroy, checks: true, timeout: longTimeout,
		rev: irreversible("A destroyed lab's containers and their state are gone; deploying its topology again makes a new lab, not this one.")},
	"virt.clab.lab.redeploy": {run: labRedeploy, timeout: longTimeout,
		noCheck: "it recreates every node, and whether the new lab comes up is known only once it has run",
		rev:     irreversible("Every node is recreated, so what was inside the containers is gone.")},
	"virt.clab.lab.save": acts(labSave, "saving overwrites configurations the server does not show beforehand",
		irreversible("It overwrites each node's saved configuration, which this method does not read first.")),
	"virt.clab.lab.start": {run: labState("start"), checks: true, timeout: longTimeout,
		rev: irreversible("A lab-wide start cannot say which nodes were in which state before; virt.clab.node.start records an exact undo for one node.")},
	"virt.clab.lab.stop": {run: labState("stop"), checks: true, timeout: longTimeout,
		rev: irreversible("A lab-wide stop cannot say which nodes were in which state before; virt.clab.node.stop records an exact undo for one node.")},
	"virt.clab.lab.restart": {run: labState("restart"), timeout: longTimeout,
		noCheck: "a restart always changes the lab, and how the nodes come back is known only once it has run",
		rev:     irreversible("A restart cannot be put back.")},
	"virt.clab.lab.apply": {run: labApply, checks: true, timeout: longTimeout,
		rev: irreversible("Nodes it recreated are new containers, and a link it removed is not read first.")},

	"virt.clab.node.start": predicts(nodeState("start"), reversible("Its undo is virt.clab.node.stop, recorded for a node that was stopped.", inverse("virt.clab.node.stop", false, "lab", "node"))),
	"virt.clab.node.stop": predicts(nodeState("stop"), reversible("Its undo is virt.clab.node.start, recorded for a node that was running, and partial for one that was paused, which a start does not restore.",
		inverse("virt.clab.node.start", true, "lab", "node"))),
	"virt.clab.node.pause":   predicts(nodeState("pause"), reversible("Its undo is virt.clab.node.unpause.", inverse("virt.clab.node.unpause", false, "lab", "node"))),
	"virt.clab.node.unpause": predicts(nodeState("unpause"), reversible("Its undo is virt.clab.node.pause.", inverse("virt.clab.node.pause", false, "lab", "node"))),
	"virt.clab.node.restart": acts(nodeRestart, "a restart always changes the node, and how it comes back is known only once it has run", irreversible("A restart cannot be put back.")),
	"virt.clab.node.exec": {run: nodeExec, noCheck: "a command run in a lab node can change anything the node can reach, and " + unknownEffect,
		rev: irreversible("A command's effect inside a lab node is unknown to this platform, as exec.command's is on a host.")},
	"virt.clab.node.logs":  reads(nodeLogs),
	"virt.clab.node.ports": reads(nodePorts),
	"virt.clab.node.ssh_access": acts(nodeSSHAccess, "it opens a session on the server, which a check must not",
		reversible("Its undo closes the session it opened, by its port.", inverse("virt.clab.ssh_session.terminate", false, "port"))),
	"virt.clab.node.disable_tx_offload": acts(nodeDisableTxOffload, "the offload setting is not read first, so what it changes is known only once it has run",
		irreversible("The offload setting it found is not read, so it cannot be put back.")),

	"virt.clab.netem.show": reads(netemShowHandler),
	"virt.clab.netem.set": predicts(netemSet, reversible("Its undo puts back what it found: the same settings, or a reset when the interface had none; partial when corruption was set, which the server does not show.",
		inverse("virt.clab.netem.set", true, "lab", "node", "interface", "delay", "jitter", "loss", "rate"), inverse("virt.clab.netem.reset", true, "lab", "node", "interface"))),
	"virt.clab.netem.reset": predicts(netemReset, reversible("Its undo is virt.clab.netem.set with the settings it found.",
		inverse("virt.clab.netem.set", false, "lab", "node", "interface", "delay", "jitter", "loss", "rate"))),

	"virt.clab.veth.create": acts(vethCreate, "whether the endpoints can be joined is known only once the pair is made",
		irreversible("The server has no call that removes a vEth pair.")),
	"virt.clab.vxlan.create": acts(vxlanCreate, "whether the tunnel comes up is known only once it is made",
		reversible("Its undo removes the VxLAN interfaces named vx-*, which is partial when others carry that prefix.", inverse("virt.clab.vxlan.delete", true, "prefix"))),
	"virt.clab.vxlan.delete": acts(vxlanDelete, "which interfaces carry the prefix is not read first",
		irreversible("The tunnels' settings are not read first, so they cannot be put back.")),

	"virt.clab.image.list": reads(imageList),
	"virt.clab.image.pull": predicts(imagePull, reversible("Its undo removes the image, recorded when this task pulled it.", inverse("virt.clab.image.remove", false, "image"))),
	"virt.clab.image.remove": predicts(imageRemove, reversible("Its undo pulls the same reference, which is partial: the tag comes back, though the layers may be a newer build.",
		inverse("virt.clab.image.pull", true, "image"))),

	"virt.clab.server.version": reads(serverRead("/api/v1/version", false, func(raw map[string]any) map[string]any {
		return map[string]any{"version": text(raw, "versionInfo")}
	})),
	"virt.clab.server.version_check": reads(serverRead("/api/v1/version/check", false, func(raw map[string]any) map[string]any {
		return map[string]any{"result": text(raw, "checkResult")}
	})),
	"virt.clab.server.health": {run: serverRead("/health", true, func(raw map[string]any) map[string]any {
		return map[string]any{"status": text(raw, "status"), "uptime": text(raw, "uptime"), "api_version": text(raw, "version")}
	}), anonymous: true, checks: true, rev: readOnly()},
	"virt.clab.server.metrics": reads(serverRead("/api/v1/health/metrics", false, func(raw map[string]any) map[string]any {
		return map[string]any{"metrics": raw["metrics"]}
	})),

	"virt.clab.capture.packetflix":    reads(capturePacketflix),
	"virt.clab.topology.generate":     reads(topologyGenerate),
	"virt.clab.ssh_session.list":      reads(sshSessionList),
	"virt.clab.ssh_session.terminate": predicts(sshSessionTerminate, irreversible("A closed session is opened again by virt.clab.node.ssh_access, which makes a new one on another port.")),
	"virt.clab.edgeshark.status":      reads(edgesharkStatus),
	"virt.clab.edgeshark.install": {run: edgesharkChange("install"), checks: true, timeout: longTimeout,
		rev: reversible("Its undo is virt.clab.edgeshark.uninstall, recorded when this task installed it.", inverse("virt.clab.edgeshark.uninstall", false))},
	"virt.clab.edgeshark.uninstall": {run: edgesharkChange("uninstall"), checks: true, timeout: longTimeout,
		rev: reversible("Its undo is virt.clab.edgeshark.install, recorded when this task removed it.", inverse("virt.clab.edgeshark.install", false))},
}

func init() {
	for _, spec := range clabapi.Specs() {
		b, ok := behaviors[spec.Name]
		if !ok {
			panic(fmt.Sprintf("virt.clab: %s is documented and has no behavior", spec.Name))
		}
		register(spec, b)
	}
}

// register makes one method from its text and its behavior.
func register(spec clabapi.Spec, b behavior) {
	m := collection.Manifest{
		SupportedTransports:  []string{capability.TransportHTTPS},
		RequiredCapabilities: []capability.Name{capability.NameClabAPI},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.3.0",
		Status:               collection.StatusImplemented,
		Reversibility:        b.rev,
		SupportsCheck:        b.checks,
		NoCheckReason:        b.noCheck,
		Doc:                  spec.Doc,
	}
	d := collection.Descriptor{
		Name:     spec.Name,
		Manifest: m,
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			return execute(ctx, rc, device, params, spec, b, false)
		},
	}
	if b.checks {
		d.Check = func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			return execute(ctx, rc, device, params, spec, b, true)
		}
	}
	collection.MustRegister(d)
}

// execute is every method's run and check: the parameters held to what the
// method declares, a session to the server, the handler under a timeout.
func execute(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, spec clabapi.Spec, b behavior, check bool) (collection.Result, error) {
	a, err := newArgs(spec.Name, spec.Doc.Params, params)
	if err != nil {
		return collection.Result{}, err
	}
	s, err := open(rc, device, spec.Name, a, b.anonymous)
	if err != nil {
		return collection.Result{}, err
	}
	timeout := b.timeout
	if timeout == 0 {
		timeout = shortTimeout
	}
	if n, ok := a.integer("timeout"); ok {
		if n <= 0 {
			return collection.Result{}, fmt.Errorf("%s: timeout must be positive", spec.Name)
		}
		timeout = time.Duration(n) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	changed, err := b.run(ctx, s, a, check)
	// A handler that acted before reading a parameter would have been caught
	// by session.call; one that never called the server is held to it here.
	if err == nil && a.err != nil {
		err = fmt.Errorf("%s: %w", spec.Name, a.err)
	}
	return collection.Result{Changed: changed}, err
}
