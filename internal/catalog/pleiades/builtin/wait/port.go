// Package wait implements the "pleiades.builtin.wait.port" namespaced
// Collection method: hold a runbook until a TCP port starts, or stops,
// accepting connections.
//
// # Why the namespace is longer than its siblings'
//
// wait.path and wait.search sit in the bare "wait" namespace. This one
// does not, and the difference is deliberate rather than an oversight:
// this is the catalog's one native-only gate marked as belonging to
// The Pleiades' own reserved namespace instead of mapping one to one onto an
// Ansible module name. Renaming the two siblings to match would be a
// second change riding along on this one, so they are left alone.
//
// # Where the probe runs, which is this method's only real decision
//
// It runs ON THE DEVICE, through the SSH connection the task already
// holds, never from the runner. Three reasons, in the order they matter:
//
// Ansible's wait_for is a module, and a module runs on the managed host.
// A playbook being converted has always meant "on the target," so a
// method that quietly asked a different question would give a different
// answer to the same YAML.
//
// A service bound to 127.0.0.1 is the common case worth waiting for, and
// it is invisible from anywhere except the device itself. A runner-side
// check could not see a database that has just come up on loopback, which
// is most of what anyone uses this for.
//
// A firewall, a NAT rule or a security group between the runner and the
// device answers about the path, not about the service. "Can I reach it"
// and "is it listening" are different questions, and only the second one
// is what a task waiting on a restart is asking.
//
// The cost is real and is stated plainly in the method's own Doc: the
// device needs a tool that can open a TCP connection, and this package
// requires python3 or bash. See portProbers for which tools were
// considered and why nc is not among them.
//
// # Layering
//
// Like every Collection package this imports only pkg/, which is why the
// SSH mechanism comes from pkg/remoteexec rather than
// internal/transport/ssh. That constraint is the same one a third-party
// Collection will have to satisfy once Part X's OCI distribution exists,
// so a built-in reaching into internal/ would be proving a pattern nobody
// outside this module could follow.
//
// Because this package lives under internal/, it is reachable only from
// code inside this module or a fork of it: Go's internal/ visibility rule
// blocks any other module from importing it at all.
package wait

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// portFQCN is this method's fully qualified name, written once so an
// error prefix and the registration cannot drift apart.
const portFQCN = "pleiades.builtin.wait.port"

// Parameter names, which are ansible.builtin.wait_for's own names. This
// platform is a superset of Ansible rather than a new vocabulary, so
// somebody converting a wait_for task should be renaming nothing.
//
// The port prefix is scaffolding rather than style: sibling methods
// landing in this package would share one Go package and Go has no
// file-level scope, so every identifier this file adds is prefixed with
// the method that owns it.
const (
	portParamPort    = "port"
	portParamHost    = "host"
	portParamTimeout = "timeout"
	portParamDelay   = "delay"
	portParamSleep   = "sleep"
	portParamState   = "state"
)

// Stat names this method returns. elapsed, port and state are
// wait_for's own return names, so a converted playbook's later tasks read
// what they already read. host is an addition, documented as one: a task
// that let the host default still wants to say in its report which
// address was actually probed.
const (
	portStatElapsed = "elapsed"
	portStatPort    = "port"
	portStatHost    = "host"
	portStatState   = "state"
)

// The two states this method accepts, which are wait_for's two
// port-relevant ones. Its present and absent are about a path and its
// drained is about connection counts, so none of the three has a meaning
// here and each is refused by name rather than silently treated as
// started.
const (
	portStateStarted = "started"
	portStateStopped = "stopped"
)

// Defaults and bounds.
//
// portDefaultHost is wait_for's own default, and it says exactly what
// "the device itself" means once the probe runs on the device: loopback
// there is the machine the task targets.
//
// portMaxSeconds caps timeout, delay and sleep at one day. Ansible has no
// such ceiling, and the divergence is deliberate twice over: a value
// larger than a day is nearly always milliseconds written where seconds
// were meant, and a bound this far from time.Duration's own range means
// the multiplication into nanoseconds below can never overflow.
//
// portConnectTimeoutSeconds is wait_for's connect_timeout default, used
// by the python prober for each individual connection attempt. This
// method does not expose it as a parameter, because the one that governs
// how long a task waits in total is timeout, and a second knob that only
// changes how quickly a filtered port is given up on is a knob nobody has
// needed yet.
const (
	portDefaultHost           = "127.0.0.1"
	portDefaultTimeout        = 300
	portDefaultDelay          = 0
	portDefaultSleep          = 1
	portMaxSeconds            = 86400
	portLowestPort            = 1
	portHighestPort           = 65535
	portConnectTimeoutSeconds = 5
)

// The three exit statuses a probe command may report, and the reason only
// these three are treated as answers.
//
// A prober that is present but broken is the failure worth designing
// against. For state: started a broken prober would report the port
// closed forever, and the task would fail loudly on its timeout, which is
// survivable. For state: stopped the very same wrong answer reads as
// success and returns immediately, which is silent and wrong. So "I could
// not run the check" gets a status of its own, and any status that is
// none of the three is an error rather than a vote.
const (
	portExitOpen     = 0
	portExitClosed   = 1
	portExitUnusable = 2
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: portFQCN,
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetworkAddressable,
			},
			// Left false: opening a TCP connection to a port needs no
			// privilege at all, and reading a listening socket's state is
			// not what this does.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// Nothing to undo, which is the same fact that makes this
			// method report changed: false. It never emits an inverse
			// either, and Port's own comment says where that is decided.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "This method only observes: it opens a TCP connection from the device and reports whether it succeeded, so the device is in " +
					"exactly the state it would have been in had the task never run. There is nothing to undo, and an inverse that waited again " +
					"would be work the forward run never did.",
			},
			// Only reads, and still not checkable: see NoCheckReason.
			NoCheckReason: "what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out " +
				"its timeout and fail where the real run succeeds",
			Doc: portDoc(),
		},
		Invoke: Port,
	})
}

// portDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, the source the
// scaffolder is driven from, and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares the two for equality so
// the copies cannot drift.
func portDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Waits for a TCP port on the target to start (or stop) accepting connections.",
		Description: "Holds the runbook until a TCP port accepts a connection, or until it stops accepting one. The check runs ON THE DEVICE, over the SSH connection the task already holds, which is where Ansible's wait_for runs it and is the only place it answers the useful question: a service bound to 127.0.0.1 is invisible from the runner, and a firewall between the runner and the device would report on the path rather than on the service. Running there costs a prerequisite, stated rather than assumed: the device needs python3 or bash, checked once before polling starts and refused by name when neither is present. python3 is preferred because its exit status is this method's own to define; bash is the fallback because it needs no package installed, using its /dev/tcp redirection, and a bash built without that feature is detected and reported rather than mistaken for a closed port. nc is deliberately not used: its flags and exit statuses differ between the openbsd, traditional and busybox builds, so a wrong answer from it could not be told apart from a real one. Never reports changed: waiting observes, it does not act.",
		Params: []collection.Param{
			{Name: portParamPort, Type: "int", Required: true, Description: "The TCP port to wait on, 1 to 65535. Write it unquoted: a quoted \"8080\" is text rather than a number and is refused by name."},
			{Name: portParamHost, Type: "string", Default: portDefaultHost, Description: "The address to connect to, resolved and dialed ON THE DEVICE. The default is loopback, which means the device itself, and is what makes this method see a service bound only to 127.0.0.1."},
			{Name: portParamTimeout, Type: "int", Default: strconv.Itoa(portDefaultTimeout), Description: "How many seconds to wait in total before giving up, at most 86400. Measured from the start of the task, so delay, the SSH connection and the tool check all come out of this budget, exactly as Ansible measures it. Running out is an error, not a quiet pass."},
			{Name: portParamDelay, Type: "int", Default: strconv.Itoa(portDefaultDelay), Description: "How many seconds to wait before the first check, at most 86400. Useful when a service is known to accept connections briefly before it is really ready."},
			{Name: portParamSleep, Type: "int", Default: strconv.Itoa(portDefaultSleep), Description: "How many seconds to wait between checks, at least 1 and at most 86400. The sleep happens on the runner, not on the device, so it costs the device nothing. Zero is refused because it would open SSH sessions in a tight loop."},
			{Name: portParamState, Type: "string", Default: portStateStarted, Choices: []string{portStateStarted, portStateStopped}, Description: "Wait for the port to start accepting connections, or to stop. Ansible's present and absent describe a path and its drained describes connection counts, so all three are refused here rather than silently read as started."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: portStatElapsed, Type: "int", Returned: "always", Description: "Whole seconds spent waiting, including the delay. Recorded on a timeout too, so a failed task still says how long it held."},
			{Name: portStatPort, Type: "int", Returned: "always", Description: "The port that was waited on."},
			{Name: portStatHost, Type: "string", Returned: "always", Description: "The address that was dialed from the device, filled in with the default when the task did not name one."},
			{Name: portStatState, Type: "string", Returned: "always", Description: "The state that was waited for, either started or stopped."},
		},
		Examples: []collection.Example{
			{
				Name:        "Wait for a database to come back after a restart",
				RunbookYAML: "- name: Wait for postgres to accept connections\n  pleiades.builtin.wait.port:\n    port: 5432\n    timeout: 120\n",
			},
			{
				Name:        "Wait for a port to be released before rebinding it",
				RunbookYAML: "- name: Wait for the old listener to go away\n  pleiades.builtin.wait.port:\n    port: 8080\n    state: stopped\n    timeout: 60\n",
			},
			{
				Name:        "Give a service a head start, then poll slowly",
				RunbookYAML: "- name: Wait for the API on its private address\n  pleiades.builtin.wait.port:\n    host: 10.0.0.7\n    port: 443\n    delay: 10\n    sleep: 5\n",
			},
		},
		SeeAlso: []string{"wait.path", "wait.search", "exec.command"},
	}
}

