# An external Collection: `example.note.write`

This directory is a complete external Collection: a program built outside the `pleiades`
binary that Pleiades runs as a child process, once per task, to provide a Collection method
of its own. It provides one method, `example.note.write`, which makes sure a file on the
target holds exactly the text a task asks for.

It imports only `pkg/` packages. That is the one rule an external Collection lives under:
code outside this repository can import `pkg/` and nothing under `internal/`.

## Build it and make it available

```sh
go build -o ~/pleiades-collections/note ./examples/external_collection
chmod 700 ~/pleiades-collections
export PLEIADES_COLLECTIONS_DIR=~/pleiades-collections
pleiades collection approve note
```

`pleiades collection approve` shows the build's digest and what it says it provides, and asks
before recording it. Nothing in the directory runs until its exact build is approved, and a
rebuild has to be approved again.

The directory must be owned by you (or root) and must not be writable by your group or by
anyone else. Every file in it must be a program you own with the same rule. Pleiades refuses
to load the directory otherwise, because every program in it runs as your user. Each run is
confined with Linux's Landlock to the program's own directory, the system files it needs, your
`known_hosts` file and a private temporary directory, so it cannot read your credential store or
SSH keys; loading is refused where Landlock is unavailable.

## Use it

```yaml
id: notes
tasks:
  - name: Leave a note
    fqcn: example.note.write
    params:
      target: web1
      path: /tmp/note.txt
      content: "managed by pleiades\n"
```

```sh
pleiades doc example.note.write
pleiades validate runbooks/notes.yaml
pleiades run runbooks/notes.yaml --mode check   # says whether it would write, writes nothing
pleiades run runbooks/notes.yaml
```

## What to read

- `main.go`: the whole `main` function is one call to `external.Main`.
- `note.go`: one `collection.Descriptor`, written exactly as a built-in method's is, with an
  `Invoke` and a `Check` that share one code path and differ only in whether the write happens.

See [Extending Pleiades](../../docs/11-extending-pleiades.md) for the full contract.
