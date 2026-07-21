package client

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// codexws is a minimal WebSocket-over-UDS JSON-RPC client for a codex
// `app-server --listen unix://SOCK` (probed on codex-cli 0.145.0). The transport is a raw
// unix socket carrying an HTTP 101 upgrade then NDJSON JSON-RPC in ws text frames; raw
// NDJSON without the upgrade gets EOF. stdlib only, by ruling — no ws dependency for one
// framing we fully control. Requests are {id, method, params} (no jsonrpc field; the
// server does not require one); responses carry result/error keyed by id; everything else
// is a notification.

const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

const codexCallTimeout = 60 * time.Second

// maxFrameSize caps a single inbound frame. A codex JSON-RPC frame is far smaller; the cap
// turns a corrupt or hostile 64-bit length into an error instead of an unbounded allocation.
const maxFrameSize = 16 << 20 // 16 MiB

// rpcError is a JSON-RPC error object. The bridge switches on Code to drive its fallback
// ladder: -32600 with "no active turn to steer" vs "thread not found" are distinct rungs.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message) }

// codexNote is a server notification (method + raw params). The bridge decodes the few it
// cares about (turn/started, turn/completed, thread/status/changed).
type codexNote struct {
	Method string
	Params json.RawMessage
}

type rpcResult struct {
	result json.RawMessage
	err    *rpcError
}

// codexConn is a live connection to a codex app-server. Safe for concurrent call() from
// multiple goroutines; a single read loop demuxes responses to callers and notifications
// to notes. Notifications the consumer does not drain are dropped once notes fills — they
// are advisory (status/turn tracking), never the delivery path itself.
type codexConn struct {
	conn net.Conn
	br   *bufio.Reader

	idMu   sync.Mutex
	nextID int

	pendMu  sync.Mutex
	pending map[int]chan rpcResult

	wMu sync.Mutex // serializes socket writes (frames must not interleave)

	notes     chan codexNote
	closed    chan struct{}
	closeOnce sync.Once
}

// dialCodex connects to a unix-socket app-server and completes the ws handshake, leaving a
// read loop running. The caller closes it.
func dialCodex(sock string) (*codexConn, error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	br, err := wsHandshake(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c := &codexConn{
		conn: conn,
		// the SAME reader the handshake used: a first frame arriving in the same segment as
		// the 101 response is buffered here, and a fresh reader would silently drop it.
		br:      br,
		pending: map[int]chan rpcResult{},
		notes:   make(chan codexNote, 256),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *codexConn) notifications() <-chan codexNote { return c.notes }

func (c *codexConn) close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.conn.Close()
}

func (c *codexConn) markClosed() { c.closeOnce.Do(func() { close(c.closed) }) }

// call sends a request and blocks for its response, returning the raw result or a typed
// error (an *rpcError for a server-side error, else a transport/timeout error).
func (c *codexConn) call(method string, params any) (json.RawMessage, error) {
	c.idMu.Lock()
	c.nextID++
	id := c.nextID
	c.idMu.Unlock()

	ch := make(chan rpcResult, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		c.forget(id)
		return nil, err
	}
	if err := c.writeFrame(opText, b); err != nil {
		c.forget(id)
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-time.After(codexCallTimeout):
		c.forget(id)
		return nil, fmt.Errorf("codex rpc %s: timeout", method)
	case <-c.closed:
		return nil, fmt.Errorf("codex rpc %s: connection closed", method)
	}
}

func (c *codexConn) forget(id int) {
	c.pendMu.Lock()
	delete(c.pending, id)
	c.pendMu.Unlock()
}

func (c *codexConn) readLoop() {
	defer c.markClosed()
	var frag []byte
	var fragOp byte
	for {
		fin, op, payload, err := readFrame(c.br)
		if err != nil {
			return
		}
		switch op {
		case opPing:
			_ = c.writeFrame(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			return
		case opCont:
			frag = append(frag, payload...)
			if !fin {
				continue
			}
			payload, op = frag, fragOp
			frag = nil
		case opText, opBinary:
			if !fin {
				frag = append(frag[:0], payload...)
				fragOp = op
				continue
			}
		}
		for _, line := range bytes.Split(payload, []byte{'\n'}) {
			if line = bytes.TrimSpace(line); len(line) > 0 {
				c.dispatch(line)
			}
		}
	}
}

// dispatch routes one decoded NDJSON message: a response (id + result/error) to its
// waiter, anything with a method to the notes channel (best-effort, dropped if full).
func (c *codexConn) dispatch(line []byte) {
	var env struct {
		ID     *json.Number    `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return
	}
	if env.ID != nil && (env.Result != nil || env.Error != nil) {
		id, err := env.ID.Int64()
		if err != nil {
			return
		}
		c.pendMu.Lock()
		ch := c.pending[int(id)]
		delete(c.pending, int(id))
		c.pendMu.Unlock()
		if ch != nil {
			ch <- rpcResult{result: env.Result, err: env.Error}
		}
		return
	}
	if env.Method != "" {
		select {
		case c.notes <- codexNote{Method: env.Method, Params: env.Params}:
		default: // consumer behind; advisory notifications drop rather than stall the reader
		}
	}
}

// ---- ws frame codec ----

// wsHandshake performs the client half of RFC 6455 over an already-connected socket and
// RETURNS the bufio.Reader it used. Threading that reader on to the read loop is load-bearing:
// a server that writes its first frame in the same segment as the 101 leaves those bytes
// buffered in this reader, and a fresh reader would drop them (the C1 first-frame-loss bug).
func wsHandshake(conn net.Conn) (*bufio.Reader, error) {
	var kb [16]byte
	if _, err := rand.Read(kb[:]); err != nil {
		return nil, err
	}
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(kb[:]) + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.Contains(status, "101") {
		return nil, fmt.Errorf("codex app-server did not upgrade: %q", strings.TrimSpace(status))
	}
	for { // drain the rest of the response headers up to the blank line
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			return br, nil
		}
	}
}

// writeFrame sends a single masked frame. Client frames MUST be masked (RFC 6455 5.3);
// codex EOFs an unmasked client frame.
func (c *codexConn) writeFrame(op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	n := len(payload)
	const maskBit = 0x80
	switch {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n < 65536:
		hdr = append(hdr, maskBit|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, ext[:]...)
	}
	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	hdr = append(hdr, key[:]...)
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ key[i%4]
	}
	c.wMu.Lock()
	defer c.wMu.Unlock()
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

// readFrame reads one frame. Server frames are unmasked, but the mask bit is honored for
// robustness.
func readFrame(br *bufio.Reader) (fin bool, op byte, payload []byte, err error) {
	b0, err := br.ReadByte()
	if err != nil {
		return
	}
	fin = b0&0x80 != 0
	op = b0 & 0x0f
	b1, err := br.ReadByte()
	if err != nil {
		return
	}
	masked := b1&0x80 != 0
	n := int(b1 & 0x7f)
	switch n {
	case 126:
		var e [2]byte
		if _, err = readFull(br, e[:]); err != nil {
			return
		}
		n = int(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		if _, err = readFull(br, e[:]); err != nil {
			return
		}
		u := binary.BigEndian.Uint64(e[:])
		if u > maxFrameSize { // guard BEFORE int conversion: a huge u would overflow / OOM
			err = fmt.Errorf("codex frame too large: %d bytes (max %d)", u, maxFrameSize)
			return
		}
		n = int(u)
	}
	var key [4]byte
	if masked {
		if _, err = readFull(br, key[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = readFull(br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i%4]
		}
	}
	return
}

func readFull(br *bufio.Reader, b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := br.Read(b[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