// portProber is one way of asking a device whether a TCP port accepts a
// connection: the tool it needs, and the shell command that asks.
type portProber struct {
	// tool is the executable whose presence makes this prober usable,
	// looked for with command -v before any probing starts.
	tool string

	// build returns the command to send, which must exit
	// portExitOpen, portExitClosed or portExitUnusable and nothing else.
	build func(host string, port int) string
}

// portProbers are the ways this method can probe, in preference order.
//
// python3 is first because its exit status is entirely ours to define:
// the source below decides what open, closed and "could not run" mean, so
// there is no tool whose conventions we have to guess at. It is also
// Ansible's own baseline on a managed host, since wait_for is a python
// module, so a device that already runs Ansible already has it.
//
// bash is second because it needs nothing installed. Its /dev/tcp
// redirection is a shell builtin, so any device with bash can probe
// without a package. It is second rather than first because that builtin
// can be compiled out, and a bash without it fails exactly like a refused
// connection; the command built below is what tells those two apart.
//
// nc was considered and rejected. The openbsd, traditional and busybox
// builds disagree about -z, about -w and about what they exit with, and
// none of those disagreements is visible from here: a busybox nc that
// does not understand -z exits non-zero, which is indistinguishable from
// a closed port, and for state: stopped that misreading is a silent pass.
// A prober whose wrong answers look like right ones is worse than not
// having it.
var portProbers = []portProber{
	{tool: "python3", build: portPythonCommand},
	{tool: "bash", build: portBashCommand},
}

// portPythonSource is the program the python prober runs, with %d filled
// in from portConnectTimeoutSeconds so the timeout is written once.
//
// The host and port arrive as argv rather than being pasted into the
// source, so nothing a runbook wrote is ever python code. connect_ex
// returns an errno instead of raising for an ordinary connection failure,
// which is what makes closed a clean exit 1; a failure that is not a
// refusal, an address that will not resolve above all, raises instead and
// lands in the except, where it becomes portExitUnusable rather than a
// port reported closed forever.
const portPythonSource = `import socket,sys
try:
    s=socket.socket(); s.settimeout(%d); r=s.connect_ex((sys.argv[1],int(sys.argv[2]))); s.close()
except Exception: sys.exit(2)
sys.exit(0 if r==0 else 1)`

// portPythonCommand builds the python3 probe for host and port.
func portPythonCommand(host string, port int) string {
	source := fmt.Sprintf(portPythonSource, portConnectTimeoutSeconds)
	return "python3 -c " + remoteexec.QuoteArg(source) + " " +
		remoteexec.QuoteArg(host) + " " + strconv.Itoa(port)
}

