// The model's answers to the PowerShell scripts pkg/vboxmanage runs:
// writing a file from its standard input, reading a file's end, and
// removing a file, over the files the model holds.
package vboxmanagetest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// assignment reads a script's $name = '...' or $name = N line.
func assignment(script, name string) string {
	m := regexp.MustCompile(`(?m)^\$` + name + ` = (?:'([^']*)'|\$?([A-Za-z0-9]+))\r?$`).FindStringSubmatch(script)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

// PowerShell answers one script, as vboxmanage.Runner, by the tag on its
// first line. A script the model does not know is an error, so a test
// cannot pass on a script nothing answered.
func (h *Host) PowerShell(_ context.Context, script, stdin string) (vboxmanage.Output, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tag, _, _ := strings.Cut(script, "\n")
	path := assignment(script, "path")
	call := strings.TrimSpace("powershell " + strings.TrimPrefix(tag, "# vboxmanage: ") + " " + path)
	h.calls = append(h.calls, call)
	if out, ok := h.failure(call); ok {
		return out, nil
	}
	switch tag {
	case "# vboxmanage: upload":
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdin))
		if err != nil {
			return vboxmanage.Output{ExitCode: 1, Stderr: "FormatException: not base64\r\n"}, nil
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != assignment(script, "want") {
			return vboxmanage.Output{ExitCode: 21, Stdout: "got=" + got + "\r\n"}, nil
		}
		h.Files[path] = data
		return vboxmanage.Output{Stdout: fmt.Sprintf("written=%d\r\n", len(data))}, nil
	case "# vboxmanage: read":
		data, ok := h.Files[path]
		if !ok {
			return vboxmanage.Output{ExitCode: 3}, nil
		}
		limit, _ := strconv.Atoi(assignment(script, "max"))
		if len(data) > limit {
			data = data[len(data)-limit:]
		}
		return vboxmanage.Output{Stdout: base64.StdEncoding.EncodeToString(data) + "\r\n"}, nil
	case "# vboxmanage: remove":
		delete(h.Files, path)
		return vboxmanage.Output{Stdout: "removed\r\n"}, nil
	}
	return vboxmanage.Output{}, fmt.Errorf("vboxmanagetest: the model does not run the script %q", tag)
}
