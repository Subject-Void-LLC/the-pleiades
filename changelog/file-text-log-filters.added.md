Added 8 file, text, and log filters, callable from `when`, `when_or`, and
`when_cel`: `syslogParse` (RFC 5424 and legacy RFC 3164, degrading field by
field on anything RFC 3164 does not strictly enforce), `lineEndingConvert`
(lf/crlf), `pathJoin`/`pathExtractExtension` (string transforms only, never
touch a real filesystem), `gzipCompress`/`gzipDecompress` (in-memory,
stdlib `compress/gzip` only), `payloadChunker` (splits a list into
fixed-size chunks), and `trimNormalizeWhitespace`. A text-diff generator was
deliberately not added this round; that is a separate future decision. See
the generated filter reference for the full list.