// portBashCommand builds the bash probe for host and port.
//
// The bash half is only the redirection: exec 3<>/dev/tcp/host/port
// succeeds when the connection is accepted and fails otherwise. Every
// decision about what that failure MEANS is made by the outer shell
// instead, which matters because bash cannot report the one distinction
// this method needs. A bash compiled without net redirections does not
// know /dev/tcp is special, treats it as an ordinary filename and fails
// with "No such file or directory", exiting 1 exactly like a refused
// connection would.
//
// So the outer shell captures bash's message, and turns two cases into
// portExitUnusable: that message, and any status at or above 126, which
// is how a shell reports a command it could not execute at all. Both are
// echoed onward so an operator sees what the device actually said.
//
// LC_ALL=C pins the message to English, since the match above is on text
// that a localized bash would otherwise translate.
func portBashCommand(host string, port int) string {
	redirect := remoteexec.QuoteArg(`exec 3<>/dev/tcp/"$1"/"$2"`)
	// The literal "bash" after -c is what bash assigns to $0, which is
	// what shifts the host and port into $1 and $2.
	attempt := "LC_ALL=C bash -c " + redirect + " bash " +
		remoteexec.QuoteArg(host) + " " + strconv.Itoa(port) + " 2>&1"

	return "msg=$(" + attempt + "); s=$?; " +
		"[ \"$s\" = 0 ] && exit 0; " +
		"if [ \"$s\" -ge 126 ]; then echo \"$msg\" >&2; exit 2; fi; " +
		"case \"$msg\" in *\"No such file\"*) echo \"$msg\" >&2; exit 2;; esac; " +
		"exit 1"
}

// portDetectCommand builds the single command that reports which of the
// probers' tools the device has.
//
// One command rather than one per prober, because each is a fresh SSH
// session and the answer is wanted before any polling starts. It uses
// only shell builtins (for, command -v, echo), so it works on a device
// whose PATH is nearly empty, which is exactly the device this is trying
// to detect. The trailing exit 0 makes the command's own status mean "the
// check ran" rather than "the last tool was present."
func portDetectCommand() string {
	quoted := make([]string, 0, len(portProbers))
	for _, prober := range portProbers {
		quoted = append(quoted, remoteexec.QuoteArg(prober.tool))
	}
	return "for t in " + strings.Join(quoted, " ") +
		"; do command -v \"$t\" >/dev/null 2>&1 && echo \"$t\"; done; exit 0"
}

// portChooseProber asks the device which tools it has and returns the
// first prober that can run there.
func portChooseProber(ctx context.Context, conn *remoteexec.Conn) (portProber, error) {
	result, err := conn.Run(ctx, portDetectCommand())
	if err != nil {
		return portProber{}, err
	}
	return portSelectProber(result)
}

// portSelectProber turns the tool check's answer into the prober to use.
//
// It is split from the round trip that produced it because that round
// trip cannot produce every answer this has to handle. The check ends in
// exit 0, so a non-zero status only ever comes from a shell that could
// not run the command at all, a network device's own CLI being the real
// example, and no test harness built on a real POSIX shell can be made
// into one. Splitting the decision out is what lets that branch be
// exercised with the exact Result such a device would return, while the
// path a working device takes stays covered end to end by the tests that
// run the real command.
func portSelectProber(result remoteexec.Result) (portProber, error) {
	var none portProber

	if result.ExitCode != 0 {
		return none, fmt.Errorf("checking the device for a TCP probe tool exited %d, so its shell did not run the check: %s",
			result.ExitCode, portFirstLine(result.Stderr))
	}

	present := make(map[string]bool, len(portProbers))
	for _, line := range strings.Fields(result.Stdout) {
		present[line] = true
	}
	// Walked in portProbers order rather than in the order the device
	// printed, so preference is decided here and not by a shell loop.
	for _, prober := range portProbers {
		if present[prober.tool] {
			return prober, nil
		}
	}

	wanted := make([]string, 0, len(portProbers))
	for _, prober := range portProbers {
		wanted = append(wanted, prober.tool)
	}
	return none, fmt.Errorf("the device has none of %s, and this method opens the connection from the device rather than from the runner, so it needs one of them: install one, or use a runner-side reachability check instead",
		strings.Join(wanted, " or "))
}

// portProbe runs one probe and reports whether the port accepted a
// connection.
//
// Only the three declared statuses are answers. Anything else means the
// command did not do what this method built it to do, and reporting that
// as "closed" is how a state: stopped wait would quietly succeed against
// a device it never really checked.
func portProbe(ctx context.Context, conn *remoteexec.Conn, prober portProber, host string, port int) (bool, error) {
	result, err := conn.Run(ctx, prober.build(host, port))
	if err != nil {
		return false, err
	}

	switch result.ExitCode {
	case portExitOpen:
		return true, nil
	case portExitClosed:
		return false, nil
	case portExitUnusable:
		return false, fmt.Errorf("the device's %s cannot open a TCP connection to %s:%d: %s",
			prober.tool, host, port, portFirstLine(result.Stderr))
	default:
		return false, fmt.Errorf("the %s probe of %s:%d exited %d, which is not an answer this method built it to give: %s",
			prober.tool, host, port, result.ExitCode, portFirstLine(result.Stderr))
	}
}

