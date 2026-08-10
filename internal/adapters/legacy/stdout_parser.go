// Package legacy implements runner.ExecutionAdapter for unconverted
// Ansible playbooks, by spinning up one real ephemeral container that
// runs a real ansible-playbook invocation against the one device a
// dispatched wire.DispatchPayload names, parsing its real captured
// stdout, and translating the result into this platform's own
// wire.JobEvent, published over the same job-log bus
// internal/adapters/native.Adapter already uses.
package legacy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// This file's parser targets Ansible's real, always-present
// `ansible.builtin.default` text callback at `-v` verbosity, not the
// `json` stdout callback PLAN.md Section 23's own architecture text might
// suggest. That callback was removed from Ansible core at the 2.10/2.11
// collection split and was never carried into community.general (verified
// directly against a real, currently-installed `ansible-core 2.19.11` in
// this repository's own development environment:
// `ANSIBLE_STDOUT_CALLBACK=json ansible-playbook ...` fails closed with
// "[ERROR]: Could not load 'json' callback plugin", and `ansible-doc -t
// callback -l` lists no `json` entry at all, only
// `community.general.syslog_json`). `-v` is required, not optional: at
// plain verbosity most modules (e.g. `command`) print a per-host result
// line with no JSON body at all, and only a module that always echoes
// data (e.g. `debug`) carries one regardless of verbosity -- also
// verified directly, not assumed.

var (
	// taskHeaderRE matches "TASK [task name] ****...", the line
	// ansible-playbook prints before every task's own per-host results.
	taskHeaderRE = regexp.MustCompile(`^TASK \[(.*)\] \*+\s*$`)

	// resultRE matches every per-host task result line except the
	// "fatal:" ones, which carry a different shape (see fatalRE): "ok:
	// [host] => {...}", "changed: [host]", "skipping: [host] => {...}",
	// "unreachable: [host] => {...}". Group 1 is the status word, group 2
	// the host, group 4 whatever follows "=> " on the same line (empty
	// when the line carries no JSON at all).
	resultRE = regexp.MustCompile(`^(ok|changed|failed|skipping|unreachable):\s*\[([^\]]+)\](?:\s*=>\s*(.*))?$`)

	// fatalRE matches "fatal: [host]: FAILED! => {...}" and "fatal:
	// [host]: UNREACHABLE! => {...}", Ansible's own distinct shape for a
	// task that stops the play on that host.
	fatalRE = regexp.MustCompile(`^fatal:\s*\[([^\]]+)\]:\s*(FAILED|UNREACHABLE)!\s*=>\s*(.*)$`)

	// playRecapHeaderRE matches the "PLAY RECAP ****..." line that opens
	// the terminal per-host summary table.
	playRecapHeaderRE = regexp.MustCompile(`^PLAY RECAP \*+\s*$`)

	// recapLineRE matches one PLAY RECAP row: "<host>  : key=val
	// key=val ...". Only ever applied while the parser is inside a PLAY
	// RECAP block (see parseState.inRecap below), never against the
	// general stream, since its pattern (anything containing a colon) is
	// far too permissive to apply unconditionally.
	recapLineRE = regexp.MustCompile(`^(\S+)\s*:\s*(.+)$`)

	// recapFieldRE extracts one "key=digits" pair from a recap line's
	// second half.
	recapFieldRE = regexp.MustCompile(`(\w+)=(\d+)`)
)

// ParseStdout parses output, a completed ansible-playbook run's real
// captured combined stdout/stderr (ContainerResult.Output, `-v`
// verbosity, ANSIBLE_FORCE_COLOR=false), into an ordered sequence of
// wire.JobEvent: one per per-host task result line, plus one final
// "task.completed" event per host derived from the PLAY RECAP table. now
// stamps every event's Timestamp: Ansible's own default callback carries
// no per-line timestamp, so every event this batch parse produces is
// honestly stamped with the single moment the completed run was parsed,
// not a fabricated per-line time. now is a parameter, not a wall-clock
// read inside this function, so this parser stays a pure function of its
// input and is deterministically testable.
//
// This is a post-hoc batch parse, not a live stream: it runs after
// ansible-playbook has already exited, over its full captured output.
// Phase 25 (The Ansible Callback Bridge) replaces this with a real
// callback plugin emitting genuinely incremental, per-task events; this
// parser is the smallest real thing that makes Phase 17's own Release
// Gate wording ("parses STDOUT... into a structured JSON event payload")
// true today.
//
// Every event's EventData.Message is Ansible's own vocabulary already
// translated: this function never lets a raw Ansible callback event name
// (e.g. "runner_on_ok") reach the returned wire.JobEvent.Status, which is
// always one of "ok", "changed", or "failed" (this parser never produces
// "started", which Adapter.Execute publishes itself before the container
// ever runs).
func ParseStdout(output []byte, now time.Time) []wire.JobEvent {
	var events []wire.JobEvent
	timestamp := now.UTC().Format(time.RFC3339)

	reader := bufio.NewReader(bytes.NewReader(output))
	currentTask := ""
	inRecap := false

	for {
		line, readErr := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")

		switch {
		case playRecapHeaderRE.MatchString(trimmed):
			inRecap = true

		case inRecap:
			if strings.TrimSpace(trimmed) == "" {
				inRecap = false
				break
			}
			if m := recapLineRE.FindStringSubmatch(trimmed); m != nil {
				events = append(events, recapEvent(m[1], m[2], timestamp))
			}

		case taskHeaderRE.MatchString(trimmed):
			currentTask = taskHeaderRE.FindStringSubmatch(trimmed)[1]

		default:
			if m := fatalRE.FindStringSubmatch(trimmed); m != nil {
				host, rest := m[1], m[3]
				message, err := decodeTrailingJSON(rest, reader)
				if err != nil {
					message = rest
				}
				events = append(events, taskEvent("failed", host, currentTask, message, timestamp))
				break
			}
			if m := resultRE.FindStringSubmatch(trimmed); m != nil {
				status, host, rest := m[1], m[2], m[3]
				message := ""
				if rest != "" {
					var err error
					message, err = decodeTrailingJSON(rest, reader)
					if err != nil {
						message = rest
					}
				}
				events = append(events, taskEvent(mapResultStatus(status), host, currentTask, message, timestamp))
			}
		}

		if readErr != nil {
			break
		}
	}

	return events
}

