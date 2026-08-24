Retention settings you change on the message stream are no longer reverted the next
time a runner restarts. Only the controller changes the stream's shape now; every
other service binds to what is already there, and creates it only if it is missing,
so you still do not need to start the services in any particular order. The same
applies to the lock bucket that holds device execution leases.

A service whose build expects a different shape than the one in use logs a warning
naming each setting that differs.
