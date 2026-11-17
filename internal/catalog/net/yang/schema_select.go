// Package yang: choosing the sources a task fetches from what the device's
// library lists, and fetching them, every bound held before a file is
// written.
package yang

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// source is one protocol's way to a device's YANG sources.
type source interface {
	// list is what the device's library names: every module, with its
	// submodules and where each source is served from.
	list(ctx context.Context) (listing, error)
	// fetch returns e's source as the device sent it, at most limit bytes.
	fetch(ctx context.Context, e yanglib.Schema, limit int) ([]byte, error)
	// warnings are what the task says of the session or client.
	warnings() []string
	// close releases the session or client.
	close()
}

// listing is one read of a device's library.
type listing struct {
	entries      []yanglib.Schema
	completeness yanglib.Completeness
	libraryError string
}

// file is one source a task fetches and writes.
type file struct {
	entry      yanglib.Schema
	submodule  bool
	importOnly bool
	// path is where it is written, dest and its file name.
	path string
	// data is the source as fetched, then as masked.
	data   []byte
	masked bool
}

// choose is the files a request fetches from l: every entry (and, with
// submodules, each one's submodules) when the request names none, else each
// one named. A name the library lists at two revisions is refused until one
// is named, and one it does not list is refused naming how complete the list
// is. Each source is chosen once, however many modules list it.
func choose(l listing, r request) ([]file, error) {
	var out []file
	seen := map[string]bool{}
	add := func(e yanglib.Schema, submodule bool) error {
		name, err := e.FileName()
		if err != nil {
			return err
		}
		if seen[e.Key()] {
			return nil
		}
		seen[e.Key()] = true
		out = append(out, file{entry: e, submodule: submodule, importOnly: e.ImportOnly, path: path.Join(r.dest, name)})
		return nil
	}
	addModule := func(e yanglib.Schema) error {
		if err := add(e, false); err != nil || !r.submodules {
			return err
		}
		for _, sub := range e.Submodules {
			if err := add(sub, true); err != nil {
				return err
			}
		}
		return nil
	}
	if len(r.modules) == 0 {
		for _, e := range l.entries {
			if err := addModule(e); err != nil {
				return nil, err
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("the device's YANG library lists no module%s", partial(l))
		}
		return out, nil
	}
	for _, w := range r.modules {
		e, submodule, err := find(l, w)
		if err != nil {
			return nil, err
		}
		if submodule {
			err = add(e, true)
		} else {
			err = addModule(e)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// find is the entry w names: a module the library lists, or else a submodule
// of one, refusing a name it lists at more than one revision.
func find(l listing, w wanted) (yanglib.Schema, bool, error) {
	for _, level := range []bool{false, true} {
		var matches []yanglib.Schema
		for _, e := range l.entries {
			candidates := []yanglib.Schema{e}
			if level {
				candidates = e.Submodules
			}
			for _, c := range candidates {
				if c.Name == w.name && (w.revision == "" || c.Revision == w.revision) {
					matches = append(matches, c)
				}
			}
		}
		switch {
		case len(matches) == 1:
			return matches[0], level, nil
		case len(matches) > 1:
			keys := make([]string, len(matches))
			for i, m := range matches {
				keys[i] = m.Key()
			}
			sort.Strings(keys)
			return yanglib.Schema{}, false, fmt.Errorf("the device's YANG library lists %s at more than one revision (%s): name the one to fetch, as %s",
				w.name, strings.Join(keys, ", "), keys[0])
		}
	}
	return yanglib.Schema{}, false, fmt.Errorf("the device's YANG library lists no module %s%s; net.netconf.facts or net.restconf.facts lists what it serves", w.key(), partial(l))
}

// partial says how far l is to be trusted, for a refusal: nothing for a
// complete list.
func partial(l listing) string {
	if l.completeness == yanglib.Complete {
		return ""
	}
	if l.libraryError != "" {
		return fmt.Sprintf(" (its module list is %s: %s)", l.completeness, l.libraryError)
	}
	return fmt.Sprintf(" (its module list is %s)", l.completeness)
}

// gather lists src's library, chooses the files r asks for, refuses a task
// past its bounds or naming two files one case-insensitive file system would
// take for one, and fetches each, holding each answer to being the source
// asked for, masking it by forms, and refusing one that holds a short secret
// (shorts) as a whole token. The bounds hold each source as masked, the bytes
// written, since a placeholder can be longer than the secret it replaces.
// Nothing is written here, so a refusal leaves the project as it was.
func gather(ctx context.Context, src source, r request, forms, shorts []string) ([]file, error) {
	l, err := src.list(ctx)
	if err != nil {
		return nil, err
	}
	files, err := choose(l, r)
	if err != nil {
		return nil, err
	}
	if len(files) > r.maxModules {
		return nil, fmt.Errorf("the task would fetch %d sources (modules and their submodules), more than %s (%d): name the modules to fetch in %s, or raise %s (at most %d)",
			len(files), paramMaxModules, r.maxModules, paramModules, paramMaxModules, ceilingMaxModules)
	}
	folded := map[string]string{}
	for _, f := range files {
		lower := strings.ToLower(f.path)
		if other, ok := folded[lower]; ok {
			return nil, fmt.Errorf("%s and %s name one file on a file system that ignores case (macOS's, Windows'), so neither is written: name one of them in %s", other, f.path, paramModules)
		}
		folded[lower] = f.path
	}
	total := 0
	for i := range files {
		f := &files[i]
		data, err := src.fetch(ctx, f.entry, r.maxModuleBytes)
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", f.entry.Key(), err)
		}
		if err := yanglib.CheckSource(data, f.entry, f.submodule); err != nil {
			return nil, err
		}
		f.data, f.masked = maskKnown(data, forms)
		for _, s := range shorts {
			// Never the value: only the module, and why.
			if holdsToken(string(f.data), s) {
				return nil, fmt.Errorf("the source of %s holds, as a word of its own, a secret value of this run shorter than %d bytes, which cannot be masked "+
					"without cutting the schema's own words apart, so nothing is written: give the account or the named secret a value of at least %d bytes, or leave %s out",
					f.entry.Key(), minMaskLength, minMaskLength, f.entry.Name)
			}
		}
		if len(f.data) > r.maxModuleBytes {
			return nil, fmt.Errorf("the source of %s, as masked, is larger than %s (%d bytes), and is not cut short", f.entry.Key(), paramMaxModuleBytes, r.maxModuleBytes)
		}
		if total += len(f.data); total > r.maxTotalBytes {
			return nil, fmt.Errorf("the sources come to more than %s (%d bytes) by %s, so none is written: name fewer modules, or raise %s (at most %d)",
				paramMaxTotalBytes, r.maxTotalBytes, f.entry.Key(), paramMaxTotalBytes, ceilingMaxTotalBytes)
		}
	}
	return files, nil
}
