// Package journal: Notes, what a consumer says of a batch, held until the
// batch is stored.
//
// The facts and journal consumers both say, per batch, what they changed of
// what it named (a method held to its node, an identity cleared, a claim kept
// as sent, an identity this toolchain would not make). Said before the write,
// a write that failed and was redelivered said it twice, and a batch never
// stored said it at all. So each consumer collects its notes and says them
// once the batch is stored, or once it is acknowledged with nothing to store
// (FAILURE_PATTERNS 1161 fixed the same for the facts consumer's own notes).
package journal

import (
	"context"
	"log/slog"
)

// Notes collects log lines to say later through one logger. The zero value
// with a logger set is ready to use; it is not safe for concurrent use.
type Notes struct {
	logger *slog.Logger
	lines  []note
}

// note is one held log line.
type note struct {
	level slog.Level
	msg   string
	attrs []slog.Attr
}

// NewNotes returns Notes that say their lines through logger.
func NewNotes(logger *slog.Logger) *Notes { return &Notes{logger: logger} }

// Warn holds a warning.
func (n *Notes) Warn(msg string, attrs ...slog.Attr) { n.add(slog.LevelWarn, msg, attrs) }

// Info holds an info line.
func (n *Notes) Info(msg string, attrs ...slog.Attr) { n.add(slog.LevelInfo, msg, attrs) }

// add holds one line.
func (n *Notes) add(level slog.Level, msg string, attrs []slog.Attr) {
	n.lines = append(n.lines, note{level: level, msg: msg, attrs: attrs})
}

// Say logs every held line, in the order held, and forgets them.
func (n *Notes) Say() {
	for _, l := range n.lines {
		n.logger.LogAttrs(context.Background(), l.level, l.msg, l.attrs...)
	}
	n.lines = nil
}
