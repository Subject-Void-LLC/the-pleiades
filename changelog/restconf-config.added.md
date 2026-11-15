`net.restconf.config` writes one YANG data resource over RESTCONF with post, put, patch or delete, from a path it parses
and rebuilds key by key. It reads the resource before and after, so a re-run that changes nothing reports no change, and
records the undo of a create. A named secret in its content is resolved just before the write and masked in everything
the run reports.
