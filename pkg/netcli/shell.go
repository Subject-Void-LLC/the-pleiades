// The line session a Dialect runs over, whatever carries it.
package netcli

import (
	"context"
	"regexp"
)

// Shell is an interactive, line-oriented session with a device's CLI: an
// SSH shell (*remoteexec.Shell) or a Telnet session (*telnetexec.Session).
// A Session needs no more of it than these three, so a Dialect's prompt,
// paging and error conventions hold over either carrier unchanged.
type Shell interface {
	// WriteLine submits one line, refusing one holding a line terminator.
	WriteLine(ctx context.Context, line string) error
	// ReadUntil reads until pattern matches, at most maxBytes, keeping
	// what came after the match for the next read. An error it returns
	// carries what had been read when it implements partialReader.
	ReadUntil(ctx context.Context, pattern *regexp.Regexp, maxBytes int) (string, error)
	// Close ends the session and whatever connection carries it.
	Close() error
}

// partialReader is a read error that keeps what the read had got, which
// both carriers' read errors do.
type partialReader interface {
	error
	PartialRead() []byte
}