// mapResultStatus translates resultRE's own captured status word into the
// closed wire.JobEvent.Status vocabulary. This is the Anti-Corruption
// Layer boundary: no Ansible-native word ("skipping") ever survives past
// this function.
func mapResultStatus(word string) string {
	switch word {
	case "changed":
		return "changed"
	case "failed", "unreachable":
		return "failed"
	default: // "ok", "skipping"
		return "ok"
	}
}

// taskEvent builds one per-host, per-task wire.JobEvent.
func taskEvent(status, host, task, message, timestamp string) wire.JobEvent {
	evt := wire.JobEvent{Status: status, Host: host, Task: task}
	evt.Timestamp = timestamp
	evt.EventData.Message = message
	return evt
}

// recapEvent builds the terminal, per-host "task.completed" event from
// one PLAY RECAP row's own "key=digits" fields. Its status follows the
// same convergence convention internal/adapters/native.summarize already
// establishes: report "failed" if anything failed or was unreachable,
// "changed" only if something actually changed and nothing failed,
// otherwise "ok".
func recapEvent(host, fields, timestamp string) wire.JobEvent {
	counts := map[string]int{}
	for _, m := range recapFieldRE.FindAllStringSubmatch(fields, -1) {
		n, _ := strconv.Atoi(m[2]) // recapFieldRE's own \d+ group guarantees a valid integer
		counts[m[1]] = n
	}

	status := "ok"
	switch {
	case counts["failed"] > 0 || counts["unreachable"] > 0:
		status = "failed"
	case counts["changed"] > 0:
		status = "changed"
	}

	message := fmt.Sprintf(
		"ok=%d changed=%d unreachable=%d failed=%d skipped=%d rescued=%d ignored=%d",
		counts["ok"], counts["changed"], counts["unreachable"], counts["failed"], counts["skipped"], counts["rescued"], counts["ignored"],
	)

	evt := wire.JobEvent{Status: status, Host: host, Task: "task.completed"}
	evt.Timestamp = timestamp
	evt.EventData.Message = message
	return evt
}

// decodeTrailingJSON turns the text following "=> " on a result line into
// a display message. first is that line's own remainder, already read;
// if it does not begin a JSON object at all (a bare, non-JSON trailer),
// it is returned as the message unchanged. When first is (or begins) a
// JSON object, reader supplies any further lines needed to complete it:
// Ansible's `debug` module pretty-prints its own result across several
// lines even at "-v" (confirmed against real captured output), so a
// result's JSON body is not reliably confined to one line.
//
// It re-parses the accumulated buffer from scratch on every additional
// line read, rather than handing reader directly to a json.Decoder.
// json.Decoder reads its own input in internally buffered chunks and can
// legitimately consume bytes past the end of one decoded value while
// looking for its closing brace; those extra bytes would then be trapped
// inside the Decoder's own private buffer, invisible to this function's
// caller, which needs reader positioned at the start of the next line to
// keep scanning. Re-parsing a small, bounded buffer from scratch avoids
// that data loss entirely, at the cost of some redundant work this
// function's realistic input sizes (one task result at a time) make
// negligible.
func decodeTrailingJSON(first string, reader *bufio.Reader) (string, error) {
	trimmedFirst := strings.TrimSpace(first)
	if !strings.HasPrefix(trimmedFirst, "{") {
		return first, nil
	}

	buf := first
	for {
		var data map[string]any
		dec := json.NewDecoder(strings.NewReader(buf))
		err := dec.Decode(&data)
		switch {
		case err == nil:
			return messageFromDecoded(data), nil
		case err == io.EOF || err == io.ErrUnexpectedEOF:
			next, readErr := reader.ReadString('\n')
			buf += next
			if readErr != nil {
				return "", fmt.Errorf("incomplete JSON result body: %w", readErr)
			}
		default:
			return "", fmt.Errorf("malformed JSON result body: %w", err)
		}
	}
}

// messageFromDecoded picks the human-relevant text out of a decoded
// per-host task result. Ansible's `debug` module (and several others)
// carries a "msg" string field, which is what an operator actually wants
// to read; anything else falls back to the whole decoded object,
// re-marshaled compactly, so no information the module reported is
// silently dropped.
func messageFromDecoded(data map[string]any) string {
	if msg, ok := data["msg"].(string); ok && msg != "" {
		return msg
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(b)
}
