// Running go test with something watching it from outside: testgate's
// headroom guard sees the command before it starts (to bound its memory),
// while it runs (to stop it, with everything under it, when the machine
// runs short) and each event as it is read (to know which packages are
// running when a reading is taken).
package flakegate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Observer sees a go test run from outside it. Prepare sees the command
// before it starts, and may change its environment; Started sees it
// running; Event sees each event of its output as it is read, in order.
// A run with an Observer is started in a process group of its own where the
// system has them (SignalGroup), so a stop reaches every process under go
// test: its test binaries, compiles and links.
type Observer interface {
	Prepare(cmd *exec.Cmd)
	Started(cmd *exec.Cmd)
	Event(e Event)
}

// RunGoTestJSONObserved is RunGoTestJSONPackages with obs watching the run;
// a nil obs watches nothing.
func RunGoTestJSONObserved(args, packages []string, echo io.Writer, obs Observer) ([]Event, error) {
	if len(packages) == 0 {
		return nil, errors.New("no packages to test")
	}
	full := append([]string{"test"}, args...)
	full = append(full, "-json")
	full = append(full, packages...)

	// #nosec G204 -- args is always a fixed literal slice built at each
	// caller's own call site (testgate's main.go, coverage-check's
	// measureCoverageTolerant), and packages are import paths the Makefile
	// lists or `go list` printed, never derived from user input or an
	// environment variable; gosec cannot see through the parameters to
	// confirm that.
	cmd := exec.Command("go", full...)
	cmd.Stderr = os.Stderr
	if obs != nil {
		ownGroup(cmd)
		obs.Prepare(cmd)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("attaching stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting go test: %w", err)
	}
	if obs != nil {
		obs.Started(cmd)
	}

	var events []Event
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			// A line that is not valid JSON is not itself a failure this
			// package should classify: go test -json's own contract is one
			// JSON object per line, so anything else is either a scanner
			// artifact or output this package does not need to understand,
			// not evidence of a test outcome either way.
			continue
		}
		events = append(events, evt)
		if obs != nil {
			obs.Event(evt)
		}

		if echo != nil && evt.Test == "" && (evt.Action == "pass" || evt.Action == "fail") {
			status := "ok  "
			if evt.Action == "fail" {
				status = "FAIL"
			}
			fmt.Fprintf(echo, "%s\t%s\n", status, evt.Package)
		}
	}
	scanErr := scanner.Err()

	waitErr := cmd.Wait()
	if scanErr != nil {
		return events, fmt.Errorf("reading go test output: %w", scanErr)
	}
	return events, waitErr
}
