// Package lobby implements the presence-adjacent, non-socket state: the challenge
// manager (Section 3.5). The WebSocket hub (internal/ws) owns actual socket I/O and
// calls into this manager for the data/expiry side of challenges.
package lobby

import (
	"errors"
	"sync"
	"time"

	"shift/internal/idgen"
)

type ChallengeStatus string

const (
	StatusPending  ChallengeStatus = "pending"
	StatusAccepted ChallengeStatus = "accepted"
	StatusDeclined ChallengeStatus = "declined"
	StatusExpired  ChallengeStatus = "expired"
)

type Challenge struct {
	ID           string
	ChallengerID string
	ChallengedID string
	Status       ChallengeStatus
	MatchType    string // ranked | casual | private
	Difficulty   string
	Mode         string
	CreatedAt    time.Time
	RoomID       string
}

var (
	ErrNotFound      = errors.New("challenge not found")
	ErrNotChallenged = errors.New("only the challenged player may respond")
	ErrSelfChallenge = errors.New("cannot challenge yourself")
)

const expirySeconds = 60

// Manager holds all in-flight challenges, guarded by a mutex (spec allows either a
// mutex-guarded map or a dedicated channel-owning goroutine; a mutex is used here for
// simplicity since challenge volume is low relative to in-room game actions).
type Manager struct {
	mu         sync.Mutex
	challenges map[string]*Challenge
	onExpire   func(c *Challenge)
}

func NewManager(onExpire func(c *Challenge)) *Manager {
	m := &Manager{
		challenges: map[string]*Challenge{},
		onExpire:   onExpire,
	}
	go m.sweepLoop()
	return m
}

func (m *Manager) Create(challengerID, challengedID, matchType, difficulty, mode string) (*Challenge, error) {
	if challengerID == challengedID {
		return nil, ErrSelfChallenge
	}
	c := &Challenge{
		ID:           idgen.New(),
		ChallengerID: challengerID,
		ChallengedID: challengedID,
		Status:       StatusPending,
		MatchType:    matchType,
		Difficulty:   difficulty,
		Mode:         mode,
		CreatedAt:    time.Now(),
	}
	m.mu.Lock()
	m.challenges[c.ID] = c
	m.mu.Unlock()
	return c, nil
}

func (m *Manager) Get(id string) (*Challenge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	return c, ok
}

// Respond marks a challenge accepted/declined. Only the challenged user may respond.
func (m *Manager) Respond(id, responderID string, accept bool) (*Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok || c.Status != StatusPending {
		return nil, ErrNotFound
	}
	if c.ChallengedID != responderID {
		return nil, ErrNotChallenged
	}
	if accept {
		c.Status = StatusAccepted
		c.RoomID = idgen.New()
	} else {
		c.Status = StatusDeclined
	}
	return c, nil
}

func (m *Manager) sweepLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var expired []*Challenge
		m.mu.Lock()
		for id, c := range m.challenges {
			if c.Status == StatusPending && now.Sub(c.CreatedAt) > expirySeconds*time.Second {
				c.Status = StatusExpired
				expired = append(expired, c)
				delete(m.challenges, id)
			}
		}
		m.mu.Unlock()
		for _, c := range expired {
			if m.onExpire != nil {
				m.onExpire(c)
			}
		}
	}
}
