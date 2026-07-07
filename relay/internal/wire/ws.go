// Package wire is a minimal std-lib-only WebSocket implementation covering
// exactly what the relay needs: server-side upgrade with subprotocol echo,
// text/ping/pong/close frames, client-masked reads, server-unmasked writes.
// No fragmentation, no extensions, no compression — peers are cbus tools.
package wire

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	OpText  byte = 0x1
	OpClose byte = 0x8
	OpPing  byte = 0x9
	OpPong  byte = 0xA
)

const maxFrame = 1 << 20 // 1 MiB — a bus message is a short JSON line

func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// Conn is a WebSocket connection after a completed handshake.
type Conn struct {
	c      net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	client bool // client conns mask outgoing frames

	// WriteTimeout bounds each WriteFrame so a wedged peer cannot block the
	// caller until TCP gives up. Zero = no deadline.
	WriteTimeout time.Duration
}

// Upgrade hijacks the HTTP connection and completes the server handshake,
// echoing the given subprotocol (required by RFC 6455 when one was selected).
func Upgrade(w http.ResponseWriter, r *http.Request, subprotocol string) (*Conn, error) {
	if r.Method != http.MethodGet {
		return nil, errors.New("websocket upgrade requires GET")
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!headerContainsToken(r.Header.Get("Connection"), "upgrade") {
		return nil, errors.New("not a websocket upgrade request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("unsupported websocket version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if raw, err := base64.StdEncoding.DecodeString(key); err != nil || len(raw) != 16 {
		return nil, errors.New("bad Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer cannot hijack")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n")
	if subprotocol != "" {
		b.WriteString("Sec-WebSocket-Protocol: " + subprotocol + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := conn.Write([]byte(b.String())); err != nil {
		conn.Close()
		return nil, err
	}
	return &Conn{c: conn, br: brw.Reader}, nil
}

func headerContainsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Dial opens a client connection (used by the wstail test client).
// addr is host:port, path like "/tail?...", subprotocol offered and required
// to be echoed by the server.
func Dial(addr, path, subprotocol string, timeout time.Duration) (*Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, addr, key)
	if subprotocol != "" {
		req += "Sec-WebSocket-Protocol: " + subprotocol + "\r\n"
	}
	req += "\r\n"
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout)) // bound the whole handshake
	}
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(status), " ", 3)
	if len(parts) < 2 || parts[1] != "101" {
		conn.Close()
		return nil, fmt.Errorf("handshake refused: %s", strings.TrimSpace(status))
	}
	gotProto := ""
	wantAccept := acceptKey(key)
	gotAccept := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, val, ok := strings.Cut(line, ":"); ok {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "sec-websocket-accept":
				gotAccept = strings.TrimSpace(val)
			case "sec-websocket-protocol":
				gotProto = strings.TrimSpace(val)
			}
		}
	}
	if gotAccept != wantAccept {
		conn.Close()
		return nil, errors.New("bad Sec-WebSocket-Accept")
	}
	if subprotocol != "" && gotProto != subprotocol {
		conn.Close()
		return nil, fmt.Errorf("server did not echo subprotocol (got %q)", gotProto)
	}
	_ = conn.SetDeadline(time.Time{}) // handshake done; caller owns deadlines
	return &Conn{c: conn, br: br, client: true}, nil
}

// WriteFrame writes one unfragmented frame. Safe for concurrent use.
func (c *Conn) WriteFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.WriteTimeout > 0 {
		if err := c.c.SetWriteDeadline(time.Now().Add(c.WriteTimeout)); err != nil {
			return err
		}
	}
	header := make([]byte, 0, 14)
	header = append(header, 0x80|op) // FIN + opcode
	maskBit := byte(0)
	if c.client {
		maskBit = 0x80
	}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, maskBit|byte(n))
	case n < 1<<16:
		header = append(header, maskBit|126)
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header = append(header, maskBit|127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	if c.client {
		mask := make([]byte, 4)
		if _, err := rand.Read(mask); err != nil {
			return err
		}
		header = append(header, mask...)
		masked := make([]byte, n)
		for i, b := range payload {
			masked[i] = b ^ mask[i%4]
		}
		payload = masked
	}
	if _, err := c.c.Write(header); err != nil {
		return err
	}
	_, err := c.c.Write(payload)
	return err
}

// ReadFrame reads one frame, unmasking if needed. Fragmented messages are
// rejected (our peers never send them).
func (c *Conn) ReadFrame() (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(c.br, h); err != nil {
		return 0, nil, err
	}
	fin := h[0]&0x80 != 0
	op := h[0] & 0x0F
	if !fin || op == 0 {
		return 0, nil, errors.New("fragmented frames unsupported")
	}
	if h[0]&0x70 != 0 {
		return 0, nil, errors.New("reserved websocket bits set")
	}
	switch op {
	case OpText, OpClose, OpPing, OpPong:
	default:
		return 0, nil, fmt.Errorf("unsupported opcode %x", op)
	}
	masked := h[1]&0x80 != 0
	// RFC 6455: client→server frames MUST be masked, server→client MUST NOT be
	if c.client == masked {
		return 0, nil, errors.New("wrong masking direction")
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext)
	}
	if n > maxFrame {
		return 0, nil, fmt.Errorf("frame too large (%d)", n)
	}
	if op != OpText && n > 125 {
		return 0, nil, errors.New("control frame too large")
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(c.br, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}

// Close sends a close frame (best effort) and closes the socket.
func (c *Conn) Close() error {
	_ = c.WriteFrame(OpClose, nil)
	return c.c.Close()
}

// SetReadDeadline bounds the next ReadFrame.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }
