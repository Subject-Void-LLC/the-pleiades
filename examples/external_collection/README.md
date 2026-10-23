# An external Collection: `example.note.write` and `example.notebook`

This directory is a complete external Collection: a program built outside the `pleiades`
binary that The Pleiades runs as a child process, once per task, to provide Collection methods
of its own. `example.note.write` makes sure a file on the target holds exactly the text a task
asks for. It also defines a capability The Pleiades has never heard of, `example.notebook`
(the connecting account keeps notes in `~/notes`), with a probe that grants it and a method,
`example.notebook.write`, that requires it, all without a rebuilt `pleiades`.

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
anyone else. Every file in it must be a program you own with the same rule. The Pleiades refuses
to load the directory otherwise, because every program in it runs as your user. Each run is
confined with Linux's Landlock to the program's own directory, the system files it needs, your
`known_hosts` file and a private temporary directory, so it cannot read your credential store or
SSH keys; loading is refused where Landlock is unavailable.

## Use it

```yaml
id: notes
tasks:
  - name: Leave a note
    example.note.write:
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

## A capability the program defines

```sh
pleiades doc example.notebook          # what it is, and how a device comes to hold it
pleiades onboard web1                  # runs example.notebook.probe: granted when ~/notes is writable
pleiades grant-capability web1 example.notebook --set dir=/home/ops/notes --reason "made by hand"
pleiades revoke-capability web1 example.notebook
```

```yaml
# What this does: writes a note in web1's notebook, wherever its grant says the notebook is.
# Blast radius: one file in the notebook.
# Changes: the note.
# Undo: remove the note by hand; the method keeps no copy.
id: notebook
hosts: web1
tasks:
  - name: A note in the notebook
    example.notebook.write:
      name: hello
      content: "from a capability no core release defined\n"
```

## What to read

- `main.go`: the whole `main` function is one call to `external.MainProgram`, naming the
  capability and the methods.
- `notebook.go`: the capability's definition, its read-only probe, and a method that finds the
  notebook's directory in the device's grant (`external.Granted`) rather than a parameter.
- `note.go`: one `collection.Descriptor`, written exactly as a built-in method's is, with an
  `Invoke` and a `Check` that share one code path and differ only in whether the write happens.

See [Extending The Pleiades](../../docs/11-extending-pleiades.md) for the full contract.
