//go:build integration

// The terminating proxy's configuration, kept in its own file because it
// is a different language with different rules, and because every
// directive in it is load bearing in a way that reads badly inline.
//
// WHY NGINX AND NOT TOXIPROXY, since Toxiproxy already stands at every
// other network boundary in this repository. Toxiproxy forwards TCP
// bytes. A wss:// client reaching a broker through a byte forwarder
// proves nothing a direct listener has not already proven, which is
// exactly Phase 96d's unchecked item: what wss:// claims is that a broker
// can be reached through something that TERMINATES TLS and speaks HTTP.
// So the thing in front of the broker has to be one, and what follows is
// the minimum configuration that makes it one.
// Package e2e holds the terminating proxy's configuration for the wss://
// traversal gate, kept in its own file because it is a different language
// with different rules and every directive in it is load bearing.
package e2e

// proxyLogPrefix opens every access log line, so the gate can tell them
// apart from nginx's error log in the container's merged log stream.
const proxyLogPrefix = "pleiades"

// terminatingProxyConf is the whole nginx configuration. It REPLACES the
// image's shipped one, which is why several things the stock file sets
// are restated here rather than inherited.
const terminatingProxyConf = `
# nginx will not start without this block. One worker is ample: this proxy
# carries about half a dozen long-lived connections and nothing else.
events {
    worker_connections 1024;
}

# Restated rather than inherited. The stock image writes this to a file
# symlinked to /dev/stderr, and this file replaces the stock one, so
# leaving it out would silently take away the "start worker processes"
# line the container wait strategy keys on.
error_log /dev/stderr notice;

http {
    # The whole WebSocket handshake, in four lines.
    #
    # Connection is a hop-by-hop header, so a proxy must rewrite it rather
    # than forward it. Forwarding "Connection: Upgrade" on a request that
    # is NOT an upgrade breaks keep-alive for every other request, and
    # sending "Connection: close" on one that IS an upgrade breaks the
    # upgrade. This map says "upgrade when the client asked to, close
    # otherwise", which is the canonical form from nginx's own WebSocket
    # documentation and the form every real ingress controller generates.
    map $http_upgrade $connection_upgrade {
        default upgrade;
        ''      close;
    }

    # The line the gate reads back out of the container.
    #
    # The stock combined format records neither the Upgrade header nor
    # which upstream served the request, and both are the evidence:
    # status=101 says this proxy answered a real HTTP upgrade handshake
    # rather than proxying an opaque stream, and upstream= says it then
    # talked to the broker's websocket port rather than to something else.
    # tls= records the version actually negotiated, which turns the
    # version floor from a setting into an observation.
    log_format upgrade_proof 'pleiades status=$status upstream=$upstream_addr '
                             'upgrade="$http_upgrade" connection="$http_connection" '
                             'proto=$server_protocol tls=$ssl_protocol '
                             'request="$request"';
    access_log /dev/stdout upgrade_proof;

    server {
        # 443, because that is the port the traversal claim is about: a
        # Runner on somebody else's network can reach this and cannot
        # reach 4222.
        #
        # HTTP/2 is deliberately NOT enabled. An HTTP/2 connection cannot
        # carry an HTTP/1.1 Upgrade at all, so a proxy that negotiated h2
        # would refuse every wss:// client. nats.go offers no ALPN
        # protocols so h2 could not be selected here anyway; this comment
        # is the guard, because "http2 on;" is one line away and looks
        # harmless.
        listen 443 ssl;

        # The certificate internal/tlscert generated for this run, copied
        # in by the test. Self-signed and its own root, which is what lets
        # a client verify against this one file instead of skipping
        # verification, and what lets the same file be handed to both
        # binaries as NATS_CA_FILE.
        ssl_certificate     /etc/nginx/tls/cert.pem;
        ssl_certificate_key /etc/nginx/tls/key.pem;

        # The serving half of the one statement this platform makes about
        # acceptable TLS versions, so the two ends of this handshake
        # cannot disagree. internal/tlscert states the client half.
        ssl_protocols TLSv1.2 TLSv1.3;

        # Readiness, answered by nginx itself.
        #
        # It proves three things a bound port does not: the listener is
        # up, the key pair loaded, and a client holding the generated root
        # can complete a handshake. And it proves them WITHOUT touching
        # the broker, so a broker slow to come up cannot be mistaken for a
        # proxy that failed to start.
        location = /healthz {
            return 200 "ok\n";
        }

        # The broker's own monitoring endpoint, reached THROUGH this proxy
        # rather than through a published port. This is what lets the
        # broker container publish nothing at all: the gate asks the
        # broker itself which address each of its clients connected from,
        # and over what transport. An exact-match location, so it cannot
        # shadow the upgrade location below.
        location = /connz {
            proxy_pass http://nats:8222/connz;
        }

        location / {
            # The broker's WEBSOCKET listener, by its network alias. Plain
            # http:// on purpose: TLS is terminated HERE, which is what
            # "terminating proxy" means and what a tls:// listener on the
            # broker would not exercise. A literal upstream rather than a
            # variable, so nginx resolves it at config load through
            # Docker's embedded DNS, which is why the broker container has
            # to be up before this one starts.
            proxy_pass http://nats:8080;

            # MANDATORY. nginx speaks HTTP/1.0 upstream by default and an
            # Upgrade handshake does not exist in 1.0, so without this
            # line nginx answers the client's upgrade request with a plain
            # 200 and nats.go fails with "invalid websocket connection",
            # which reads like a broker problem and is not one.
            proxy_http_version 1.1;
            proxy_set_header Upgrade    $http_upgrade;
            proxy_set_header Connection $connection_upgrade;
            proxy_set_header Host       $host;

            # Once a connection is upgraded nginx tunnels it, and an idle
            # tunnel is closed after proxy_read_timeout, which defaults to
            # 60 seconds. topology.PingInterval is 20, so the default
            # would USUALLY survive, and "usually" is how a Release Gate
            # becomes flaky. Under -race every budget in this package is
            # multiplied by raceTimeScale, so a run legitimately spends
            # minutes between broker round trips. An hour is longer than
            # any run of this package, and being wrong in the other
            # direction costs a silently dropped connection that nats.go
            # quietly reconnects, turning a real failure into
            # inexplicable churn in the logs.
            proxy_read_timeout 1h;
            proxy_send_timeout 1h;
        }
    }
}
`
