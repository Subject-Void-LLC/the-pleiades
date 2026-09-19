// Package remoteexectest: the subsystem half of the server, for the
// methods that speak NETCONF rather than a terminal CLI.
//
// It is scripted for the reason device.go's CLI is: there is no NETCONF
// server to run locally, and /bin/sh does not speak RFC 6241. What it
// covers is a method OPENING a session against something that speaks the
// protocol: the subsystem channel, the two hellos, the framing those
// hellos negotiate, an RPC, its echoed message-id, and the reply. The
// protocol itself is pkg/netconf's to cover, against bytes captured from
// real devices and against a real server in its own container tests.
package remoteexectest

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"sync"

	cryptossh "golang.org/x/crypto/ssh"
)

// endOfMessage is RFC 6242 section 4.3's separator, which base:1.0 frames
// every message with. This server advertises only base:1.0, so that is
// the framing both sides use.
const endOfMessage = "]]>]]>"

// NetconfDevice is a scripted NETCONF server: what the SSH server answers
// a "netconf" subsystem request with.
type NetconfDevice struct {
	// Capabilities are advertised in the server hello, beside the base:1.0
	// one this server always sends.
	Capabilities []string

	// Replies answers one RPC by the name of the element inside <rpc>
	// ("get-config", "edit-config", "lock"), with the XML that goes inside
	// the <rpc-reply>. A name with no entry is answered with <ok/>.
	Replies map[string]string

	// SessionID is what the hello reports. Empty means "1".
	SessionID string
}

// serveNetconfSubsystem answers a subsystem request for "netconf" and
// then speaks the protocol until the client closes the channel. Any other
// subsystem is declined, as a device offering only netconf declines one.
//
// Every RPC's element name is recorded in the server's command log, so a
// test reads back what the method asked for the same way it does for a
// command.
func serveNetconfSubsystem(channel cryptossh.Channel, requests <-chan *cryptossh.Request, log *commandLog, device *NetconfDevice) {
	defer func() { _ = channel.Close() }()

	started := make(chan struct{})
	var once sync.Once
	go func() {
		for req := range requests {
			name := ""
			if req.Type == "subsystem" {
				name = decodeSubsystemPayload(req.Payload)
			}
			if req.Type == "subsystem" && name == "netconf" {
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				once.Do(func() { close(started) })
				continue
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
		once.Do(func() { close(started) })
	}()

	<-started

	sessionID := device.SessionID
	if sessionID == "" {
		sessionID = "1"
	}
	capabilities := `<capability>urn:ietf:params:netconf:base:1.0</capability>`
	for _, c := range device.Capabilities {
		capabilities += "<capability>" + c + "</capability>"
	}
	hello := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">`+
		`<capabilities>%s</capabilities><session-id>%s</session-id></hello>`, capabilities, sessionID)
	if _, err := io.WriteString(channel, hello+endOfMessage); err != nil {
		return
	}

	reader := bufio.NewReader(channel)
	for {
		message, err := readFramedMessage(reader)
		if message != "" && !strings.Contains(message, "<hello") {
			name, id := rpcNameAndID(message)
			if name != "" {
				log.add(name)
			}
			body, known := device.Replies[name]
			if !known {
				body = "<ok/>"
			}
			reply := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rpc-reply message-id=%q xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">%s</rpc-reply>`, id, body)
			if _, werr := io.WriteString(channel, reply+endOfMessage); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// readFramedMessage reads one base:1.0 message, up to and excluding the
// end-of-message separator.
func readFramedMessage(r *bufio.Reader) (string, error) {
	var buf strings.Builder
	for {
		b, err := r.ReadByte()
		if err != nil {
			return buf.String(), err
		}
		buf.WriteByte(b)
		if strings.HasSuffix(buf.String(), endOfMessage) {
			return strings.TrimSuffix(buf.String(), endOfMessage), nil
		}
	}
}

// rpcNameAndID reads an RPC's message-id and the name of the operation
// element inside it, which is what a script keys its replies on.
func rpcNameAndID(message string) (name, id string) {
	decoder := xml.NewDecoder(strings.NewReader(message))
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return name, id
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		depth++
		if depth == 1 {
			for _, attr := range start.Attr {
				if attr.Name.Local == "message-id" {
					id = attr.Value
				}
			}
			continue
		}
		if depth == 2 {
			return start.Name.Local, id
		}
	}
}

// decodeSubsystemPayload reads the subsystem name from a "subsystem"
// request's payload, which is one SSH string: a four-byte length and the
// name.
func decodeSubsystemPayload(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	length := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if length < 0 || len(payload) < 4+length {
		return ""
	}
	return string(payload[4 : 4+length])
}
