`make dev-cert` now writes its private key at mode 0600. It wrote 0644, readable by every
user on the machine, because the underlying tool's `-container-readable` flag defaulted to
on and the target never said otherwise. The relaxed mode is still available for the case
it was added for, a container running as another UID reading the key through a bind mount,
but it has to be asked for: `make dev-cert DEV_CERT_FLAGS=-container-readable`, which
prints a line saying the key is world readable.
