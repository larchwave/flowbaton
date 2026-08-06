package web

// The DevTools handshake carries no Origin.
//
// Chrome refuses a remote-debugging websocket handshake whose Origin it was not
// told to allow. Desktop Chrome we launch ourselves, so --remote-allow-origins=*
// (spec 02 §4) covers it. An Android WebView is launched by the app under test,
// there is no flag to pass it, and it accepts the non-browser handshake only
// when the Origin header is absent.
//
// golang.org/x/net/websocket writes the header unconditionally
// (hybi.go:418) and refuses a nil Config.Origin outright (client.go:92), so
// there is no setting that turns it off. Rather than take on a second websocket
// dependency or hand-roll RFC 6455 framing for one header, the handshake is
// filtered on the wire: the library writes it, this strips the one line, and
// every frame after the header passes through untouched.

import (
	"bytes"
	"io"
	"net"
	"net/url"
)

// hostPort is the address to dial for a ws:// endpoint, filling in the default
// http port the URL may leave off.
func hostPort(endpoint *url.URL) string {
	if endpoint.Port() != "" {
		return endpoint.Host
	}
	return net.JoinHostPort(endpoint.Hostname(), "80")
}

// headerTerminator ends the handshake request. Everything after it is frames.
var headerTerminator = []byte("\r\n\r\n")

// originHeaderPrefix is what x/net/websocket writes, lower-cased by it.
var originHeaderPrefix = []byte("Origin: ")

// originStrippingConn removes the Origin line from the handshake request its
// wrapped writer is about to send, and gets out of the way afterwards.
//
// Writes are buffered only until the request header is complete, because the
// header may in principle arrive in more than one Write and a line cannot be
// edited once half of it is on the wire. In practice bufio flushes it whole.
type originStrippingConn struct {
	net.Conn
	pending  []byte
	inHeader bool
}

func newOriginStrippingConn(connection net.Conn) *originStrippingConn {
	return &originStrippingConn{Conn: connection, inHeader: true}
}

func (conn *originStrippingConn) Write(payload []byte) (int, error) {
	if !conn.inHeader {
		return conn.Conn.Write(payload)
	}
	conn.pending = append(conn.pending, payload...)
	end := bytes.Index(conn.pending, headerTerminator)
	if end < 0 {
		// Still mid-header: nothing can be sent yet, but the caller's bytes are
		// all accounted for.
		return len(payload), nil
	}
	conn.inHeader = false
	header := stripOriginHeader(conn.pending[:end+len(headerTerminator)])
	rest := conn.pending[end+len(headerTerminator):]
	conn.pending = nil
	if _, err := conn.Conn.Write(header); err != nil {
		return 0, err
	}
	if len(rest) > 0 {
		if _, err := conn.Conn.Write(rest); err != nil {
			return 0, err
		}
	}
	return len(payload), nil
}

// stripOriginHeader drops every Origin line from a complete request header.
func stripOriginHeader(header []byte) []byte {
	lines := bytes.SplitAfter(header, []byte("\r\n"))
	kept := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if bytes.HasPrefix(line, originHeaderPrefix) {
			continue
		}
		kept = append(kept, line)
	}
	return bytes.Join(kept, nil)
}

// interface assertion: the websocket client only needs a ReadWriteCloser, but
// keeping the net.Conn shape means a caller can still set deadlines.
var _ io.ReadWriteCloser = (*originStrippingConn)(nil)
