package store

import "time"

// User mirrors the `users` table (Section 3.1 of the technical requirements doc).
type User struct {
	ID                string    `json:"id"`
	GameName          string    `json:"game_name"`
	SecretHash        string    `json:"secret_number_hash"`
	SessionSecret     string    `json:"session_secret"` // rotated on logout to invalidate old cookies
	AvatarURL         string    `json:"avatar_url"`
	Wins              int       `json:"wins"`
	Losses            int       `json:"losses"`
	MatchesPlayed     int       `json:"matches_played"`
	Rank              string    `json:"rank"`
	Level             int       `json:"level"`
	WinRate           float64   `json:"win_rate"`
	FavoriteGameMode  string    `json:"favorite_game_mode"`
	SoundEnabled      bool      `json:"sound_enabled"`
	NotifyEnabled     bool      `json:"notify_enabled"`
	Deleted           bool      `json:"deleted"` // soft-delete/anonymize per Settings spec (7.9)
	CreatedAt         time.Time `json:"created_at"`
	LastActiveAt      time.Time `json:"last_active_at"`
}

// Match mirrors the `matches` table (Section 3.2) — audit log, server-written only.
type Match struct {
	ID          string    `json:"id"`
	PlayerAID   string    `json:"player_a_id"`
	PlayerBID   string    `json:"player_b_id"`
	WinnerID    string    `json:"winner_id"`
	LoserID     string    `json:"loser_id"`
	Ranked      bool      `json:"ranked"`
	Difficulty  string    `json:"difficulty"`
	Mode        string    `json:"mode"`
	FinalScoreA int       `json:"final_score_a"`
	FinalScoreB int       `json:"final_score_b"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
}

// CatalogObject mirrors the `object_catalog` table (Section 3.3).
type CatalogObject struct {
	ID       int    `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Emoji    string `json:"emoji"`
}