// portFirstLine reduces a probe's error output to its first line, or to a
// fixed phrase when it said nothing.
//
// A python traceback is several lines and only its last is informative,
// but its FIRST line is the one that names the failing operation, and a
// multi-line error inside a single-line task failure is unreadable. Saying
// "no output" is better than an error that trails off into nothing.
func portFirstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "it printed nothing"
	}
	if cut := strings.IndexByte(trimmed, '\n'); cut >= 0 {
		return strings.TrimSpace(trimmed[:cut])
	}
	return trimmed
}

// portRequest is everything a task asked for, already checked.
type portRequest struct {
	host string
	port int

	// state is the word the task wrote, kept for the report, and wantOpen
	// is that word turned into what a probe has to return. Both, rather
	// than deriving one from the other at each use: the report has to echo
	// the author's own vocabulary and the loop has to compare booleans.
	state    string
	wantOpen bool

	timeout time.Duration
	delay   time.Duration
	sleep   time.Duration
}

// Port implements the "pleiades.builtin.wait.port" collection method: it
// waits until a TCP port on the device starts or stops accepting
// connections.
//
// # It never reports a change, and never emits an inverse
//
// Waiting observes. The device ends the task in exactly the state it
// would have been in had the task never run, so Changed is false
// unconditionally and sdk.RecordInverse is never called. That absence is
// the record rather than a gap in one: a journal with no inverse for this
// task is saying that undoing it means doing nothing, which is true.
//
// A diff is not recorded either, and that is the one judgement here worth
// arguing with. This method really does observe two different states in a
// run that waits, closed and then open, so a before and after pair could
// be filled in. It is not, because a diff says what THIS TASK changed,
// and writing "was closed, is now open" would credit this task with
// starting a service it only watched. Whatever really started it is the
// task that should be recording that.
//
// # The order of operations
//
// Every mistake a runbook can make is refused before the first packet, so
// a typo costs no round trip and names the runbook rather than the device.
// The timeout budget starts before anything else, matching wait_for,
// which takes its own start time before sleeping the delay. The tool
// check happens once, before the delay, so a device that can never answer
// says so immediately instead of after a long wait.
func Port(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := portReadRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", portFQCN, err)
	}

	started := time.Now()
	// One deadline covering the whole task, so the connection, the tool
	// check and every probe come out of the budget the task asked for. A
	// probe that hangs on a filtered port cannot outlive it: conn.Run
	// enforces cancellation by closing the session out from under the
	// command.
	waitCtx, cancel := context.WithDeadline(ctx, started.Add(req.timeout))
	defer cancel()

	// The budget can run out during SETUP as well as during a probe, and
	// until this was handled the two said different things about one
	// cause. A deadline that expired while connecting or while choosing a
	// prober surfaced the transport's own "context deadline exceeded",
	// which tells an operator the session failed rather than that the wait
	// they asked for expired -- and skipped the stats a failed wait owes.
	//
	// Found by the push gate rather than by review: the setup path is only
	// slow enough to lose this race on a loaded machine, so it passed
	// every isolated run and failed under a full parallel suite.
	conn, err := sdk.Connect(waitCtx, rc, device, params, portFQCN)
	if err != nil {
		return collection.Result{}, portSetupFailed(ctx, waitCtx, rc, req, started, err)
	}
	defer func() { _ = conn.Close() }()

	prober, err := portChooseProber(waitCtx, conn)
	if err != nil {
		return collection.Result{}, portSetupFailed(ctx, waitCtx, rc, req, started, fmt.Errorf("%s: %w", portFQCN, err))
	}

	if err := portPause(waitCtx, req.delay); err != nil {
		return collection.Result{}, portGaveUp(ctx, rc, req, started)
	}

	for {
		open, probeErr := portProbe(waitCtx, conn, prober, req.host, req.port)
		if probeErr != nil {
			// The budget running out mid-probe is this method's own
			// timeout wearing a transport error's clothes. Reporting the
			// raw error would tell an operator the session failed, which is
			// true and useless, instead of that the wait expired.
			if waitCtx.Err() != nil {
				return collection.Result{}, portGaveUp(ctx, rc, req, started)
			}
			return collection.Result{}, fmt.Errorf("%s: %w", portFQCN, probeErr)
		}

		if open == req.wantOpen {
			if err := portRecordStats(rc, req, started); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", portFQCN, err)
			}
			return collection.Result{Changed: false}, nil
		}

		if err := portPause(waitCtx, req.sleep); err != nil {
			return collection.Result{}, portGaveUp(ctx, rc, req, started)
		}
	}
}

