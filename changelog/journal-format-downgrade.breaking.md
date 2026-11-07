A run journal written by 0.4.0 names its format on every line, so 0.3.0 cannot read it: `pleiades journal` and
`pleiades rollback` on 0.3.0 refuse it with an unknown `format` key. Run a journal's rollback with the release that
wrote it or a later one.
