// The node methods: a node's state, a command in it, its logs and ports, a
// temporary SSH door to it.
package clab

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// transition is one node state method: the states it acts on, the state it
// leaves, and the method that puts it back.
type transition struct {
	from []string
	to   string
	undo string
	// partial is the state whose undo cannot be exact: a node stopped from
	// paused is started to running, not to paused.
	partial string
}

// transitions are the node state methods by verb.
var transitions = map[string]transition{
	"start":   {from: []string{"exited"}, to: "running", undo: "stop"},
	"stop":    {from: []string{"running", "paused"}, to: "exited", undo: "start", partial: "paused"},
	"pause":   {from: []string{"running"}, to: "paused", undo: "unpause"},
	"unpause": {from: []string{"paused"}, to: "running", undo: "pause"},
}

// nodeState moves a node to a state by one of the server's node routes, only
// when it is in a state the move acts on, and records the exact undo.
func nodeState(verb string) handler {
	t := transitions[verb]
	return func(ctx context.Context, s *session, a *args, check bool) (bool, error) {
		lab, name := a.ident("lab"), a.ident("node")
		if a.err != nil {
			return false, a.err
		}
		n, err := s.node(ctx, lab, name)
		if err != nil {
			return false, err
		}
		acts := false
		for _, from := range t.from {
			acts = acts || n.State == from
		}
		if !acts {
			return false, s.stats(map[string]any{"state": n.State, "name": n.Name})
		}
		if check {
			return true, s.stats(map[string]any{"state": t.to, "name": n.Name})
		}
		undo := sdk.Inverse{FQCN: "virt.clab.node." + t.undo, Params: map[string]any{"lab": lab, "node": n.NodeName},
			Description: fmt.Sprintf("Puts %s back: virt.clab.node.%s.", n.Name, t.undo), Partial: n.State == t.partial}
		if undo.Partial {
			undo.Description += " It was " + t.partial + ", which that does not restore."
		}
		// Recorded before the call and kept through a failure: undoing a move
		// that never happened reads the state first and does nothing.
		undo.KeepOnFailure = true
		if err := s.undo(undo); err != nil {
			return false, err
		}
		if err := s.call(ctx, http.MethodPost, labPath(lab, "nodes", url.PathEscape(n.Name), verb), nil, nil, nil); err != nil {
			return false, err
		}
		after, err := s.node(ctx, lab, n.NodeName)
		if err != nil {
			return true, err
		}
		if after.State != t.to {
			return true, fmt.Errorf("%s: after the %s the node %s is %s, not %s", s.fqcn, verb, n.Name, after.State, t.to)
		}
		return true, s.stats(map[string]any{"state": after.State, "name": n.Name})
	}
}

// nodeRestart restarts a node. It always changes it, and has no undo.
func nodeRestart(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "nodes", url.PathEscape(n.Name), "restart"), nil, nil, nil); err != nil {
		return false, err
	}
	after, err := s.node(ctx, lab, n.NodeName)
	if err != nil {
		return true, err
	}
	return true, s.stats(map[string]any{"state": after.State, "name": n.Name})
}

