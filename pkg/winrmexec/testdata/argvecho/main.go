// Command argvecho reports how it was started, as one line of JSON: its
// arguments, the PLEIADES_ environment variables it can see, its working
// directory, and the length and SHA-256 of everything on its standard
// input.
//
// The WinRM modes Release Gate cross-compiles it for Windows, copies it to
// the host over stdin, and runs it with no shell, because no program on a
// stock Windows host reports its own argument vector faithfully: cmd.exe
// parses its command line its own way, and powershell.exe refuses
// arguments after -EncodedCommand. Go splits a Windows command line with
// the standard rules (the same ones CommandLineToArgvW follows), so what
// this prints is what a well-behaved program receives.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// report is the one line argvecho prints.
type report struct {
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Dir         string            `json:"dir"`
	StdinLen    int               `json:"stdin_len"`
	StdinSHA256 string            `json:"stdin_sha256"`
}

func main() {
	in, _ := io.ReadAll(os.Stdin)
	sum := sha256.Sum256(in)
	env := map[string]string{}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PLEIADES_") {
			env[name] = value
		}
	}
	dir, _ := os.Getwd()
	_ = json.NewEncoder(os.Stdout).Encode(report{
		Args:        os.Args[1:],
		Env:         env,
		Dir:         dir,
		StdinLen:    len(in),
		StdinSHA256: hex.EncodeToString(sum[:]),
	})
}
