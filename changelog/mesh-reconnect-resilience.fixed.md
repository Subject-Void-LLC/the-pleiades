A Controller or Runner that loses its connection to the message bus now reconnects
when the link returns, however long it was gone, instead of giving up permanently
after about two minutes and needing a manual restart. Connection loss and recovery
are now logged, and starting a service before the broker is reachable no longer
fails immediately.
