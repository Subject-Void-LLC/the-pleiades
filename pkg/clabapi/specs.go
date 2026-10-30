// Package clabapi: the reference text of every virt.clab method, kept beside the
// client so the catalog and the generated documentation read one definition.
package clabapi

import (
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Spec is one virt.clab method's reference text: what its page, `pleiades
// doc` and the module catalog say. The behavior is in internal/catalog/virt/clab;
// the text is here so the catalog's data (internal/forge/catalogdata) and the
// registered manifest are built from one definition and cannot disagree.
type Spec struct {
	// Name is the method's FQCN.
	Name string
	// Doc is its reference documentation.
	Doc collection.Doc
}

// target is every method's closing sentence about what it is aimed at.
const target = " The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled."

// Parameters shared by many methods.
var (
	pLab  = collection.Param{Name: "lab", Type: "string", Required: true, Description: "The lab's name, as containerlab deployed it."}
	pNode = collection.Param{Name: "node", Type: "string", Required: true, Description: "The node's name in the lab (n1), or its container's name (clab-<lab>-n1)."}
	pNone = collection.Param{Name: "node_filter", Type: "string", Description: "Only this node, by its name in the lab or its container's name. Every node when empty."}
)

// Specs returns every method's reference text, in the order its page lists
// them. The slice is fresh on each call.
func Specs() []Spec {
	specs := []Spec{}
	add := func(name string, doc collection.Doc) { specs = append(specs, Spec{Name: name, Doc: doc}) }
	read := func(summary, desc string, params []collection.Param, returns []collection.ReturnField, ex, also string) collection.Doc {
		return collection.Doc{Summary: summary, Description: desc + " It only reads, so a check is the same call." + target, Params: params, Returns: returns,
			Examples: []collection.Example{{Name: summary, RunbookYAML: ex}}, SeeAlso: split(also)}
	}
	ret := func(name, typ, when, desc string) collection.ReturnField {
		return collection.ReturnField{Name: name, Type: typ, Returned: when, Description: desc}
	}

	// ---- labs ----
	add("virt.clab.lab.list", read("Lists the labs the containerlab server has deployed.",
		"Returns every deployed lab's name and its nodes as the server's inspect reports them.",
		nil, []collection.ReturnField{ret("labs", "list of string", "always", "The deployed labs' names, sorted."), ret("nodes", "dict", "always", "Each lab's nodes, by lab name: name, node, kind, image, state, status and management addresses.")},
		"- name: What is deployed\n  virt.clab.lab.list:\n    target: clab-api\n  register: labs\n", "virt.clab.lab.info"))
	add("virt.clab.lab.info", read("Inspects one lab: whether it exists and its nodes.",
		"A lab that is not deployed is not a failure: exists is false and no nodes are reported, so a later task can decide what to do.",
		[]collection.Param{pLab},
		[]collection.ReturnField{ret("exists", "bool", "always", "Whether the lab is deployed."), ret("nodes", "list of dict", "when it exists", "Each node: name (container), node, kind, image, state (running, exited or paused), status, ipv4_address, ipv6_address, container_id, and ports (each host_ip, host_port, port and protocol it publishes)."), ret("node_count", "int", "when it exists", "How many nodes.")},
		"- name: Is the lab up\n  virt.clab.lab.info:\n    target: clab-api\n    lab: nclab\n  register: lab\n", "virt.clab.lab.list, virt.clab.lab.deploy"))
	add("virt.clab.lab.links", read("Lists a lab's links with each end's interface, address and state.", "Narrow it to the links one node has with node.",
		[]collection.Param{pLab, {Name: "node", Type: "string", Description: "Only the links this node has."}},
		[]collection.ReturnField{ret("links", "list of dict", "always", "Each link: type, and its two ends (a and b) with node, container, interface, mac, mtu and state.")},
		"- name: The lab's links\n  virt.clab.lab.links:\n    target: clab-api\n    lab: nclab\n", "virt.clab.lab.interfaces"))
	add("virt.clab.lab.interfaces", read("Lists the interfaces of a lab's nodes.", "Narrow it to one node with node.",
		[]collection.Param{pLab, {Name: "node", Type: "string", Description: "Only this node's interfaces."}},
		[]collection.ReturnField{ret("interfaces", "list of dict", "always", "Each node: name, and its interfaces with name, alias, mac, ifindex, mtu, type and state.")},
		"- name: r1's interfaces\n  virt.clab.lab.interfaces:\n    target: clab-api\n    lab: nclab\n    node: r1\n", "virt.clab.lab.links"))
	add("virt.clab.lab.topology", read("Reads a deployed lab's topology as YAML.", "The text is the topology file the server holds for the lab.",
		[]collection.Param{pLab}, []collection.ReturnField{ret("topology", "string", "always", "The topology file's YAML text.")},
		"- name: Read the topology\n  virt.clab.lab.topology:\n    target: clab-api\n    lab: nclab\n  register: topo\n", "virt.clab.lab.validate, virt.clab.lab.apply"))
	add("virt.clab.lab.validate", read("Validates a lab's on-disk topology without deploying it.", "Asks containerlab to parse the topology file the server holds for the lab and reports whether it is valid. path names another file on the server.",
		[]collection.Param{pLab, {Name: "path", Type: "string", Description: "A topology file on the server, when it is not the lab's own."}},
		[]collection.ReturnField{ret("valid", "bool", "always", "Whether the topology is valid."), ret("nodes", "int", "always", "How many nodes."), ret("links", "int", "always", "How many links."), ret("error", "string", "when invalid", "containerlab's validation error.")},
		"- name: Validate it first\n  virt.clab.lab.validate:\n    target: clab-api\n    lab: nclab\n", "virt.clab.lab.apply"))

	deployParams := []collection.Param{
		pLab,
		{Name: "topology", Type: "dict", Description: "The topology as a mapping, as containerlab's topology file is (name, topology: nodes, links). The three topology, topology_file and source_url are alternatives: give only one."},
		{Name: "topology_file", Type: "string", Description: "A containerlab topology YAML file in the project directory, read by the command line; refused on a Walk-tier Runner, where topology is the way. The three topology, topology_file and source_url are alternatives: give only one."},
		{Name: "source_url", Type: "string", Description: "A URL of a Git repository holding the topology, which the server fetches. The three topology, topology_file and source_url are alternatives: give only one."},
		{Name: "max_workers", Type: "int", Description: "How many nodes the server starts at once; the server's own default when unset."},
		{Name: "network", Type: "string", Description: "The management network's name, when it is not clab."},
		{Name: "ipv4_subnet", Type: "string", Description: "The management network's IPv4 subnet."},
		{Name: "ipv6_subnet", Type: "string", Description: "The management network's IPv6 subnet."},
		{Name: "skip_post_deploy", Type: "bool", Default: "false", Description: "Skip the nodes' post-deploy steps."},
	}
	add("virt.clab.lab.deploy", collection.Doc{
		Summary:     "Deploys a lab from a topology, unless it is already deployed.",
		Description: "Deploys the topology (given as a mapping, a project file or a repository URL) through the server. A lab named lab that is already deployed is left as it is and reported unchanged, so a runbook that deploys first can run again; reconfigure true deploys it over the running one. The topology's own name must be lab: a topology that names another lab would deploy that one and leave this one absent. Its undo destroys the lab, and is recorded only when this task created it." + target,
		Params:      append(append([]collection.Param{}, deployParams...), collection.Param{Name: "reconfigure", Type: "bool", Default: "false", Description: "Deploy over a lab that is already deployed, recreating it. Records no undo."}),
		Returns:     []collection.ReturnField{ret("exists", "bool", "always", "Whether the lab is deployed now."), ret("nodes", "list of dict", "always", "Its nodes, as lab.info reports them."), ret("created", "bool", "always", "Whether this task deployed it.")},
		Examples:    []collection.Example{{Name: "Deploy a two-node lab", RunbookYAML: "- name: The lab is up\n  virt.clab.lab.deploy:\n    target: clab-api\n    lab: nclab\n    topology_file: lab/nclab.clab.yml\n"}},
		SeeAlso:     []string{"virt.clab.lab.destroy", "virt.clab.lab.redeploy", "virt.clab.lab.info"}})
	add("virt.clab.lab.destroy", collection.Doc{
		Summary:     "Destroys a lab, unless it is not deployed.",
		Description: "Removes the lab's containers and network through the server. A lab that is not deployed is not a failure and is reported unchanged. It cannot be undone: the containers' state is gone, and an undo that deployed the topology again would be a new lab, not this one." + target,
		Params: []collection.Param{pLab,
			{Name: "cleanup", Type: "bool", Default: "false", Description: "Also remove the lab's directory and configuration state."},
			{Name: "purge_lab_dir", Type: "bool", Default: "false", Description: "Remove the lab's directory on the server."},
			{Name: "graceful", Type: "bool", Default: "false", Description: "Stop the nodes gracefully before removing them."},
			{Name: "keep_mgmt_net", Type: "bool", Default: "false", Description: "Keep the management network."}},
		Returns:  []collection.ReturnField{ret("exists", "bool", "always", "Whether the lab is deployed now (false after a destroy).")},
		Examples: []collection.Example{{Name: "Tear a lab down", RunbookYAML: "- name: The lab is gone\n  virt.clab.lab.destroy:\n    target: clab-api\n    lab: nclab\n    cleanup: true\n"}},
		SeeAlso:  []string{"virt.clab.lab.deploy"}})
	add("virt.clab.lab.redeploy", collection.Doc{
		Summary:     "Destroys and deploys a lab again from a topology.",
		Description: "Replaces the lab with a new one from the topology, through the server. Every node is recreated, so what was inside the containers is lost; it cannot be undone. Use virt.clab.lab.apply to change a running lab by the difference instead." + target,
		Params: append(append([]collection.Param{}, deployParams...),
			collection.Param{Name: "cleanup", Type: "bool", Default: "false", Description: "Also remove the old lab's directory and configuration state."},
			collection.Param{Name: "graceful", Type: "bool", Default: "false", Description: "Stop the old nodes gracefully."}),
		Returns:  []collection.ReturnField{ret("nodes", "list of dict", "always", "The new lab's nodes, as lab.info reports them.")},
		Examples: []collection.Example{{Name: "Start over", RunbookYAML: "- name: Fresh lab\n  virt.clab.lab.redeploy:\n    target: clab-api\n    lab: nclab\n    topology_file: lab/nclab.clab.yml\n"}},
		SeeAlso:  []string{"virt.clab.lab.deploy", "virt.clab.lab.apply"}})
	add("virt.clab.lab.save", collection.Doc{
		Summary:     "Saves each node's running configuration to its startup configuration.",
		Description: "Runs containerlab's save for the lab, so a node that reads a startup configuration comes back with what it runs now. It overwrites those saved configurations, which this method does not read first, so it cannot be undone." + target,
		Params:      []collection.Param{pLab, pNone},
		Returns:     []collection.ReturnField{ret("message", "string", "always", "The server's message."), ret("output", "string", "always", "containerlab's own output.")},
		Examples:    []collection.Example{{Name: "Keep what the lab runs", RunbookYAML: "- name: Save the configurations\n  virt.clab.lab.save:\n    target: clab-api\n    lab: nclab\n"}},
		SeeAlso:     []string{"virt.clab.lab.deploy"}})
	for _, op := range []struct{ verb, past, want string }{{"start", "started", "running"}, {"stop", "stopped", "exited"}, {"restart", "restarted", ""}} {
		desc := "Starts every stopped node of the lab through the server, and leaves the running ones. A lab whose nodes all run already is reported unchanged."
		switch op.verb {
		case "stop":
			desc = "Stops every running node of the lab through the server. A lab whose nodes are all stopped already is reported unchanged."
		case "restart":
			desc = "Restarts every node of the lab through the server. It always changes the lab."
		}
		add("virt.clab.lab."+op.verb, collection.Doc{
			Summary:     "Makes a lab's nodes " + op.past + ".",
			Description: desc + " A lab-wide " + op.verb + " cannot say which nodes were in which state before, so it records no undo; virt.clab.node." + op.verb + " records an exact one for one node." + target,
			Params:      []collection.Param{pLab},
			Returns:     []collection.ReturnField{ret("nodes", "list of dict", "always", "The nodes after, as lab.info reports them.")},
			Examples:    []collection.Example{{Name: "Lab " + op.verb, RunbookYAML: "- name: Lab " + op.verb + "\n  virt.clab.lab." + op.verb + ":\n    target: clab-api\n    lab: nclab\n"}},
			SeeAlso:     []string{"virt.clab.node." + op.verb, "virt.clab.lab.info"}})
	}
	add("virt.clab.lab.apply", collection.Doc{
		Summary:     "Changes a running lab to match its on-disk topology, by the difference.",
		Description: "Asks the server to apply the topology file it holds for the lab: nodes and links added, removed, recreated or restarted, and the rest left alone. With dry_run it reports what it would do and changes nothing, which is also what a check does, so a check of this method is a real prediction. It cannot be undone: nodes it recreated are new containers." + target,
		Params: []collection.Param{pLab,
			{Name: "path", Type: "string", Description: "A topology file on the server, when it is not the lab's own."},
			{Name: "dry_run", Type: "bool", Default: "false", Description: "Report the difference and change nothing."},
			{Name: "skip_post_deploy", Type: "bool", Default: "false", Description: "Skip the nodes' post-deploy steps."}},
		Returns:  []collection.ReturnField{ret("added_nodes", "list of string", "always", "Nodes the apply adds."), ret("deleted_nodes", "list of string", "always", "Nodes it removes."), ret("recreated_nodes", "list of string", "always", "Nodes it recreates."), ret("started_nodes", "list of string", "always", "Nodes it starts."), ret("restarted_nodes", "list of string", "always", "Nodes it restarts."), ret("added_links", "list of string", "always", "Links it adds."), ret("deleted_endpoints", "list of string", "always", "Link ends it removes."), ret("dry_run", "bool", "always", "Whether nothing was changed.")},
		Examples: []collection.Example{{Name: "Plan, then apply", RunbookYAML: "- name: What would change\n  virt.clab.lab.apply:\n    target: clab-api\n    lab: nclab\n    dry_run: true\n- name: Apply it\n  virt.clab.lab.apply:\n    target: clab-api\n    lab: nclab\n"}},
		SeeAlso:  []string{"virt.clab.lab.validate", "virt.clab.lab.redeploy"}})

	// ---- nodes ----
	for _, op := range []struct{ verb, past, from, to, undo, undoNote string }{
		{"start", "started", "stopped", "running", "stop", "a node that was stopped"},
		{"stop", "stopped", "running or paused", "stopped", "start", "a node that was running"},
		{"pause", "paused", "running", "paused", "unpause", "a node that was running"},
		{"unpause", "unpaused", "paused", "running", "pause", "a node that was paused"},
	} {
		add("virt.clab.node."+op.verb, collection.Doc{
			Summary:     "Makes one lab node " + op.past + ", if it is not already.",
			Description: "Reads the node's state first and acts only on a node that is " + op.from + ", so a node already " + op.to + " is left alone and reported unchanged. Its undo is virt.clab.node." + op.undo + ", recorded for " + op.undoNote + "." + target,
			Params:      []collection.Param{pLab, pNode},
			Returns:     []collection.ReturnField{ret("state", "string", "always", "The node's state after: running, exited or paused."), ret("name", "string", "always", "The node's container name.")},
			Examples:    []collection.Example{{Name: "Node " + op.verb, RunbookYAML: "- name: r1 " + op.verb + "\n  virt.clab.node." + op.verb + ":\n    target: clab-api\n    lab: nclab\n    node: r1\n"}},
			SeeAlso:     []string{"virt.clab.node." + op.undo, "virt.clab.lab.info"}})
	}
	add("virt.clab.node.restart", collection.Doc{
		Summary:     "Restarts one lab node.",
		Description: "Restarts the node through the server. It always changes the node, and records no undo: a restart cannot be put back." + target,
		Params:      []collection.Param{pLab, pNode},
		Returns:     []collection.ReturnField{ret("state", "string", "always", "The node's state after."), ret("name", "string", "always", "The node's container name.")},
		Examples:    []collection.Example{{Name: "Restart a node", RunbookYAML: "- name: Restart r1\n  virt.clab.node.restart:\n    target: clab-api\n    lab: nclab\n    node: r1\n"}},
		SeeAlso:     []string{"virt.clab.node.stop", "virt.clab.node.start"}})
	add("virt.clab.node.exec", collection.Doc{
		Summary:     "Runs one command in a lab node, with no SSH to the node.",
		Description: "Runs the command inside the node's container through the server's exec endpoint, so a node that has no SSH, or no address, is reached. The server reports exit code 0 for a command that failed, so the method wraps the command in a small shell that prints its own exit status (marked with a random value no output can forge) and reads it back: a failing command fails the task, with its output recorded first, unless allow_nonzero is set. That needs a shell (sh) in the node; raw sends the command exactly as given, with the server's exit code, which is unreliable. A check is not possible: what a command changes is known only after it runs." + target,
		Params: []collection.Param{pLab, pNode,
			{Name: "command", Type: "string", Description: "The command line, split into words by the server as a shell would, so quote what must stay one word. Set command or argv, not both."},
			{Name: "argv", Type: "list of string", Description: "The command as separate words, each quoted so the server cannot split or expand one. Set command or argv, not both."},
			{Name: "allow_nonzero", Type: "bool", Default: "false", Description: "Do not fail on a non-zero exit code; the code is still returned."},
			{Name: "raw", Type: "bool", Default: "false", Description: "Send the command as given, with no wrapper and no reliable exit code, for a node with no shell."},
			{Name: "timeout", Type: "int", Default: "60", Description: "Seconds the login and the command may take together."}},
		Returns:  []collection.ReturnField{ret("exit_code", "int", "always", "The command's exit code."), ret("stdout", "string", "always", "Its standard output."), ret("stderr", "string", "always", "Its standard error."), ret("name", "string", "always", "The node's container name.")},
		Examples: []collection.Example{{Name: "Give a lab router an address with no SSH", RunbookYAML: "- name: r1 eth1 gets its address\n  virt.clab.node.exec:\n    target: clab-api\n    lab: nclab\n    node: r1\n    argv: [ip, address, add, 10.1.1.1/24, dev, eth1]\n"}},
		SeeAlso:  []string{"container.exec", "exec.command", "net.frr.config"}})
	add("virt.clab.node.logs", read("Reads the last lines of a lab node's output.", "Returns the last tail lines the node's container wrote. The text is the node's own, and may hold anything the node printed.",
		[]collection.Param{pLab, pNode, {Name: "tail", Type: "int", Default: "100", Description: "How many of the last lines."}},
		[]collection.ReturnField{ret("logs", "string", "always", "The node's output."), ret("name", "string", "always", "The node's container name.")},
		"- name: Why did r1 stop\n  virt.clab.node.logs:\n    target: clab-api\n    lab: nclab\n    node: r1\n    tail: 50\n", "virt.clab.node.exec"))
	add("virt.clab.node.ports", read("Lists the browser-reachable ports a lab node publishes.", "Returns the ports the server reports for the node.",
		[]collection.Param{pLab, pNode},
		[]collection.ReturnField{ret("ports", "list", "always", "The node's published browser ports."), ret("name", "string", "always", "The node's container name.")},
		"- name: r1's ports\n  virt.clab.node.ports:\n    target: clab-api\n    lab: nclab\n    node: r1\n", "virt.clab.lab.info"))
	add("virt.clab.node.ssh_access", collection.Doc{
		Summary:     "Opens a temporary SSH door to a lab node through the server.",
		Description: "Asks the server for time-limited SSH access to the node: a port on the server that reaches it. The access is a session on the server, which this method's undo, virt.clab.ssh_session.terminate, closes; left alone it ends at its expiry. It returns the host, port, account and the command, never a password." + target,
		Params: []collection.Param{pLab, pNode,
			{Name: "duration", Type: "string", Default: "1h", Description: "How long the access lasts, as a duration (30m, 1h)."},
			{Name: "ssh_username", Type: "string", Description: "The node's SSH account, when it is not the server's default for its kind."}},
		Returns:  []collection.ReturnField{ret("host", "string", "always", "The server's host."), ret("port", "int", "always", "The port on the server that reaches the node."), ret("username", "string", "always", "The account to log in as."), ret("expiration", "string", "always", "When the access ends."), ret("command", "string", "always", "The ssh command that uses it.")},
		Examples: []collection.Example{{Name: "A door for ten minutes", RunbookYAML: "- name: Reach r1\n  virt.clab.node.ssh_access:\n    target: clab-api\n    lab: nclab\n    node: r1\n    duration: 10m\n"}},
		SeeAlso:  []string{"virt.clab.ssh_session.list", "virt.clab.ssh_session.terminate"}})
	add("virt.clab.node.disable_tx_offload", collection.Doc{
		Summary:     "Disables TX checksum offload on a lab node's interfaces.",
		Description: "Runs containerlab's disable-tx-offload for the node's container, as some images need before their packets leave a veth correctly. It cannot be undone: the offload setting it found is not read." + target,
		Params:      []collection.Param{pLab, pNode},
		Returns:     []collection.ReturnField{ret("name", "string", "always", "The node's container name.")},
		Examples:    []collection.Example{{Name: "Turn it off", RunbookYAML: "- name: r1 offload off\n  virt.clab.node.disable_tx_offload:\n    target: clab-api\n    lab: nclab\n    node: r1\n"}},
		SeeAlso:     []string{"virt.clab.node.exec"}})

	// ---- link impairment ----
	add("virt.clab.netem.show", read("Shows the netem impairments on a lab node's interfaces.", "Returns each interface that has a delay, jitter, loss or rate set.",
		[]collection.Param{pLab, pNode},
		[]collection.ReturnField{ret("interfaces", "list of dict", "always", "Each impaired interface: interface, delay, jitter, packet_loss and rate."), ret("name", "string", "always", "The node's container name.")},
		"- name: What is impaired\n  virt.clab.netem.show:\n    target: clab-api\n    lab: nclab\n    node: r1\n", "virt.clab.netem.set, virt.clab.netem.reset"))
	add("virt.clab.netem.set", collection.Doc{
		Summary:     "Sets delay, jitter, loss or rate on a lab node's interface, unless it is already so.",
		Description: "Reads the interface's impairments first and acts only when they differ from what is asked, so a run that repeats it is unchanged. What it sets replaces what was there: an impairment not named is cleared. Its undo puts back what it found: the same settings, or a reset when the interface had none. corruption is not shown by netem.show, so a call that sets it always acts and its undo is partial." + target,
		Params: []collection.Param{pLab, pNode,
			{Name: "interface", Type: "string", Required: true, Description: "The interface (eth1), or its alias."},
			{Name: "delay", Type: "string", Description: "A duration, such as 100ms."},
			{Name: "jitter", Type: "string", Description: "A duration; needs delay."},
			{Name: "loss", Type: "float", Description: "Packet loss, 0 to 100 percent."},
			{Name: "rate", Type: "int", Description: "Rate limit in kbit/s."},
			{Name: "corruption", Type: "float", Description: "Packet corruption, 0 to 100 percent."}},
		Returns:  []collection.ReturnField{ret("interfaces", "list of dict", "always", "The node's impaired interfaces after.")},
		Examples: []collection.Example{{Name: "A slow, lossy link", RunbookYAML: "- name: r1 eth1 is slow\n  virt.clab.netem.set:\n    target: clab-api\n    lab: nclab\n    node: r1\n    interface: eth1\n    delay: 50ms\n    loss: 1\n"}},
		SeeAlso:  []string{"virt.clab.netem.reset", "virt.clab.netem.show"}})
	add("virt.clab.netem.reset", collection.Doc{
		Summary:     "Clears the netem impairments on a lab node's interface, if it has any.",
		Description: "Reads the interface first and acts only when it has an impairment, so a clean interface is reported unchanged. Its undo is virt.clab.netem.set with the settings it found." + target,
		Params:      []collection.Param{pLab, pNode, {Name: "interface", Type: "string", Required: true, Description: "The interface (eth1), or its alias."}},
		Returns:     []collection.ReturnField{ret("interfaces", "list of dict", "always", "The node's impaired interfaces after.")},
		Examples:    []collection.Example{{Name: "Heal the link", RunbookYAML: "- name: r1 eth1 is clean\n  virt.clab.netem.reset:\n    target: clab-api\n    lab: nclab\n    node: r1\n    interface: eth1\n"}},
		SeeAlso:     []string{"virt.clab.netem.set"}})

	// ---- host-side links ----
	add("virt.clab.veth.create", collection.Doc{
		Summary:     "Creates a vEth pair between two endpoints.",
		Description: "Creates a virtual Ethernet pair whose ends are in the two endpoints, each written node:interface (for a node in a lab) or kind:name (a host or bridge interface). The server has no call that removes one, so it cannot be undone." + target,
		Params: []collection.Param{
			{Name: "a_endpoint", Type: "string", Required: true, Description: "End A, as node:interface or kind:name."},
			{Name: "b_endpoint", Type: "string", Required: true, Description: "End B, as node:interface or kind:name."},
			{Name: "mtu", Type: "int", Description: "The pair's MTU; 9500 when unset."}},
		Examples: []collection.Example{{Name: "Wire a node to the host", RunbookYAML: "- name: r1 to the bridge\n  virt.clab.veth.create:\n    target: clab-api\n    a_endpoint: clab-nclab-r1:eth9\n    b_endpoint: host:veth-r1\n"}},
		SeeAlso:  []string{"virt.clab.vxlan.create"}})
	add("virt.clab.vxlan.create", collection.Doc{
		Summary:     "Creates a VxLAN tunnel between a host interface and a remote VTEP.",
		Description: "Creates a VxLAN interface on the server's host that carries the traffic of link to remote. Its undo is virt.clab.vxlan.delete with the prefix vx-, which removes every VxLAN interface containerlab made with that prefix, so it is partial when others exist." + target,
		Params: []collection.Param{
			{Name: "remote", Type: "string", Required: true, Description: "The remote VTEP's IP address."},
			{Name: "link", Type: "string", Required: true, Description: "An existing interface on the host to bridge to the tunnel."},
			{Name: "vni", Type: "int", Description: "The VxLAN network identifier; 10 when unset."},
			{Name: "port", Type: "int", Description: "The UDP port; 14789 when unset."},
			{Name: "dev", Type: "string", Description: "The device to source the tunnel from; found when unset."},
			{Name: "mtu", Type: "int", Description: "The interface's MTU; worked out when unset."}},
		Examples: []collection.Example{{Name: "Stretch a lab to another host", RunbookYAML: "- name: VxLAN to host B\n  virt.clab.vxlan.create:\n    target: clab-api\n    remote: 10.0.0.2\n    link: veth-r1\n    vni: 100\n"}},
		SeeAlso:  []string{"virt.clab.vxlan.delete", "virt.clab.veth.create"}})
	add("virt.clab.vxlan.delete", collection.Doc{
		Summary:     "Deletes the VxLAN interfaces containerlab made with a prefix.",
		Description: "Removes every VxLAN interface on the server's host whose name starts with prefix. It cannot be undone: the tunnels' settings are not read first." + target,
		Params:      []collection.Param{{Name: "prefix", Type: "string", Default: "vx-", Description: "The interface-name prefix; vx- is what containerlab uses."}},
		Examples:    []collection.Example{{Name: "Remove the tunnels", RunbookYAML: "- name: No more tunnels\n  virt.clab.vxlan.delete:\n    target: clab-api\n"}},
		SeeAlso:     []string{"virt.clab.vxlan.create"}})

	// ---- images ----
	add("virt.clab.image.list", read("Lists the container images the server's runtime holds.", "Returns each image's id, tags and size.",
		nil, []collection.ReturnField{ret("runtime", "string", "always", "The container runtime."), ret("images", "list of dict", "always", "Each image: id, short_id, repo_tags, size, created_at.")},
		"- name: Images on the server\n  virt.clab.image.list:\n    target: clab-api\n", "virt.clab.image.pull"))
	add("virt.clab.image.pull", collection.Doc{
		Summary:     "Pulls an image onto the server, unless it is already there.",
		Description: "Pulls the image the reference names into the server's runtime. An image already present under that reference is reported unchanged. Its undo is virt.clab.image.remove, recorded when this task pulled it." + target,
		Params:      []collection.Param{{Name: "image", Type: "string", Required: true, Description: "The image reference, such as alpine:3.20.10."}},
		Returns:     []collection.ReturnField{ret("present", "bool", "always", "Whether the image is on the server now.")},
		Examples:    []collection.Example{{Name: "Have the node image", RunbookYAML: "- name: alpine is on the server\n  virt.clab.image.pull:\n    target: clab-api\n    image: alpine:3.20.10\n"}},
		SeeAlso:     []string{"virt.clab.image.remove", "virt.clab.image.list"}})
	add("virt.clab.image.remove", collection.Doc{
		Summary:     "Removes an image from the server, if it is there.",
		Description: "Removes the image the reference names. An image that is not there is reported unchanged. Its undo is a pull of the same reference, which is partial: the tag comes back but the layers may be a newer build." + target,
		Params: []collection.Param{{Name: "image", Type: "string", Required: true, Description: "The image reference."},
			{Name: "force", Type: "bool", Default: "false", Description: "Remove it even when a container uses it."}},
		Returns:  []collection.ReturnField{ret("present", "bool", "always", "Whether the image is on the server now.")},
		Examples: []collection.Example{{Name: "Drop an image", RunbookYAML: "- name: No old image\n  virt.clab.image.remove:\n    target: clab-api\n    image: alpine:3.19\n"}},
		SeeAlso:  []string{"virt.clab.image.pull"}})

	// ---- server ----
	add("virt.clab.server.version", read("Reads the containerlab version the server runs.", "Returns containerlab's own version text.",
		nil, []collection.ReturnField{ret("version", "string", "always", "containerlab's version output.")},
		"- name: containerlab version\n  virt.clab.server.version:\n    target: clab-api\n", "virt.clab.server.version_check"))
	add("virt.clab.server.version_check", read("Asks whether a newer containerlab exists.", "Returns containerlab's own answer, which reaches the internet from the server.",
		nil, []collection.ReturnField{ret("result", "string", "always", "containerlab's answer.")},
		"- name: Is it current\n  virt.clab.server.version_check:\n    target: clab-api\n", "virt.clab.server.version"))
	add("virt.clab.server.health", read("Reads the API server's health.", "Asked with no login, as the server answers it.",
		nil, []collection.ReturnField{ret("status", "string", "always", "The server's status."), ret("uptime", "string", "always", "How long it has run."), ret("api_version", "string", "always", "The API server's version.")},
		"- name: API server health\n  virt.clab.server.health:\n    target: clab-api\n", "virt.clab.server.version"))
	add("virt.clab.server.metrics", read("Reads the host's CPU, memory and disk figures the API server reports.", "Returns the server's own metrics document.",
		nil, []collection.ReturnField{ret("metrics", "dict", "always", "The server's metrics: cpu, mem and disk.")},
		"- name: Host load\n  virt.clab.server.metrics:\n    target: clab-api\n", "virt.clab.server.health"))

	// ---- capture, generate, ssh sessions, edgeshark ----
	add("virt.clab.capture.packetflix", read("Builds Packetflix capture links for a lab's interfaces.", "Asks the server for the packetflix:// URI that opens a capture of each interface named, for Wireshark with the Edgeshark plugin. It starts no capture itself.",
		[]collection.Param{pLab,
			{Name: "targets", Type: "list of dict", Required: true, Description: "What to capture: each a mapping of container_name (the node's container), interface_name, and node_namespace (true to capture in the node's namespace)."},
			{Name: "remote_hostname", Type: "string", Description: "The server's name as the capturing machine reaches it."}},
		[]collection.ReturnField{ret("captures", "list of dict", "always", "Each: container_name, interface_names and packetflix_uri.")},
		"- name: Capture links\n  virt.clab.capture.packetflix:\n    target: clab-api\n    lab: nclab\n    targets:\n      - {container_name: clab-nclab-r1, interface_name: eth1}\n", "virt.clab.edgeshark.status"))
	add("virt.clab.topology.generate", collection.Doc{
		Summary:     "Generates a containerlab topology from a description of its tiers.",
		Description: "Asks the server to write a topology (a Clos fabric, say) from the tiers given, and returns its YAML. It never deploys: deploy is refused here, since virt.clab.lab.deploy is the call that changes anything. It changes nothing on the server, so a check is the same call." + target,
		Params: []collection.Param{
			{Name: "name", Type: "string", Required: true, Description: "The topology's name."},
			{Name: "default_kind", Type: "string", Description: "The node kind used where a tier names none."},
			{Name: "images", Type: "dict", Description: "Image by kind."},
			{Name: "tiers", Type: "list of dict", Required: true, Description: "The tiers, each a mapping of count, kind and type."},
			{Name: "management_network", Type: "string", Description: "The management network's name."},
			{Name: "ipv4_subnet", Type: "string", Description: "The management network's IPv4 subnet."},
			{Name: "node_prefix", Type: "string", Description: "A prefix for node names."},
			{Name: "group_prefix", Type: "string", Description: "A prefix for tier group names."}},
		Returns:  []collection.ReturnField{ret("topology", "string", "always", "The generated topology's YAML."), ret("message", "string", "always", "The server's message.")},
		Examples: []collection.Example{{Name: "Two linux nodes", RunbookYAML: "- name: Generate\n  virt.clab.topology.generate:\n    target: clab-api\n    name: g1\n    default_kind: linux\n    images: {linux: \"alpine:3.20.10\"}\n    tiers:\n      - {count: 2, kind: linux}\n  register: g\n"}},
		SeeAlso:  []string{"virt.clab.lab.deploy"}})
	add("virt.clab.ssh_session.list", read("Lists the temporary SSH sessions the server holds.", "Returns each session's port, lab, node, account and expiry.",
		[]collection.Param{{Name: "all", Type: "bool", Default: "false", Description: "Every session, not only this login's."}},
		[]collection.ReturnField{ret("sessions", "list of dict", "always", "Each session: port, lab_name, node_name, username, expiration, created.")},
		"- name: Open doors\n  virt.clab.ssh_session.list:\n    target: clab-api\n", "virt.clab.node.ssh_access"))
	add("virt.clab.ssh_session.terminate", collection.Doc{
		Summary:     "Closes a temporary SSH session on the server, if it is open.",
		Description: "Terminates the session on the port given. A session that is not open is reported unchanged. It cannot be undone: a closed door is opened again by virt.clab.node.ssh_access, which makes a new one." + target,
		Params:      []collection.Param{{Name: "port", Type: "int", Required: true, Description: "The session's port, as ssh_access returned it."}},
		Examples:    []collection.Example{{Name: "Close a door", RunbookYAML: "- name: Close it\n  virt.clab.ssh_session.terminate:\n    target: clab-api\n    port: 2223\n"}},
		SeeAlso:     []string{"virt.clab.node.ssh_access", "virt.clab.ssh_session.list"}})
	add("virt.clab.edgeshark.status", read("Reads whether Edgeshark, the packet-capture service, runs on the server.", "Returns the server's own status document for Edgeshark.",
		nil, []collection.ReturnField{ret("status", "dict", "always", "Edgeshark's status as the server reports it.")},
		"- name: Edgeshark status\n  virt.clab.edgeshark.status:\n    target: clab-api\n", "virt.clab.edgeshark.install"))
	add("virt.clab.edgeshark.install", collection.Doc{
		Summary:     "Installs Edgeshark on the server's host, unless it runs already.",
		Description: "Reads Edgeshark's status and installs it only when it is not installed. Its undo is virt.clab.edgeshark.uninstall, recorded when this task installed it." + target,
		Examples:    []collection.Example{{Name: "Have capture ready", RunbookYAML: "- name: Edgeshark is installed\n  virt.clab.edgeshark.install:\n    target: clab-api\n"}},
		SeeAlso:     []string{"virt.clab.edgeshark.uninstall", "virt.clab.edgeshark.status"}})
	add("virt.clab.edgeshark.uninstall", collection.Doc{
		Summary:     "Uninstalls Edgeshark from the server's host, if it is installed.",
		Description: "Reads Edgeshark's status and removes it only when it is installed. Its undo is virt.clab.edgeshark.install, recorded when this task removed it." + target,
		Examples:    []collection.Example{{Name: "Remove capture", RunbookYAML: "- name: Edgeshark is gone\n  virt.clab.edgeshark.uninstall:\n    target: clab-api\n"}},
		SeeAlso:     []string{"virt.clab.edgeshark.install", "virt.clab.edgeshark.status"}})
	return specs
}

// split returns the comma-separated names in s, trimmed, or nil for "".
func split(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
