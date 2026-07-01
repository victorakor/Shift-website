package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"shift/internal/game"
	"shift/internal/idgen"
	"shift/internal/store"
)

var gameNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,20}$`)
var secretRe = regexp.MustCompile(`^\d{6}$`)

type Handlers struct {
	Store     store.Store
	Sessions  *SessionManager
	RecoverRL *RateLimiter
}

func NewHandlers(st store.Store, sm *SessionManager) *Handlers {
	return &Handlers{
		Store:     st,
		Sessions:  sm,
		RecoverRL: NewRateLimiter(10*time.Minute, 5),
	}
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Code: code, Message: msg})
}

type registerReq struct {
	GameName     string `json:"gameName"`
	SecretNumber string `json:"secretNumber"`
}

// POST /api/register — Section 5.1
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	if !gameNameRe.MatchString(req.GameName) {
		writeErr(w, http.StatusBadRequest, "INVALID_INPUT", "game name must be 3-20 alphanumeric/underscore characters")
		return
	}
	if !secretRe.MatchString(req.SecretNumber) {
		writeErr(w, http.StatusBadRequest, "INVALID_INPUT", "secret number must be exactly 6 digits")
		return
	}
	if _, err := h.Store.GetUserByGameName(req.GameName); err == nil {
		writeErr(w, http.StatusConflict, "NAME_TAKEN", "that game name is already taken")
		return
	}
	hash, err := HashSecret(req.SecretNumber)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SERVER_ERROR", "could not create account")
		return
	}
	u := &store.User{
		ID:            idgen.New(),
		GameName:      req.GameName,
		SecretHash:    hash,
		SessionSecret: NewSessionSecret(),
		Rank:          game.RankForWins(0),
		Level:         game.LevelForWins(0),
		SoundEnabled:  true,
		NotifyEnabled: true,
		CreatedAt:     time.Now(),
		LastActiveAt:  time.Now(),
	}
	if err := h.Store.CreateUser(u); err != nil {
		if err == store.ErrNameTaken {
			writeErr(w, http.StatusConflict, "NAME_TAKEN", "that game name is already taken")
			return
		}
		writeErr(w, http.StatusInternalServerError, "SERVER_ERROR", "could not create account")
		return
	}
	h.Sessions.IssueCookie(w, u)
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "gameName": u.GameName})
}

type recoverReq struct {
	GameName     string `json:"gameName"`
	SecretNumber string `json:"secretNumber"`
}

// POST /api/recover — Section 5.2
func (h *Handlers) Recover(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if !h.RecoverRL.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, try again later")
		return
	}
	var req recoverReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	genericErr := func() {
		writeErr(w, http.StatusUnauthorized, "RECOVERY_FAILED", "game name or secret number is incorrect")
	}
	u, err := h.Store.GetUserByGameName(req.GameName)
	if err != nil {
		genericErr()
		return
	}
	ok, err := VerifySecret(req.SecretNumber, u.SecretHash)
	if err != nil || !ok {
		genericErr()
		return
	}
	h.Sessions.IssueCookie(w, u)
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "gameName": u.GameName})
}

// POST /api/logout — Section 5.3. Rotates the user's session secret server-side so a
// copied cookie can't be replayed after logout, then clears the cookie client-side.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	u, err := h.Sessions.UserFromRequest(r)
	if err == nil {
		u.SessionSecret = NewSessionSecret()
		h.Store.UpdateUser(u)
	}
	h.Sessions.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type ctxKey string

const userCtxKey ctxKey = "user"

// RequireAuth is HTTP middleware that resolves the session and either attaches the
// user to the request context or redirects to /register (for page routes) — API
// routes use RequireAuthAPI instead, which returns JSON 401.
func (h *Handlers) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := h.Sessions.UserFromRequest(r)
		if err != nil {
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(withUser(r, u)))
	}
}

func (h *Handlers) RequireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := h.Sessions.UserFromRequest(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "please log in")
			return
		}
		next(w, r.WithContext(withUser(r, u)))
	}
}

func withUser(r *http.Request, u *store.User) context.Context {
	return context.WithValue(r.Context(), userCtxKey, u)
}

// UserFromContext retrieves the authenticated user attached by RequireAuth/RequireAuthAPI.
func UserFromContext(r *http.Request) (*store.User, bool) {
	u, ok := r.Context().Value(userCtxKey).(*store.User)
	return u, ok
}
