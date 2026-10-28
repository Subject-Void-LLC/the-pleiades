// container.copy_out: one file read out of a container, returned whole as text.
package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name:     "container.copy_out",
		Manifest: containerengine.MethodManifest(readOnly, copyOutDoc()),
		Invoke:   CopyOut,
		// It only reads, so a check of it is the task itself.
		Check: CopyOut,
	})
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
	// No such file, or no such container (the engine answers both "not
	// found"), is an answer, exists false, so a plan can branch on it in a
	// run and in a check alike; container.info tells the two apart.
	if !exists {
		return collection.Result{}, recordStats(rc, map[string]any{statName: name, paramSrc: src, statExists: false})
	}
	if !utf8.Valid(data) {
		return fail(fmt.Errorf("%s in container %s is not text; this method returns text only", src, name))
	}
	sum := sha256.Sum256(data)
	return collection.Result{}, recordStats(rc, map[string]any{statName: name, paramSrc: src, statExists: true, statText: string(data), statSize: len(data), statChecksum: hex.EncodeToString(sum[:])})
}
