// Package gnmilive holds pkg/gnmi's tests against real gNMI servers in
// containers: Nokia SR Linux, the real target (authentication, TLS with its
// own certificate, JSON_IETF, Set, Subscribe and gNOI), and Stratum's
// stratum_bmv2, for framing (plaintext, no authentication, PROTO alone). Both
// images need a privileged container, which the owner allows for these two
// alone. It has no code of its own: the tests are the package.
package gnmilive