// portSetupFailed decides what a failure BEFORE the first probe means.
//
// The deadline above covers the connection and the tool check as well as
// every probe, so either can fail because the budget ran out rather than
// because anything is wrong with the device. Until this existed only the
// probe path said so: the same cause reported "timed out waiting for
// host:port to be started" between probes and the transport's own "context
// deadline exceeded" while connecting, which tells an operator their
// session broke rather than that their wait expired, and skipped the stats
// a failed wait owes.
//
// One function rather than the same three lines at both call sites,
// because two copies of a conversion are two places for the next person to
// fix one of. It also means the branch is exercised once rather than
// needing a separate timing-dependent test per call site.
func portSetupFailed(parent, waitCtx context.Context, rc sdk.RunbookContext, req portRequest, started time.Time, err error) error {
	if waitCtx.Err() != nil {
		return portGaveUp(parent, rc, req, started)
	}
	return err
}

// portPause waits d, giving up early when ctx ends. A zero or negative d
// returns immediately, which is what makes the default delay of zero cost
// nothing.
func portPause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// portGaveUp records the stats a run that never saw what it waited for
// still owes, then returns the error that ends the task.
//
// The stats are written before the error deliberately, even though the
// engine discards a Result that arrives with an error: elapsed is the
// number an operator wants most from a failed wait, and Ansible's wait_for
// returns it on failure for the same reason.
//
// parent is the caller's own context rather than the deadline this method
// derived from it, and telling the two apart is the whole job of this
// function. A cancellation that came from outside is not a timeout, and
// reporting it as one would send somebody looking at the device for a
// service that was never given the chance to appear.
func portGaveUp(parent context.Context, rc sdk.RunbookContext, req portRequest, started time.Time) error {
	if err := portRecordStats(rc, req, started); err != nil {
		return fmt.Errorf("%s: %w", portFQCN, err)
	}

	waited := time.Since(started).Truncate(time.Second)
	if parentErr := parent.Err(); parentErr != nil {
		return fmt.Errorf("%s: stopped waiting for %s:%d to be %s after %s because the task's own context ended: %w",
			portFQCN, req.host, req.port, req.state, waited, parentErr)
	}
	return fmt.Errorf("%s: timed out after %s waiting for %s:%d to be %s, checked from the device every %s",
		portFQCN, waited, req.host, req.port, req.state, req.sleep)
}

