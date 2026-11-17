A `net.trace` no hop answered now fails the task, since it says nothing about the path, unless the only thing expected is `reached: false`; it used to succeed with every hop `*`.
