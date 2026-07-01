// Package ws implements the realtime layer: a hand-rolled RFC 6455 WebSocket server
// (deviation from spec's gorilla/websocket — see progress.md Section 0), the connection
// hub, presence registry, challenge manager, and message dispatch (Section 3.4-3.6, 4).
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes per RFC 6455 section 5.2.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Conn is a minimal WebSocket connection: text-frame read/write, ping/pong, close.
// Safe for one concurrent reader and one concurrent writer (matches this app's usage:
// one goroutine reads, the hub writes via a per-connection send channel).
type Conn struct {
	rwc net.Conn
	br  *bufio.Reader
	bw  *bufio.Writer
	mu  sync.Mutex // guards writes
}

// Upgrade performs the WebSocket handshake (RFC 6455 section 4.2.1) over an existing
// HTTP request, returning a *Conn on success. The caller must have already
// authenticated the request (session cookie) before calling this.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer does not support hijacking")
	}
	rwc, buf, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	accept := computeAcceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := buf.Writer.WriteString(resp); err != nil {
		rwc.Close()
		return nil, err
	}
	if err := buf.Writer.Flush(); err != nil {
		rwc.Close()
		return nil, err
	}

	return &Conn{
		rwc: rwc,
		br:  buf.Reader,
		bw:  buf.Writer,
	}, nil
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsMagicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func (c *Conn) readFrame() (*frame, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(c.br, head); err != nil {
		return nil, err
	}
	fin := head[0]&0x80 != 0
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7f)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(ext)
	}
	if length > 16*1024*1024 { // 16MB safety cap
		return nil, errors.New("frame too large")
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return &frame{fin: fin, opcode: opcode, payload: payload}, nil
}

// ReadMessage reads one complete (possibly fragmented) text/binary message, handling
// ping/pong/close control frames transparently. Returns io.EOF-wrapped errors on close.
func (c *Conn) ReadMessage() ([]byte, error) {
	var assembled []byte
	var msgOpcode byte
	for {
		fr, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch fr.opcode {
		case opPing:
			c.writeFrame(opPong, fr.payload)
			continue
		case opPong:
			continue
		case opClose:
			c.writeFrame(opClose, nil)
			return nil, io.EOF
		case opContinuation:
			assembled = append(assembled, fr.payload...)
		case opText, opBinary:
			msgOpcode = fr.opcode
			assembled = append(assembled[:0], fr.payload...)
		}
		if fr.fin {
			break
		}
	}
	_ = msgOpcode
	return assembled, nil
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var head []byte
	first := byte(0x80) | opcode // FIN=1
	length := len(payload)

	switch {
	case length <= 125:
		head = []byte{first, byte(length)}
	case length <= 65535:
		head = make([]byte, 4)
		head[0] = first
		head[1] = 126
		binary.BigEndian.PutUint16(head[2:], uint16(length))
	default:
		head = make([]byte, 10)
		head[0] = first
		head[1] = 127
		binary.BigEndian.PutUint64(head[2:], uint64(length))
	}
	// Server frames are never masked (client-to-server must be masked; server-to-client must not be).
	if _, err := c.bw.Write(head); err != nil {
		return err
	}
	if _, err := c.bw.Write(payload); err != nil {
		return err
	}
	return c.bw.Flush()
}

func (c *Conn) WriteMessage(data []byte) error {
	return c.writeFrame(opText, data)
}

func (c *Conn) WritePing() error {
	return c.writeFrame(opPing, []byte("ping"))
}

func (c *Conn) Close() error {
	c.writeFrame(opClose, nil)
	return c.rwc.Close()
}

func (c *Conn) SetReadDeadline(t time.Time) error  { return c.rwc.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.rwc.SetWriteDeadline(t) }
