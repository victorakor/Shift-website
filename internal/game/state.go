package game

import (
	"time"

	"shift/internal/store"
)

// Phase constants — Section 3.6 / 6.
const (
	PhaseMemorization = "memorization"
	PhaseStealing     = "stealing"
	PhaseGuessing     = "guessing"
	PhaseRoundResult  = "round_result"
	PhaseSuddenDeath  = "sudden_death"
	PhaseCompleted    = "completed"
)

// RoundResult is one entry of a room's round history.
type RoundResult struct {
	RoundNumber   int    `json:"roundNumber"`
	StealerID     string `json:"stealerId"`
	GuesserID     string `json:"guesserId"`
	StolenObject  store.CatalogObject `json:"stolenObject"`
	Correct       bool   `json:"correct"`
	PointTo       string `json:"pointTo"`
}

// GameState mirrors Section 3.6's GameState struct.
type GameState struct {
	Category          string
	RoundNumber       int
	Phase             string
	PhaseDeadline     time.Time
	StealerID         string
	GuesserID         string
	Objects           map[string][]store.CatalogObject // per-player object list — server-held only
	StolenObjectID    *int
	GuessesRemaining  map[string]int
	GuessedWrongIDs   map[string][]int
	Scores            map[string]int
	RoundHistory      []RoundResult
	SuddenDeath       bool
}