// execResult is one command's answer in the server's exec response.
type execResult struct {
	Cmd        []string `json:"cmd"`
	ReturnCode int      `json:"return-code"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
}

// execWrapper runs the command in a shell that prints its own exit status on
// standard error after a marker, because the server reports exit code 0 for a
// command that failed. The marker holds a random value made for this call, so
// nothing the command prints can forge it. $1 is the marker, $2 the command.
const execWrapper = `m=$1; shift; sh -c "$1"; rc=$?; printf '\n%s%d\n' "$m" "$rc" >&2; exit $rc`

// nodeExec runs one command in a node. A failing command fails the task after
// its output is recorded, unless allow_nonzero is set.
func nodeExec(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	command := a.str("command")
	argv, hasArgv := a.list("argv")
	raw, nonzero := a.flag("raw"), a.flag("allow_nonzero")
	if a.err != nil {
		return false, a.err
	}
	switch {
	case (command != "") == hasArgv:
		return false, fmt.Errorf("%s: set command or argv, not both and not neither", s.fqcn)
	case hasArgv && len(argv) == 0:
		return false, fmt.Errorf("%s: argv is empty", s.fqcn)
	}
	line := command
	if hasArgv {
		line = remoteexec.QuoteCommand(argv)
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	sent, marker := line, ""
	if !raw {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return false, err
		}
		marker = "pleiades-exit-" + hex.EncodeToString(raw) + ":"
		sent = remoteexec.QuoteCommand([]string{"sh", "-c", execWrapper, "sh", marker, line})
	}
	var out map[string][]execResult
	if err := s.call(ctx, http.MethodPost, labPath(lab, "exec"), query("nodeFilter", n.Name), map[string]string{"command": sent}, &out); err != nil {
		return false, err
	}
	results := out[n.Name]
	if len(out) != 1 || len(results) != 1 {
		return false, fmt.Errorf("%s: the server answered for %d nodes, wanted exactly %s", s.fqcn, len(out), n.Name)
	}
	res := results[0]
	code, stderr := res.ReturnCode, res.Stderr
	if marker != "" {
		var ok bool
		if code, stderr, ok = readMarker(res.Stderr, marker); !ok {
			if err := s.stats(map[string]any{"name": n.Name, "stdout": res.Stdout, "stderr": res.Stderr, "exit_code": -1}); err != nil {
				return true, err
			}
			return true, fmt.Errorf("%s: the node did not report an exit status (no shell, or the command ended its shell): set raw to run it without one", s.fqcn)
		}
	}
	if err := s.stats(map[string]any{"name": n.Name, "stdout": res.Stdout, "stderr": stderr, "exit_code": code}); err != nil {
		return true, err
	}
	if code != 0 && !nonzero {
		return true, fmt.Errorf("%s: the command exited %d: %s", s.fqcn, code, farside.OutputTail(firstText(stderr, res.Stdout)))
	}
	return true, nil
}

// readMarker takes the exit status the wrapper printed off the end of stderr,
// and returns it with stderr as the command wrote it.
func readMarker(stderr, marker string) (int, string, bool) {
	i := strings.LastIndex(stderr, "\n"+marker)
	if i < 0 {
		return 0, stderr, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(stderr[i+1+len(marker):]))
	if err != nil {
		return 0, stderr, false
	}
	return code, stderr[:i], true
}

// firstText is the first of values that is not only white space.
func firstText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// nodeLogs reads the last lines of a node's output.
func nodeLogs(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	tail, hasTail := a.integer("tail")
	if a.err != nil {
		return false, a.err
	}
	if !hasTail {
		tail = 100
	}
	if tail < 1 || tail > 10000 {
		return false, fmt.Errorf("%s: tail must be from 1 to 10000", s.fqcn)
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	var out struct {
		Logs string `json:"logs"`
	}
	if err := s.call(ctx, http.MethodGet, labPath(lab, "nodes", url.PathEscape(n.Name), "logs"), query("tail", strconv.Itoa(tail)), nil, &out); err != nil {
		return false, err
	}
	return false, s.stats(map[string]any{"logs": out.Logs, "name": n.Name})
}

// nodePorts lists a node's browser ports.
func nodePorts(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	var out struct {
		Ports []any `json:"ports"`
	}
	if err := s.call(ctx, http.MethodGet, labPath(lab, "nodes", url.PathEscape(n.Name), "browser-ports"), nil, nil, &out); err != nil {
		return false, err
	}
	if out.Ports == nil {
		out.Ports = []any{}
	}
	return false, s.stats(map[string]any{"ports": out.Ports, "name": n.Name})
}

// nodeSSHAccess opens a temporary SSH door to a node. Its undo closes it.
func nodeSSHAccess(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	duration, user := a.str("duration"), a.ident("ssh_username")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	body := map[string]string{}
	if duration != "" {
		body["duration"] = duration
	}
	if user != "" {
		body["sshUsername"] = user
	}
	var out struct {
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Username   string `json:"username"`
		Expiration string `json:"expiration"`
		Command    string `json:"command"`
	}
	if err := s.call(ctx, http.MethodPost, labPath(lab, "nodes", url.PathEscape(n.Name), "ssh"), nil, body, &out); err != nil {
		return false, err
	}
	// The door exists as soon as the server says so, so its undo is recorded
	// before the answer is read any further.
	if err := s.undo(sdk.Inverse{FQCN: "virt.clab.ssh_session.terminate", Params: map[string]any{"port": out.Port},
		Description: fmt.Sprintf("Closes the SSH session on port %d, which this run opened.", out.Port), KeepOnFailure: true}); err != nil {
		return true, err
	}
	return true, s.stats(map[string]any{"host": out.Host, "port": out.Port, "username": out.Username, "expiration": out.Expiration, "command": out.Command})
}

// nodeDisableTxOffload disables TX checksum offload on a node's container.
func nodeDisableTxOffload(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	if err := s.call(ctx, http.MethodPost, "/api/v1/tools/disable-tx-offload", nil, map[string]string{"containerName": n.Name}, nil); err != nil {
		return false, err
	}
	return true, s.stat("name", n.Name)
}
