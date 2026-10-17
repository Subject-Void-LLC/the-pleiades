// container.copy_in and copy_out: one file put into, or read out of, a
// container through the engine's archive endpoints.
package container

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"path"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramDest    = "dest"
	paramSrc     = "src"
	paramContent = "content"
	paramMode    = "mode"
	paramMax     = "max_bytes"

	statChecksum = "checksum"
	statSize     = "size"
	statText     = "content"

	// maxCopyIn bounds a file put into a container, as file.copy bounds src.
	maxCopyIn = 64 << 20
	// defaultCopyOut and maxCopyOut bound a file read out, returned whole.
	defaultCopyOut = 1 << 20
	maxCopyOut     = 16 << 20
)

// containerPath reads an absolute, clean path inside a container.
func containerPath(params map[string]any, key string) (string, error) {
	p, err := sdk.RequiredStringParam(params, key)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(p) || path.Clean(p) != p || p == "/" {
		return "", fmt.Errorf("%s: %q is not an absolute, clean path to a file", key, p)
	}
	return p, nil
}

// readOne reads the regular file at p in container name, up to limit
// bytes, reporting false when there is none.
func readOne(ctx context.Context, s *containerengine.Session, name, p string, limit int) ([]byte, int64, bool, error) {
	res, err := s.API.CopyFromContainer(ctx, name, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading %s in container %s: %w", p, name, err)
	}
	defer func() { _ = res.Content.Close() }()
	if !res.Stat.Mode.IsRegular() {
		return nil, 0, false, fmt.Errorf("%s in container %s is not a regular file", p, name)
	}
	tr := tar.NewReader(res.Content)
	hdr, err := tr.Next()
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading %s in container %s: %w", p, name, err)
	}
	if hdr.Size > int64(limit) {
		return nil, 0, false, fmt.Errorf("%s in container %s is %d bytes, over the %d one read takes", p, name, hdr.Size, limit)
	}
	data, err := io.ReadAll(io.LimitReader(tr, int64(limit)+1))
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading %s in container %s: %w", p, name, err)
	}
	if len(data) > limit {
		return nil, 0, false, fmt.Errorf("%s in container %s is over %d bytes", p, name, limit)
	}
	return data, hdr.Mode & 0o7777, true, nil
}
