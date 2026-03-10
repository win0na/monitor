// Package obs provides an OBS WebSocket v5 client for the Stream Monitor.
//
// Connects to OBS via its WebSocket v5 protocol using a raw TCP socket
// with RFC 6455 framing. Authenticates if needed and polls streaming
// statistics every second. Automatically reconnects on disconnection.
package obs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"stream_monitor/internal/state"
)

const (
	host = "localhost"
	port = 4455
)

// ── Minimal WebSocket implementation (RFC 6455) ─────────────────────────────

// wsConn is a minimal WebSocket client using raw TCP sockets.
// Implements just enough of RFC 6455 to communicate with OBS WebSocket v5.
type wsConn struct {
	conn   net.Conn
	closed bool
	mu     sync.Mutex
}

// wsConnect performs the HTTP upgrade handshake and returns a connected wsConn.
func wsConnect(host string, port int) (*wsConn, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}

	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	handshake := fmt.Sprintf(
		"GET / HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n"+
			"\r\n",
		addr, key,
	)
	if _, err := conn.Write([]byte(handshake)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// Read HTTP response until \r\n\r\n
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("server closed during handshake")
		}
		buf = append(buf, tmp[:n]...)
		if containsCRLFCRLF(buf) {
			break
		}
	}

	if len(buf) < 12 || string(buf[:12]) != "HTTP/1.1 101" {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake failed: %s", string(buf[:min(80, len(buf))]))
	}

	return &wsConn{conn: conn}, nil
}

// containsCRLFCRLF checks if buf contains \r\n\r\n.
func containsCRLFCRLF(buf []byte) bool {
	for i := 0; i+3 < len(buf); i++ {
		if buf[i] == '\r' && buf[i+1] == '\n' && buf[i+2] == '\r' && buf[i+3] == '\n' {
			return true
		}
	}
	return false
}

// send transmits a masked text frame.
func (ws *wsConn) send(text string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.closed {
		return fmt.Errorf("connection closed")
	}

	data := []byte(text)
	mask := make([]byte, 4)
	rand.Read(mask)

	masked := make([]byte, len(data))
	for i, b := range data {
		masked[i] = b ^ mask[i%4]
	}

	var frame []byte
	frame = append(frame, 0x81) // FIN + text opcode

	length := len(data)
	switch {
	case length < 126:
		frame = append(frame, byte(0x80|length))
	case length < 65536:
		frame = append(frame, 0xFE)
		frame = append(frame, byte(length>>8), byte(length))
	default:
		frame = append(frame, 0xFF)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		frame = append(frame, b...)
	}

	frame = append(frame, mask...)
	frame = append(frame, masked...)

	_, err := ws.conn.Write(frame)
	if err != nil {
		ws.closed = true
	}
	return err
}

// recvExact reads exactly n bytes from the socket.
func (ws *wsConn) recvExact(n int) ([]byte, error) {
	buf := make([]byte, n)
	read := 0
	for read < n {
		m, err := ws.conn.Read(buf[read:])
		if err != nil {
			ws.closed = true
			return nil, err
		}
		read += m
	}
	return buf, nil
}

// recv reads one complete WebSocket frame and returns its text payload.
// Handles ping/pong and close frames automatically.
func (ws *wsConn) recv() (string, error) {
	header, err := ws.recvExact(2)
	if err != nil {
		return "", err
	}

	opcode := header[0] & 0x0F
	isMasked := header[1]&0x80 != 0
	length := int(header[1] & 0x7F)

	switch length {
	case 126:
		ext, err := ws.recvExact(2)
		if err != nil {
			return "", err
		}
		length = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext, err := ws.recvExact(8)
		if err != nil {
			return "", err
		}
		length = int(binary.BigEndian.Uint64(ext))
	}

	var payload []byte
	if isMasked {
		mask, err := ws.recvExact(4)
		if err != nil {
			return "", err
		}
		raw, err := ws.recvExact(length)
		if err != nil {
			return "", err
		}
		payload = make([]byte, length)
		for i, b := range raw {
			payload[i] = b ^ mask[i%4]
		}
	} else {
		payload, err = ws.recvExact(length)
		if err != nil {
			return "", err
		}
	}

	switch opcode {
	case 0x08: // Close
		ws.closed = true
		return "", fmt.Errorf("connection closed by server")
	case 0x09: // Ping → Pong
		ws.sendPong(payload)
		return ws.recv()
	case 0x0A: // Pong (ignore)
		return ws.recv()
	default: // Text or binary
		return string(payload), nil
	}
}

// sendPong sends a pong frame echoing the ping payload.
func (ws *wsConn) sendPong(payload []byte) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	mask := make([]byte, 4)
	rand.Read(mask)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	frame := make([]byte, 0, 2+len(mask)+len(masked))
	frame = append(frame, 0x8A) // FIN + pong
	frame = append(frame, byte(0x80|len(payload)))
	frame = append(frame, mask...)
	frame = append(frame, masked...)
	_, _ = ws.conn.Write(frame)
}

// close sends a close frame and shuts down the socket.
func (ws *wsConn) close() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.closed {
		return
	}
	ws.closed = true
	mask := make([]byte, 4)
	rand.Read(mask)
	_, _ = ws.conn.Write(append([]byte{0x88, 0x80}, mask...))
	_ = ws.conn.Close()
}

// ── OBS authentication ─────────────────────────────────────────────────────

