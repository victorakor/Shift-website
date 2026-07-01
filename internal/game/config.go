// Package game implements the authoritative server-side game logic:
// rank/level/scoring formulas (Section 8), difficulty/mode tables, the room actor
// state machine (Section 6), and the object catalog seed (Section 3.3).
package game

import "math"

// RankThreshold — Section 8.1. Order matters (ascending by MinWins).
type RankThreshold struct {
	Name    string
	MinWins int
	Color   string // kept in sync with web/static/css/main.css rank palette + common.js RANKS
}

var Ranks = []RankThreshold{
	{"Rookie", 0, "#8A8FA3"},
	{"Bronze", 10, "#C87F4A"},
	{"Silver", 30, "#C4C9D4"},
	{"Gold", 60, "#F4C542"},
	{"Platinum", 100, "#8FE3E0"},
	{"Diamond", 180, "#7FC7FF"},
	{"Master", 300, "#B48CFF"},
	{"Champion", 500, "#FF8CC6"},
	{"Legend", 750, "#FF6A4D"},
	{"Shift Master", 1000, "gradient"}, // animated gradient sweep, handled in CSS
}

// RankForWins implements Section 8.1.
func RankForWins(wins int) string {
	rank := Ranks[0].Name
	for _, r := range Ranks {
		if wins >= r.MinWins {
			rank = r.Name
		}
	}
	return rank
}

// LevelForWins implements Section 8.2: level = floor(totalWins / 5) + 1.
func LevelForWins(wins int) int {
	return int(math.Floor(float64(wins)/5.0)) + 1
}

// LevelProgress returns (winsIntoCurrentBand, winsNeededForBand) for the level ring UI.
func LevelProgress(wins int) (int, int) {
	return wins % 5, 5
}

// WinRate implements Section 8.3.
func WinRate(wins, matchesPlayed int) float64 {
	if matchesPlayed == 0 {
		return 0
	}
	rate := (float64(wins) / float64(matchesPlayed)) * 100
	return math.Round(rate*10) / 10
}

// DifficultyConfig — Section 8.5.
type DifficultyConfig struct {
	Objects        int
	MemorizeSecs   int
	GuessAttempts  int
}

var StandardDifficulty = map[string]DifficultyConfig{
	"easy":   {Objects: 10, MemorizeSecs: 10, GuessAttempts: 3},
	"medium": {Objects: 20, MemorizeSecs: 8, GuessAttempts: 2},
	"hard":   {Objects: 25, MemorizeSecs: 5, GuessAttempts: 1},
}

var BlitzDifficulty = map[string]DifficultyConfig{
	"easy":   {Objects: 10, MemorizeSecs: 10, GuessAttempts: 3},
	"medium": {Objects: 20, MemorizeSecs: 15, GuessAttempts: 2},
	"hard":   {Objects: 25, MemorizeSecs: 20, GuessAttempts: 1},
}

func ConfigFor(mode, difficulty string) DifficultyConfig {
	table := StandardDifficulty
	if mode == "blitz" {
		table = BlitzDifficulty
	}
	cfg, ok := table[difficulty]
	if !ok {
		return table["medium"]
	}
	return cfg
}

const (
	TotalRounds       = 10
	StealGuessAFKSecs = 30
	RoundResultPause  = 4 // seconds both clients see the round result before advancing
)
