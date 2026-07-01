package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"shift/internal/auth"
	"shift/internal/store"
)

type Handlers struct {
	Store store.Store
	Auth  *auth.Handlers
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// GET /api/leaderboard?cursor=&limit=
func (h *Handlers) Leaderboard(w http.ResponseWriter, r *http.Request) {
	cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 100 {
		limit = 50
	}
	users, total, err := h.Store.ListLeaderboard(cursor, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load leaderboard"})
		return
	}
	rows := make([]map[string]any, 0, len(users))
	for i, u := range users {
		rows = append(rows, map[string]any{
			"position": cursor + i + 1,
			"id":       u.ID, "gameName": u.GameName, "rank": u.Rank, "level": u.Level,
			"wins": u.Wins, "losses": u.Losses, "winRate": u.WinRate, "avatarUrl": u.AvatarURL,
		})
	}
	nextCursor := cursor + len(users)
	writeJSON(w, http.StatusOK, map[string]any{
		"rows": rows, "nextCursor": nextCursor, "hasMore": nextCursor < total, "total": total,
	})
}

// GET /api/users/{id}
func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if id == "" || id == "me" {
		u, ok := auth.UserFromContext(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		writeJSON(w, http.StatusOK, publicUser(u))
		return
	}
	u, err := h.Store.GetUserByID(id)
	if err != nil || u.Deleted {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, publicUser(u))
}

func publicUser(u *store.User) map[string]any {
	return map[string]any{
		"id": u.ID, "gameName": u.GameName, "rank": u.Rank, "level": u.Level,
		"wins": u.Wins, "losses": u.Losses, "matchesPlayed": u.MatchesPlayed,
		"winRate": u.WinRate, "avatarUrl": u.AvatarURL, "favoriteGameMode": u.FavoriteGameMode,
	}
}

// PATCH /api/users/me
func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req struct {
		AvatarURL        *string `json:"avatarUrl"`
		FavoriteGameMode *string `json:"favoriteGameMode"`
		SoundEnabled     *bool   `json:"soundEnabled"`
		NotifyEnabled    *bool   `json:"notifyEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed body"})
		return
	}
	if req.AvatarURL != nil {
		u.AvatarURL = *req.AvatarURL
	}
	if req.FavoriteGameMode != nil {
		u.FavoriteGameMode = *req.FavoriteGameMode
	}
	if req.SoundEnabled != nil {
		u.SoundEnabled = *req.SoundEnabled
	}
	if req.NotifyEnabled != nil {
		u.NotifyEnabled = *req.NotifyEnabled
	}
	if err := h.Store.UpdateUser(u); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save"})
		return
	}
	writeJSON(w, http.StatusOK, publicUser(u))
}

// DELETE /api/users/me — anonymizes rather than deletes (Section 7.9), keeps opponents' match history intact.
func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	u.Deleted = true
	u.GameName = "deleted_" + u.ID[:8]
	u.AvatarURL = ""
	if err := h.Store.UpdateUser(u); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete account"})
		return
	}
	h.Auth.Sessions.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/object-catalog/random?category=X&count=N — used by Play vs Computer (Section 6.7).
func (h *Handlers) RandomObjects(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	count, err := strconv.Atoi(r.URL.Query().Get("count"))
	if err != nil || count <= 0 {
		count = 10
	}
	if category == "" {
		cat, err := h.Store.RandomCategory()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no catalog available"})
			return
		}
		category = cat
	}
	objs, err := h.Store.RandomObjects(category, count, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"category": category, "objects": objs})
}

// GET /api/object-catalog/categories
func (h *Handlers) Categories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"categories": h.Store.Categories()})
}
