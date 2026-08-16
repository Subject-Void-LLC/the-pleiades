The `docker-compose.yml` stack, the `docker run` command in the getting started guide and
`make ui-dev` now all start the same broker: `nats:2.14.4-alpine` with the same flags,
instead of `nats:latest` and two different variants of 2.14.4. Two people following the same
instructions on different days get the same broker, and a broker matching the one they
deploy. The Postgres service moved from `postgres:15-alpine` to `postgres:15.19-alpine`,
because a bare major still moves on its own the day 15.20 ships. The published controller and
runner images pin their base image for the same reason.
