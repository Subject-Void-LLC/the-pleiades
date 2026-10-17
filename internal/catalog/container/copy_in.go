// container.copy_in: one file put into a container, written only when it differs.
package container

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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
	if errors.Is(err, errTooLarge) {
		// Larger than any file this method writes, so it differs.
		have, exists, err = nil, false, nil
	}
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
	raw, err := containerengine.TextParam(params, paramMode)
	if err != nil {
		return 0, err
	}
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
	src, err := containerengine.TextParam(params, paramSrc)
	if err != nil {
		return nil, err
	}
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