// computeAuth computes the OBS WebSocket authentication response string.
func computeAuth(password, salt, challenge string) string {
	h1 := sha256.Sum256([]byte(password + salt))
	s1 := base64.StdEncoding.EncodeToString(h1[:])
	h2 := sha256.Sum256([]byte(s1 + challenge))
	return base64.StdEncoding.EncodeToString(h2[:])
}

// ── OBS request/response ────────────────────────────────────────────────────

type obsPending struct {
	ch chan map[string]any
}

var (
	obsMid   int
	obsPend  = map[string]*obsPending{}
	obsPLock sync.Mutex
)

// obsRequest sends a request to OBS and blocks until the response arrives (5s timeout).
func obsRequest(ws *wsConn, requestType string) map[string]any {
	obsPLock.Lock()
	obsMid++
	rid := fmt.Sprintf("%d", obsMid)
	p := &obsPending{ch: make(chan map[string]any, 1)}
	obsPend[rid] = p
	obsPLock.Unlock()

	msg, _ := json.Marshal(map[string]any{
		"op": 6,
		"d": map[string]any{
			"requestType": requestType,
			"requestId":   rid,
			"requestData": map[string]any{},
		},
	})
	_ = ws.send(string(msg))

	select {
	case res := <-p.ch:
		return res
	case <-time.After(5 * time.Second):
		obsPLock.Lock()
		delete(obsPend, rid)
		obsPLock.Unlock()
		return nil
	}
}

// obsRecvLoop continuously reads messages from OBS and dispatches responses.
func obsRecvLoop(ws *wsConn) {
	for !ws.closed {
		raw, err := ws.recv()
		if err != nil {
			break
		}

		var msg map[string]any
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}

		op, _ := msg["op"].(float64)
		if int(op) == 7 {
			d, _ := msg["d"].(map[string]any)
			if d == nil {
				continue
			}
			rid, _ := d["requestId"].(string)
			obsPLock.Lock()
			if p, ok := obsPend[rid]; ok {
				delete(obsPend, rid)
				status, _ := d["requestStatus"].(map[string]any)
				if status != nil {
					if result, ok := status["result"].(bool); ok && result {
						p.ch <- d
					} else {
						p.ch <- nil
					}
				} else {
					p.ch <- nil
				}
			}
			obsPLock.Unlock()
		}
	}
}

// obsPollLoop continuously polls OBS for stats and stream status.
// Computes a rolling 5-second bitrate average from outputBytes deltas.
func obsPollLoop(ws *wsConn, s *state.OBSState) {
	fmt.Println("✓ OBS connected")

	type sample struct {
		t     float64
		bytes float64
	}
	var window []sample

	for !ws.closed {
		statsResp := obsRequest(ws, "GetStats")
		streamResp := obsRequest(ws, "GetStreamStatus")

		if statsResp != nil && streamResp != nil {
			statsData, _ := statsResp["responseData"].(map[string]any)
			streamData, _ := streamResp["responseData"].(map[string]any)

			s.Mu.Lock()
			s.Stats = statsData
			s.Stream = streamData

			active, _ := streamData["outputActive"].(bool)
			if active {
				now := float64(time.Now().UnixMilli()) / 1000.0
				total, _ := streamData["outputBytes"].(float64)
				window = append(window, sample{now, total})

				// Keep only samples within the last 5 seconds
				cutoff := now - 5.0
				for len(window) > 1 && window[0].t < cutoff {
					window = window[1:]
				}

				if len(window) >= 2 {
					dt := window[len(window)-1].t - window[0].t
					db := window[len(window)-1].bytes - window[0].bytes
					if dt > 0 {
						kbps := math.Round((db * 8) / dt / 1000)
						s.Kbps = &kbps
					}
				} else {
					s.Kbps = nil
				}
			} else {
				window = nil
				s.Kbps = nil
			}
			s.Mu.Unlock()
		}

		time.Sleep(1 * time.Second)
	}
}

// Run is the main OBS connection loop (blocking, runs in a goroutine).
// Establishes a WebSocket connection, handles authentication, then polls.
// Reconnects automatically with a 3-second backoff.
func Run(password string, s *state.OBSState) {
	for {
		ws, err := wsConnect(host, port)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		func() {
			defer func() {
				s.Mu.Lock()
				s.Connected = false
				s.Mu.Unlock()
				ws.close()
			}()

			// Read Hello message (op 0)
			raw, err := ws.recv()
			if err != nil {
				return
			}

			var msg map[string]any
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				return
			}
			op, _ := msg["op"].(float64)
			if int(op) != 0 {
				return
			}

			d, _ := msg["d"].(map[string]any)
			identify := map[string]any{
				"op": 1,
				"d": map[string]any{
					"rpcVersion":         1,
					"eventSubscriptions": 0,
				},
			}

			if auth, ok := d["authentication"].(map[string]any); ok {
				if password == "" {
					return
				}
				salt, _ := auth["salt"].(string)
				challenge, _ := auth["challenge"].(string)
				identifyD := identify["d"].(map[string]any)
				identifyD["authentication"] = computeAuth(password, salt, challenge)
			}

			identifyJSON, _ := json.Marshal(identify)
			_ = ws.send(string(identifyJSON))

			// Read Identified response (op 2)
			raw, err = ws.recv()
			if err != nil {
				return
			}
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				return
			}
			op, _ = msg["op"].(float64)
			if int(op) != 2 {
				return
			}

			s.Mu.Lock()
			s.Connected = true
			s.Mu.Unlock()

			go obsRecvLoop(ws)
			obsPollLoop(ws, s)
		}()

		time.Sleep(3 * time.Second)
		fmt.Println("↻ OBS reconnecting...")
	}
}
