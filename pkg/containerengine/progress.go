// Reading an engine's progress stream (load, import): the lines it says
// and the error it ends with.
package containerengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/types/jsonstream"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// maxProgressLines bounds how many of a stream's lines DrainProgress keeps.
const maxProgressLines = 256

// DrainProgress reads an engine's JSON progress stream to its end and
// returns the text lines it said (each message's stream or status text,
// trimmed, empty ones dropped, at most maxProgressLines), or the error a
// message carried. An error message is the engine's own failure: the
// request was accepted and the work then failed, which the HTTP status
// alone does not say.
func DrainProgress(r io.Reader) ([]string, error) {
	dec := json.NewDecoder(r)
	var lines []string
	for {
		var msg jsonstream.Message
		err := dec.Decode(&msg)
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return lines, fmt.Errorf("containerengine: reading the engine's progress: %w", err)
		}
		if msg.Error != nil {
			return lines, fmt.Errorf("the engine reported: %s", remoteexec.Excerpt(msg.Error.Message, 512))
		}
		for _, text := range []string{msg.Stream, msg.Status} {
			if t := strings.TrimSpace(text); t != "" && len(lines) < maxProgressLines {
				lines = append(lines, remoteexec.Excerpt(t, 512))
			}
		}
	}
}

// LoadedImages picks the images a load said it loaded out of its lines:
// "Loaded image: <ref>" and "Loaded image ID: <id>", as Docker and Podman
// both say them.
func LoadedImages(lines []string) []string {
	var out []string
	for _, line := range lines {
		for _, prefix := range []string{"Loaded image ID: ", "Loaded image: ", "Loaded image(s): "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				for _, ref := range strings.Split(rest, ",") {
					if ref = strings.TrimSpace(ref); ref != "" {
						out = append(out, ref)
					}
				}
				break
			}
		}
	}
	return out
}
