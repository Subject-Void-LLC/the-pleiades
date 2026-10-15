// A one-shot container: created from a digest-pinned image, run to its end
// with its output streamed, and removed, whatever happened.
package containerengine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// RunOnceOps are the operations RunOnce sends; a session that calls it
// must be opened with them.
var RunOnceOps = []Op{
	OpImageInspect, OpImagePull,
	OpContainerCreate, OpContainerAttach, OpContainerStart, OpContainerWait, OpContainerKill, OpContainerRemove,
}

// pinnedImage is an image named by its digest, with or without a tag: the
// only form RunOnce runs, so what runs is exactly what was reviewed.
var pinnedImage = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)

// runOnceTmpfs is the writable scratch space a read-only container gets,
// since most tools write a temporary file somewhere.
const runOnceTmpfs = "rw,noexec,nosuid,size=256m"

// removeGrace bounds the cleanup after a run, on a context of its own, so
// a run whose context is already over still removes its container.
const removeGrace = 30 * time.Second

// RunOnceOptions says how a one-shot container runs.
type RunOnceOptions struct {
	// Image is a digest-pinned reference (name@sha256:...).
	Image string
	// Cmd is the command, run directly as the container's command; a
	// caller wanting a shell passes ["/bin/sh", "-c", script].
	Cmd []string
	// Network is the network the container joins; none when empty.
	Network string
	// Writable gives the container a writable root filesystem; it is
	// read-only otherwise, with a small writable /tmp.
	Writable bool
	// Pull lets an absent image be pulled, with no registry credential;
	// an absent image is refused otherwise.
	Pull bool
	// Labels are set on the container.
	Labels map[string]string
	// Timeout bounds the run; the container is killed past it.
	Timeout time.Duration
	// Output receives what the container writes to standard output and
	// standard error, in the order the engine sent it. A bounded writer
	// (gate.Tail) keeps a chatty command in constant memory.
	Output io.Writer
}

// RunOnceResult is how a one-shot container ended.
type RunOnceResult struct {
	// ExitCode is the container's exit status.
	ExitCode int
	// TimedOut reports that the run outlived Timeout and was killed.
	TimedOut bool
	// Pulled reports that the image was pulled first.
	Pulled bool
}

// RunOnce runs opts.Cmd in a fresh container from opts.Image and returns
// how it ended. The container joins no network unless one is named, has a
// read-only root unless Writable, runs with no-new-privileges, and is
// removed afterwards whether it passed, failed, timed out or was canceled.
// A ctx that ends mid-run kills the container and returns ctx's error.
func (s *Session) RunOnce(ctx context.Context, opts RunOnceOptions) (RunOnceResult, error) {
	var res RunOnceResult
	if !pinnedImage.MatchString(opts.Image) {
		return res, fmt.Errorf("containerengine: image %q is not pinned by digest (name@sha256:...)", opts.Image)
	}
	if len(opts.Cmd) == 0 || opts.Cmd[0] == "" {
		return res, errors.New("containerengine: a one-shot container needs a command")
	}
	if opts.Timeout <= 0 {
		return res, errors.New("containerengine: a one-shot container needs a timeout")
	}
	if opts.Network != "" {
		if err := ValidName("network", opts.Network); err != nil {
			return res, err
		}
	}
	output := opts.Output
	if output == nil {
		output = io.Discard
	}

	pulled, err := s.ensureImage(ctx, opts.Image, opts.Pull)
	if err != nil {
		return res, err
	}
	res.Pulled = pulled

	network := "none"
	if opts.Network != "" {
		network = opts.Network
	}
	hostConfig := &container.HostConfig{
		NetworkMode:    container.NetworkMode(network),
		ReadonlyRootfs: !opts.Writable,
		SecurityOpt:    []string{"no-new-privileges"},
	}
	if !opts.Writable {
		hostConfig.Tmpfs = map[string]string{"/tmp": runOnceTmpfs}
	}
	created, err := s.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        opts.Image,
			Cmd:          opts.Cmd,
			Labels:       opts.Labels,
			AttachStdout: true,
			AttachStderr: true,
		},
		HostConfig: hostConfig,
	})
	if err != nil {
		return res, fmt.Errorf("containerengine: creating a container from %s: %w", opts.Image, err)
	}
	id := created.ID
	if err := ValidName("container", id); err != nil {
		return res, fmt.Errorf("containerengine: the engine answered an unusable container ID: %w", err)
	}
	defer s.removeOnce(id)

	attached, err := s.API.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		return res, fmt.Errorf("containerengine: attaching to container %s: %w", id, err)
	}
	defer attached.Close()
	copied := make(chan error, 1)
	go func() {
		if media, ok := attached.MediaType(); ok && media == rawStream {
			_, err := io.Copy(output, attached.Reader)
			copied <- err
			return
		}
		copied <- copyFrames(attached.Reader, output)
	}()

	if _, err := s.API.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return res, fmt.Errorf("containerengine: starting container %s: %w", id, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	waited := s.API.ContainerWait(runCtx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case done := <-waited.Result:
		res.ExitCode = int(done.StatusCode)
		if done.Error != nil && done.Error.Message != "" {
			return res, fmt.Errorf("containerengine: waiting for container %s: %s", id, done.Error.Message)
		}
	case err := <-waited.Error:
		if runCtx.Err() == nil {
			return res, fmt.Errorf("containerengine: waiting for container %s: %w", id, err)
		}
		s.kill(id)
		if ctx.Err() != nil {
			return res, fmt.Errorf("containerengine: container %s: %w", id, ctx.Err())
		}
		res.TimedOut = true
		res.ExitCode = 137
	}
	// The stream ends when the container does; a container killed above
	// ends it too, so this does not outlast the run.
	select {
	case <-copied:
	case <-time.After(removeGrace):
	}
	return res, nil
}

// ensureImage reports whether image had to be pulled, refusing an absent
// image when pull is false. Only the engine's own "no such image" reads as
// absent; any other failure is an error.
func (s *Session) ensureImage(ctx context.Context, image string, pull bool) (bool, error) {
	_, err := s.API.ImageInspect(ctx, image)
	if err == nil {
		return false, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return false, fmt.Errorf("containerengine: inspecting image %s: %w", image, err)
	}
	if !pull {
		return false, fmt.Errorf("containerengine: image %s is not on the device's engine and pulling it was not asked for", image)
	}
	stream, err := s.API.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return false, fmt.Errorf("containerengine: pulling %s: %w", image, err)
	}
	defer stream.Close()
	if err := stream.Wait(ctx); err != nil {
		return false, fmt.Errorf("containerengine: pulling %s: %w", image, err)
	}
	return true, nil
}

// kill stops a container at once, on a context of its own.
func (s *Session) kill(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), removeGrace)
	defer cancel()
	_, _ = s.API.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: "KILL"})
}

// removeOnce removes a one-shot container, forced, with its anonymous
// volumes, on a context of its own so a run that already ended still
// cleans up.
func (s *Session) removeOnce(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), removeGrace)
	defer cancel()
	_, _ = s.API.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
}

// copyFrames streams an attached container's framed output to w: each
// frame's payload, standard output and standard error alike, in the order
// the engine sent them. Nothing is buffered beyond what io.CopyN needs, so
// a frame claiming a huge length costs no allocation; w bounds what is kept.
func copyFrames(r io.Reader, w io.Writer) error {
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading a frame header: %w", err)
		}
		size := int64(binary.BigEndian.Uint32(header[4:8]))
		if _, err := io.CopyN(w, r, size); err != nil {
			return fmt.Errorf("reading a %d-byte frame: %w", size, err)
		}
	}
}
