package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"shift/internal/game"
	"shift/internal/idgen"
	"shift/internal/lobby"
	"shift/internal/store"
)

const (
	heartbeatInterval = 15 * time.Second
	pongTimeout       = 45 * time.Second
)

// Client is one connected browser tab.
type Client struct {
	UserID string
	conn   *Conn
	send   chan []byte
	hub    *Hub
	roomID string
	mu     sync.Mutex
}

// Hub owns all live connections, the presence registry (implicitly: connected
// clients ARE presence), the challenge manager, and active game rooms.
type Hub struct {
	st         store.Store
	mu         sync.RWMutex
	clients    map[string]*Client // userID -> client; a new connection replaces the old (Section 10 reconnect handling)
	rooms      map[string]*game.Room
	challenges *lobby.Manager
}

func NewHub(st store.Store) *Hub {
	h := &Hub{
		st:      st,
		clients: map[string]*Client{},
		rooms:   map[string]*game.Room{},
	}
	h.challenges = lobby.NewManager(func(c *lobby.Challenge) {
		h.sendTo(c.ChallengerID, "challenge_expired", map[string]any{"challengeId": c.ID})
	})
	return h
}

// ServeWS upgrades the connection and starts the read/write pumps. userID must
// already be resolved from the session cookie by the caller (never trust the client).
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, userID string) {
	conn, err := Upgrade(w, r)
	if err != nil {
		http.Error(w, "websocket upgrade failed", http.StatusBadRequest)
		return
	}
	c := &Client{UserID: userID, conn: conn, send: make(chan []byte, 32), hub: h}

	h.mu.Lock()
	// Reconnect handling (Section 10): replace any existing connection for this user
	// rather than creating a duplicate presence entry.
	if old, ok := h.clients[userID]; ok {
		old.conn.Close()
	}
	h.clients[userID] = c
	// Resync into an active room if one exists for this user.
	for roomID, room := range h.rooms {
		if room.PlayerA == userID || room.PlayerB == userID {
			c.roomID = roomID
			break
		}
	}
	h.mu.Unlock()

	go c.writePump()
	h.broadcastPresence()
	if c.roomID != "" {
		if room, ok := h.getRoom(c.roomID); ok {
			room.Actions <- game.Action{From: userID, Type: game.ActionResync}
		}
	}
	c.readPump()
}

func (h *Hub) getRoom(id string) (*game.Room, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rooms[id]
	return r, ok
}

func (h *Hub) removeClient(c *Client) {
	h.mu.Lock()
	if cur, ok := h.clients[c.UserID]; ok && cur == c {
		delete(h.clients, c.UserID)
	}
	h.mu.Unlock()
	close(c.send)
	h.broadcastPresence()
}

// --- Presence ---

type presenceEntry struct {
	UserID   string `json:"userId"`
	GameName string `json:"gameName"`
	Rank     string `json:"rank"`
	Wins     int    `json:"wins"`
	Avatar   string `json:"avatarUrl"`
}

func (h *Hub) broadcastPresence() {
	h.mu.RLock()
	ids := make([]string, 0, len(h.clients))
	for uid := range h.clients {
		ids = append(ids, uid)
	}
	h.mu.RUnlock()

	entries := make([]presenceEntry, 0, len(ids))
	for _, uid := range ids {
		u, err := h.st.GetUserByID(uid)
		if err != nil || u.Deleted {
			continue
		}
		entries = append(entries, presenceEntry{
			UserID: u.ID, GameName: u.GameName, Rank: u.Rank, Wins: u.Wins, Avatar: u.AvatarURL,
		})
	}
	h.broadcastAll("presence_update", map[string]any{"players": entries})
}

