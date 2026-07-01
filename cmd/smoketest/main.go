// Scripted two-client smoke test: registers two users, connects both over WS,
// challenges/accepts, readies up, and plays through steal/guess actions using
// whatever object IDs the server actually deals (read from phase_update payloads)
// until match_complete arrives. Exits nonzero on any protocol violation/timeout.
//
// Run with: go run ./cmd/smoketest (server must already be running on :8080)
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

type envelope map[string]any

func register(name, secret string) (userID, cookie string) {
	body, _ := json.Marshal(map[string]string{"gameName": name, "secretNumber": secret})
	req, _ := http.NewRequest("POST", "http://localhost:8080/api/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	must(err)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		panic(fmt.Sprintf("register failed: %d %s", resp.StatusCode, b))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "shift_session" {
			cookie = c.Value
		}
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	return out["id"], cookie
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// clientConn is a minimal client-side WebSocket connection (mirrors the framing in
// internal/ws/websocket.go but from the client's perspective: sent frames must be
// masked, received frames from the server must not be).
type clientConn struct {
	conn net.Conn
	br   *bufio.Reader
}

func rawDial(cookie string) *clientConn {
	conn, err := net.Dial("tcp", "localhost:8080")
	must(err)
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: localhost:8080\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Cookie: shift_session=" + cookie + "\r\n\r\n"
	_, err = conn.Write([]byte(req))
	must(err)

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	must(err)
	if !bytesContains(statusLine, "101") {
		panic("handshake failed: " + statusLine)
	}
	for {
		line, err := br.ReadString('\n')
		must(err)
		if line == "\r\n" {
			break
		}
	}
	return &clientConn{conn: conn, br: br}
}

func bytesContains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func (c *clientConn) WriteMessage(payload []byte) error {
	var head []byte
	first := byte(0x80) | 0x1 // FIN + text opcode
	length := len(payload)
	switch {
	case length <= 125:
		head = []byte{first, byte(length) | 0x80}
	case length <= 65535:
		head = make([]byte, 4)
		head[0] = first
		head[1] = 126 | 0x80
		binary.BigEndian.PutUint16(head[2:], uint16(length))
	default:
		head = make([]byte, 10)
		head[0] = first
		head[1] = 127 | 0x80
		binary.BigEndian.PutUint64(head[2:], uint64(length))
	}
	var maskKey [4]byte
	rand.Read(maskKey[:])
	masked := make([]byte, length)
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	if _, err := c.conn.Write(head); err != nil {
		return err
	}
	if _, err := c.conn.Write(maskKey[:]); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

func (c *clientConn) ReadMessage() ([]byte, error) {
	for {
		head := make([]byte, 2)
		if _, err := io.ReadFull(c.br, head); err != nil {
			return nil, err
		}
		opcode := head[0] & 0x0f
		length := uint64(head[1] & 0x7f)
		switch length {
		case 126:
			ext := make([]byte, 2)
			io.ReadFull(c.br, ext)
			length = uint64(binary.BigEndian.Uint16(ext))
		case 127:
			ext := make([]byte, 8)
			io.ReadFull(c.br, ext)
			length = binary.BigEndian.Uint64(ext)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}
		if opcode == 0x9 { // ping -> pong
			c.writePong(payload)
			continue
		}
		if opcode == 0x8 { // close
			return nil, fmt.Errorf("closed")
		}
		if opcode == 0xA { // pong, ignore
			continue
		}
		return payload, nil
	}
}

func (c *clientConn) writePong(payload []byte) {
	first := byte(0x80) | 0xA
	head := []byte{first, byte(len(payload)) | 0x80}
	var maskKey [4]byte
	rand.Read(maskKey[:])
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	c.conn.Write(head)
	c.conn.Write(maskKey[:])
	c.conn.Write(masked)
}

func main() {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	aID, aCookie := register("SmokeA"+suffix, "111111")
	bID, bCookie := register("SmokeB"+suffix, "222222")
	fmt.Println("registered", aID, bID)

	connA := rawDial(aCookie)
	connB := rawDial(bCookie)
	fmt.Println("both sockets upgraded")

	msgsA := make(chan envelope, 100)
	msgsB := make(chan envelope, 100)
	go pump(connA, msgsA)
	go pump(connB, msgsB)

	send(connA, "join_lobby", map[string]any{})
	send(connB, "join_lobby", map[string]any{})
	time.Sleep(300 * time.Millisecond)
	drain(msgsA)
	drain(msgsB)

	send(connA, "send_challenge", map[string]any{"targetUserId": bID, "matchType": "ranked", "difficulty": "easy", "mode": "standard"})
	chal := waitFor(msgsB, "challenge_received", 3*time.Second)
	fmt.Println("B received challenge:", chal["challengeId"])

	send(connB, "respond_challenge", map[string]any{"challengeId": chal["challengeId"], "accept": true})
	foundA := waitFor(msgsA, "match_found", 3*time.Second)
	foundB := waitFor(msgsB, "match_found", 3*time.Second)
	roomID := foundA["roomId"].(string)
	fmt.Println("room:", roomID, foundB["roomId"])

	send(connA, "set_ready", map[string]any{"roomId": roomID})
	send(connB, "set_ready", map[string]any{"roomId": roomID})

	rounds := 0
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case m := <-msgsA:
			handlePhase(connA, roomID, "A", m)
			if m["type"] == "round_result" {
				rounds++
			}
			if m["type"] == "match_complete" {
				fmt.Println("MATCH COMPLETE (seen by A):", m)
				fmt.Println("SMOKETEST PASSED — rounds:", rounds)
				return
			}
		case m := <-msgsB:
			handlePhase(connB, roomID, "B", m)
			if m["type"] == "match_complete" {
				fmt.Println("MATCH COMPLETE (seen by B):", m)
				fmt.Println("SMOKETEST PASSED — rounds:", rounds)
				return
			}
		}
	}
	fmt.Println("SMOKETEST TIMED OUT after", rounds, "rounds")
	os.Exit(1)
}

func handlePhase(c *clientConn, roomID, who string, m envelope) {
	switch m["type"] {
	case "phase_update":
		if m["phase"] == "stealing" && m["role"] == "stealer" {
			objs := m["opponentObjects"].([]any)
			first := objs[0].(map[string]any)
			id := int(first["id"].(float64))
			send(c, "submit_steal", map[string]any{"roomId": roomID, "objectId": id})
		}
		if m["phase"] == "guessing" && m["role"] == "guesser" {
			objs := m["yourObjects"].([]any)
			first := objs[0].(map[string]any)
			id := int(first["id"].(float64))
			send(c, "submit_guess", map[string]any{"roomId": roomID, "objectId": id})
		}
	}
}

func drain(ch chan envelope) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitFor(ch chan envelope, typ string, timeout time.Duration) envelope {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case m := <-ch:
			if m["type"] == typ {
				return m
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	panic("timed out waiting for " + typ)
}

func send(c *clientConn, typ string, data map[string]any) {
	payload, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	must(c.WriteMessage(payload))
}

func pump(c *clientConn, out chan envelope) {
	for {
		data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var m envelope
		if json.Unmarshal(data, &m) == nil {
			out <- m
		}
	}
}