// portRecordStats writes what this method returns, which are wait_for's
// own return names so a converted playbook's later tasks read what they
// already read.
//
// elapsed is whole seconds, truncated rather than rounded, which is what
// wait_for reports.
func portRecordStats(rc sdk.RunbookContext, req portRequest, started time.Time) error {
	for key, value := range map[string]any{
		portStatElapsed: int(time.Since(started).Seconds()),
		portStatPort:    req.port,
		portStatHost:    req.host,
		portStatState:   req.state,
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}

// portReadRequest reads and checks everything this method needs from the
// task's params.
//
// It runs before the connection is opened, on purpose: every refusal
// below is a mistake in the runbook rather than a condition on the device,
// and one that costs a TCP connect, a key exchange and an authentication
// round before reporting itself is a slower answer to the same question.
func portReadRequest(params map[string]any) (portRequest, error) {
	var req portRequest

	number, present, err := portIntParam(params, portParamPort)
	if err != nil {
		return req, err
	}
	if !present {
		return req, fmt.Errorf("%s is required: name the TCP port to wait on", portParamPort)
	}
	if number < portLowestPort || number > portHighestPort {
		return req, fmt.Errorf("%s %d is not a TCP port: it must be between %d and %d",
			portParamPort, number, portLowestPort, portHighestPort)
	}
	req.port = number

	host, err := portTextParam(params, portParamHost)
	if err != nil {
		return req, err
	}
	if host == "" {
		host = portDefaultHost
	}
	req.host = host

	state, err := portTextParam(params, portParamState)
	if err != nil {
		return req, err
	}
	if state == "" {
		state = portStateStarted
	}
	switch state {
	case portStateStarted:
		req.wantOpen = true
	case portStateStopped:
		req.wantOpen = false
	default:
		return req, fmt.Errorf("%s %q must be %q or %q: Ansible's present and absent describe a path and its drained describes connection counts, so none of them means anything for a port",
			portParamState, state, portStateStarted, portStateStopped)
	}
	req.state = state

	// The lowest accepted value differs per parameter and each difference
	// is a real rule. A timeout of zero would give up before it looked. A
	// sleep of zero would open SSH sessions in a tight loop, which is a
	// denial of service aimed at the device the task is trying to help. A
	// delay of zero is simply the default.
	if req.timeout, err = portSecondsParam(params, portParamTimeout, portDefaultTimeout, 1); err != nil {
		return req, err
	}
	if req.delay, err = portSecondsParam(params, portParamDelay, portDefaultDelay, 0); err != nil {
		return req, err
	}
	if req.sleep, err = portSecondsParam(params, portParamSleep, portDefaultSleep, 1); err != nil {
		return req, err
	}

	return req, nil
}

// portSecondsParam reads one whole-seconds parameter, applying its
// default and refusing a value outside its own range.
func portSecondsParam(params map[string]any, key string, fallback, lowest int) (time.Duration, error) {
	value, present, err := portIntParam(params, key)
	if err != nil {
		return 0, err
	}
	if !present {
		value = fallback
	}
	if value < lowest {
		return 0, fmt.Errorf("%s %d is below %d: %s is a whole number of seconds", key, value, lowest, key)
	}
	if value > portMaxSeconds {
		return 0, fmt.Errorf("%s %d is more than the %d second ceiling (one day): a value this large is usually milliseconds written where seconds were meant",
			key, value, portMaxSeconds)
	}
	return time.Duration(value) * time.Second, nil
}

// portIntParam reads one whole-number parameter, reporting whether the
// key was present so a caller can tell an absent value from a zero.
//
// It accepts the three shapes a whole number really arrives in and
// refuses everything else. int is what YAML decoding produces on the Crawl
// tier, int64 is what a decoder configured for wide integers produces,
// and float64 is what the same value becomes after crossing the Runner's
// per-task subprocess boundary as JSON, so refusing that one would make a
// task work on one tier and fail on the other. A float that is not whole
// is refused rather than truncated, because 8080.5 is a mistake whose
// correction nobody can guess. Narrower integer types are deliberately
// absent: no decoder in this codebase produces one, so a case for them
// would be a branch nothing can reach.
//
// A string is refused too, and that refusal is the mirror image of the
// one file.permissions makes about mode. There, YAML turning an unquoted
// 0644 into a number is the trap; here, quoting a port turns a number
// into text. Both refusals name the parameter and say which way to write
// it, because sdk.StringParam's habit of treating a wrong type as absent
// would answer "port is required" to somebody who plainly wrote one.
func portIntParam(params map[string]any, key string) (int, bool, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return 0, false, nil
	}

	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int64:
		return int(value), true, nil
	case float64:
		whole := int(value)
		if float64(whole) != value {
			return 0, true, fmt.Errorf("%s is %v, which is not a whole number of %s", key, value, portUnitOf(key))
		}
		return whole, true, nil
	default:
		return 0, true, fmt.Errorf("%s is %T, not a number: write it unquoted in the runbook, since a quoted value is text", key, raw)
	}
}

// portUnitOf names what a parameter counts, so a refusal reads as a
// sentence rather than as a type complaint.
func portUnitOf(key string) string {
	if key == portParamPort {
		return "ports"
	}
	return "seconds"
}

// portTextParam reads one string parameter, refusing a value that arrived
// as something other than text.
//
// sdk.StringParam is the usual reader and it treats a non-string as
// absent, which is the wrong answer here: a host written as a bare number
// would silently become the loopback default and the task would probe a
// machine nobody asked about.
func portTextParam(params map[string]any, key string) (string, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s is %T, not text: quote it in the runbook", key, raw)
	}
	return text, nil
}
