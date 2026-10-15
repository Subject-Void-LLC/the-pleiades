// The image a docker run starts from, resolved to the ID it runs by.
package docker

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// imageIDPattern is an image ID as docker image inspect reports it: the
// content address of the image's configuration.
var imageIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// resolveImage returns the ID of image as the device's engine holds it,
// pulling it first when the engine has no image by that reference, and
// whether it pulled.
//
// A run starts from the ID, never from the reference it was asked for: a tag
// can move between this look and the run, and running by ID makes the
// container exactly the image that was looked at. An ID works for an image
// pulled from a registry and for one built on the host, which has no
// registry digest to pin. The reference follows "--", and the answer is
// refused unless it is an image ID, since it is text from the device that
// becomes docker run's operand.
func resolveImage(ctx context.Context, conn *remoteexec.Conn, image string, pullTimeout time.Duration) (id string, pulled bool, err error) {
	id, present, err := queryImage(ctx, conn, image)
	if err != nil || present {
		return id, false, err
	}
	if _, err := runDockerCmdWithStdin(ctx, conn, []string{"docker", "pull", "--quiet", "--", image}, "", pullTimeout); err != nil {
		return "", false, fmt.Errorf("pulling %s: %w", image, err)
	}
	id, present, err = queryImage(ctx, conn, image)
	if err == nil && !present {
		err = fmt.Errorf("docker pull %s succeeded but the engine holds no such image", image)
	}
	return id, true, err
}

// queryImage asks the engine for image's ID. Only docker's own "No such
// image" answer reads as absent; any other failure is an error, never an
// absence (FAILURE_PATTERNS 451's rule, for images).
func queryImage(ctx context.Context, conn *remoteexec.Conn, image string) (id string, present bool, err error) {
	result, err := runBounded(ctx, conn, remoteexec.QuoteCommand([]string{"docker", "image", "inspect", "--format={{.Id}}", "--", image}), nil, quickCallTimeout)
	if err != nil {
		return "", false, err
	}
	if result.ExitCode != 0 {
		if strings.Contains(strings.ToLower(result.Stderr), "no such image") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("docker image inspect could not read image %s (exit %d): %s", image, result.ExitCode, failureDetail(result))
	}
	id = strings.TrimSpace(result.Stdout)
	if !imageIDPattern.MatchString(id) {
		return "", false, fmt.Errorf("docker image inspect gave no image ID for %s: %s", image, remoteexec.Excerpt(id, 128))
	}
	return id, true, nil
}
