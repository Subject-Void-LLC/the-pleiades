Nineteen methods' documented examples, the ones `pleiades doc` and the reference pages print, could not run as written:
- **Conditions read registers by their bare names.** These now read under `result`. The restart examples read `changed`, which a register never holds; they now compare the config write's `diff` halves.
- **Eleven `ai.*` and `edge.*` tasks carried a `hosts:` key.** It is gone.
- **`svc.windows.restart`'s example wrote its config with the SSH-only `file.copy`.** It now uses `win.file.copy`.
- **`file.copy`'s template read a root that does not exist.** It now reads `vars`.
- **`container.image.load` read a register its example never wrote.** The example now saves the image first.

Every built-in method's examples now go through the engine's builder and `pleiades validate`'s rules in a test that `make pr-check` always runs.