func (h *Hub) broadcastAll(msgType string, payload map[string]any) {
	data, err := encodeEnvelope(msgType, payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients {
		select {
		case c.send <- data:
		default:
		}
	}
}

func (h *Hub) sendTo(userID, msgType string, payload map[string]any) {
	data, err := encodeEnvelope(msgType, payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	c, ok := h.clients[userID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

// --- Read/write pumps ---

func (c *Client) writePump() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WritePing(); err != nil {
				return
			}
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		c.conn.Close()
		c.hub.removeClient(c)
		if c.roomID != "" {
			if room, ok := c.hub.getRoom(c.roomID); ok {
				select {
				case room.Actions <- game.Action{From: c.UserID, Type: game.ActionLeaveRoom}:
				default:
				}
			}
		}
	}()
	c.conn.SetReadDeadline(time.Now().Add(pongTimeout))
	for {
		data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(pongTimeout))
		var msg InboundMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		c.hub.dispatch(c, msg)
	}
}

// --- Dispatch: Section 4.2 client->server messages ---

func (h *Hub) dispatch(c *Client, msg InboundMessage) {
	switch msg.Type {
	case "join_lobby":
		h.broadcastPresence()

	case "send_challenge":
		var req struct {
			TargetUserID string `json:"targetUserId"`
			MatchType    string `json:"matchType"`
			Difficulty   string `json:"difficulty"`
			Mode         string `json:"mode"`
		}
		json.Unmarshal(msg.Data, &req)
		h.mu.RLock()
		_, targetOnline := h.clients[req.TargetUserID]
		h.mu.RUnlock()
		if !targetOnline {
			h.sendTo(c.UserID, "error", map[string]any{"code": "OPPONENT_OFFLINE", "message": "that player is no longer online"})
			return
		}
		ch, err := h.challenges.Create(c.UserID, req.TargetUserID, req.MatchType, req.Difficulty, req.Mode)
		if err != nil {
			h.sendTo(c.UserID, "error", map[string]any{"code": "CHALLENGE_FAILED", "message": err.Error()})
			return
		}
		challenger, _ := h.st.GetUserByID(c.UserID)
		h.sendTo(req.TargetUserID, "challenge_received", map[string]any{
			"challengeId":  ch.ID,
			"challengerId": challenger.ID,
			"gameName":     challenger.GameName,
			"rank":         challenger.Rank,
			"avatarUrl":    challenger.AvatarURL,
			"matchType":    ch.MatchType,
			"difficulty":   ch.Difficulty,
			"mode":         ch.Mode,
		})
		h.sendTo(c.UserID, "challenge_sent", map[string]any{"challengeId": ch.ID})

	case "respond_challenge":
		var req struct {
			ChallengeID string `json:"challengeId"`
			Accept      bool   `json:"accept"`
		}
		json.Unmarshal(msg.Data, &req)
		ch, err := h.challenges.Respond(req.ChallengeID, c.UserID, req.Accept)
		if err != nil {
			h.sendTo(c.UserID, "error", map[string]any{"code": "CHALLENGE_RESPONSE_FAILED", "message": err.Error()})
			return
		}
		if !req.Accept {
			h.sendTo(ch.ChallengerID, "challenge_declined", map[string]any{"challengeId": ch.ID})
			return
		}
		h.createRoom(ch)

	case "resync":
		h.forwardToRoom(c, msg.Data, game.ActionResync)
	case "set_ready":
		h.forwardToRoom(c, msg.Data, game.ActionSetReady)
	case "leave_room":
		h.forwardToRoom(c, msg.Data, game.ActionLeaveRoom)
	case "submit_steal":
		h.forwardToRoomWithObject(c, msg.Data, game.ActionSubmitSteal)
	case "submit_guess":
		h.forwardToRoomWithObject(c, msg.Data, game.ActionSubmitGuess)
	}
}

func (h *Hub) forwardToRoom(c *Client, data json.RawMessage, actionType game.ActionType) {
	var req struct {
		RoomID string `json:"roomId"`
	}
	json.Unmarshal(data, &req)
	room, ok := h.getRoom(req.RoomID)
	if !ok || (room.PlayerA != c.UserID && room.PlayerB != c.UserID) {
		h.sendTo(c.UserID, "error", map[string]any{"code": "NOT_IN_ROOM", "message": "you are not a participant in that room"})
		return
	}
	select {
	case room.Actions <- game.Action{From: c.UserID, Type: actionType}:
	default:
	}
}

func (h *Hub) forwardToRoomWithObject(c *Client, data json.RawMessage, actionType game.ActionType) {
	var req struct {
		RoomID   string `json:"roomId"`
		ObjectID int    `json:"objectId"`
	}
	json.Unmarshal(data, &req)
	room, ok := h.getRoom(req.RoomID)
	if !ok || (room.PlayerA != c.UserID && room.PlayerB != c.UserID) {
		h.sendTo(c.UserID, "error", map[string]any{"code": "NOT_IN_ROOM", "message": "you are not a participant in that room"})
		return
	}
	select {
	case room.Actions <- game.Action{From: c.UserID, Type: actionType, ObjectID: req.ObjectID}:
	default:
	}
}

// createRoom spins up a new Room actor + event-draining goroutine for an accepted challenge.
func (h *Hub) createRoom(ch *lobby.Challenge) {
	roomID := ch.RoomID
	if roomID == "" {
		roomID = idgen.New()
	}
	ranked := ch.MatchType == "ranked" || ch.MatchType == ""
	room := game.NewRoom(roomID, ch.ChallengerID, ch.ChallengedID, ranked, ch.Difficulty, ch.Mode, h.st)

	h.mu.Lock()
	h.rooms[roomID] = room
	h.mu.Unlock()

	go room.Run()
	go h.drainRoomEvents(room)

	challenger, _ := h.st.GetUserByID(ch.ChallengerID)
	challenged, _ := h.st.GetUserByID(ch.ChallengedID)

	h.sendTo(ch.ChallengerID, "match_found", map[string]any{
		"roomId":   roomID,
		"opponent": userSummary(challenged),
	})
	h.sendTo(ch.ChallengedID, "match_found", map[string]any{
		"roomId":   roomID,
		"opponent": userSummary(challenger),
	})

	h.mu.Lock()
	if cl, ok := h.clients[ch.ChallengerID]; ok {
		cl.roomID = roomID
	}
	if cl, ok := h.clients[ch.ChallengedID]; ok {
		cl.roomID = roomID
	}
	h.mu.Unlock()
}

func userSummary(u *store.User) map[string]any {
	if u == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": u.ID, "gameName": u.GameName, "rank": u.Rank, "level": u.Level,
		"avatarUrl": u.AvatarURL, "wins": u.Wins,
	}
}

func (h *Hub) drainRoomEvents(room *game.Room) {
	for ev := range room.Events {
		if ev.ToUserID == "" {
			h.sendTo(room.PlayerA, ev.Type, cloneMap(ev.Payload))
			h.sendTo(room.PlayerB, ev.Type, cloneMap(ev.Payload))
		} else {
			h.sendTo(ev.ToUserID, ev.Type, ev.Payload)
		}
		if ev.Type == "match_complete" {
			h.broadcastPresence() // reflect updated wins/rank immediately (Section 6.6)
		}
	}
	h.mu.Lock()
	delete(h.rooms, room.ID)
	h.mu.Unlock()
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var _ = log.Println // reserved for future structured logging
