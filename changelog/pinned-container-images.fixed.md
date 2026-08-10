The `docker-compose.yml` stack and the `docker run` command in the getting started guide now
pin an exact NATS version instead of `nats:latest`, so two people following the same
instructions on different days get the same broker. The published controller and runner
images pin their Alpine base for the same reason.
