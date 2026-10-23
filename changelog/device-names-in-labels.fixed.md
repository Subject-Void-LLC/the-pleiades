Fixed device ids printed where a person reads a device's name: a run's result lines and
`outcome.allowed_unchecked` (`tasks[3].block[1] [r1]`), and `pleiades validate`'s `[route]` lines, now name the
device. A report task's `device` stays the device's id, with its name in `host`, and a validation finding gains
`host` beside its `device`.
