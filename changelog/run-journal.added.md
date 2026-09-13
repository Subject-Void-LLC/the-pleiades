Every task execution now leaves a durable record. The Crawl-tier CLI writes one append-only JSON Lines file per run under `.pleiades/journal/`, and a Walk-tier job's entries are stored by the Controller, keyed by the job, the device, the delivery attempt and the graph node.

The journal stores no value that came back from a device, so nothing about it needs masking or a key: it records which method ran against which device, in what order, with what outcome, and the parameter and return key names that method declares, never their values.
