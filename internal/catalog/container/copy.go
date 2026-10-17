// container.copy_in and copy_out: one file put into, or read out of, a
// container through the engine's archive endpoints.
package container

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"unicode/utf8"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.copy_in",
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: false,
			Notes:      "The bytes a file held before are not kept, so the write cannot be undone; a container recreated from its image has the image's file again.",
		}, copyInDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return copyIn(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return copyIn(ctx, rc, d, p, collection.ModeCheck)
		},
	})
	collection.MustRegister(collection.Descriptor{
		Name:     "container.copy_out",
		Manifest: containerengine.MethodManifest(readOnly, copyOutDoc()),
		Invoke:   CopyOut,
		// It only reads, so a check of it is the task itself.
		Check: CopyOut,
	})
}

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

// copyIn is copy_in's body: the file at dest holds the bytes given, with
// the mode given, written only when either differs.
func copyIn(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.copy_in"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	dest, err := containerPath(params, paramDest)
	if err != nil {
		return fail(err)
	}
	perm, err := fileMode(params)
	if err != nil {
		return fail(err)
	}
	data, err := bytesToCopy(rc, params)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerArchiveRead, containerengine.OpContainerArchivePut)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	if err := recordStats(rc, map[string]any{statName: name, paramDest: dest, statChecksum: want, statSize: len(data)}); err != nil {
		return collection.Result{}, err
	}
	have, haveMode, exists, err := readOne(ctx, s, name, dest, maxCopyIn)
	if err != nil {
		return fail(err)
	}
	if exists {
		got := sha256.Sum256(have)
		if hex.EncodeToString(got[:]) == want && haveMode == perm {
			return collection.Result{}, nil
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: path.Base(dest), Mode: perm, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return fail(err)
	}
	if _, err := tw.Write(data); err != nil {
		return fail(err)
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if _, err := s.API.CopyToContainer(ctx, name, client.CopyToContainerOptions{DestinationPath: path.Dir(dest), Content: &archive}); err != nil {
		return fail(fmt.Errorf("writing %s in container %s: %w", dest, name, err))
	}
	return collection.Result{Changed: true}, nil
}

// fileMode reads mode, an octal string, 0644 by default.
func fileMode(params map[string]any) (int64, error) {
	raw := sdk.StringParam(params, paramMode)
	if raw == "" {
		return 0o644, nil
	}
	m, err := strconv.ParseInt(raw, 8, 32)
	if err != nil || m < 0 || m > 0o7777 {
		return 0, fmt.Errorf("%s: %q is not an octal file mode such as \"0644\"", paramMode, raw)
	}
	return m, nil
}

// bytesToCopy is content, or src read from the run's project; exactly one.
func bytesToCopy(rc sdk.RunbookContext, params map[string]any) ([]byte, error) {
	content, hasContent := params[paramContent]
	src := sdk.StringParam(params, paramSrc)
	switch {
	case hasContent && content != nil && src != "":
		return nil, fmt.Errorf("set %s or %s, not both", paramContent, paramSrc)
	case src != "":
		files, ok := rc.(sdk.ProjectFiles)
		if !ok {
			return nil, srcUnreadable()
		}
		data, err := files.ReadProjectFile(src, maxCopyIn)
		if errors.Is(err, sdk.ErrNoProjectFiles) {
			return nil, srcUnreadable()
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", paramSrc, err)
		}
		return data, nil
	case hasContent && content != nil:
		text, ok := content.(string)
		if !ok {
			return nil, fmt.Errorf("%s is not text; quote it", paramContent)
		}
		if len(text) > maxCopyIn {
			return nil, fmt.Errorf("%s is over %d bytes", paramContent, maxCopyIn)
		}
		return []byte(text), nil
	}
	return nil, fmt.Errorf("%s or %s is required", paramContent, paramSrc)
}

// srcUnreadable is the refusal of src where the run holds no project.
func srcUnreadable() error {
	return fmt.Errorf("%s names a file in the project, which only the command line (pleiades run, pleiades adhoc) can read; a Walk-tier Runner cannot see the project, so pass the bytes as %s there", paramSrc, paramContent)
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

// CopyOut implements container.copy_out: one file read out of a
// container, returned whole as text, bounded.
func CopyOut(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.copy_out"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	src, err := containerPath(params, paramSrc)
	if err != nil {
		return fail(err)
	}
	limit, set, err := sdk.IntParam(params, paramMax)
	if err != nil {
		return fail(err)
	}
	if !set {
		limit = defaultCopyOut
	}
	if limit < 1 || limit > maxCopyOut {
		return fail(fmt.Errorf("%s: %d is not from 1 to %d", paramMax, limit, maxCopyOut))
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerArchiveRead)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	data, _, exists, err := readOne(ctx, s, name, src, limit)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(fmt.Errorf("there is no %s in container %s", src, name))
	}
	if !utf8.Valid(data) {
		return fail(fmt.Errorf("%s in container %s is not text; this method returns text only", src, name))
	}
	sum := sha256.Sum256(data)
	return collection.Result{}, recordStats(rc, map[string]any{statName: name, paramSrc: src, statText: string(data), statSize: len(data), statChecksum: hex.EncodeToString(sum[:])})
}
