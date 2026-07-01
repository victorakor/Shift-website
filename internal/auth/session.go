package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"shift/internal/store"
)

const CookieName = "shift_session"

// SessionManager signs/verifies session cookies. The cookie payload is
// "userID.sessionSecret" HMAC-signed with a server-wide signing key (serverKey).
// Because the per-user sessionSecret is embedded in what gets signed AND is
// rotated in the DB on logout, a copied/old cookie stops verifying immediately
// after logout — a plain "delete the cookie" alone wouldn't achieve that (Section 5.3).
type SessionManager struct {
	serverKey []byte
	st        store.Store
}

func NewSessionManager(st store.Store) (*SessionManager, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &SessionManager{serverKey: key, st: st}, nil
}

func NewSessionSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (sm *SessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, sm.serverKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// secureCookies reports whether the cookie's Secure flag should be set. Railway
// (and most PaaS hosts) terminate TLS at the edge and proxy plain HTTP to the app,
// so we can't check r.TLS — instead this checks for Railway's own env var, falling
// back to any explicit SHIFT_FORCE_SECURE_COOKIES override.
func secureCookies() bool {
	if os.Getenv("RAILWAY_ENVIRONMENT") != "" {
		return true
	}
	return os.Getenv("SHIFT_FORCE_SECURE_COOKIES") == "true"
}

// IssueCookie sets a long-lived (1yr), HttpOnly, SameSite=Lax session cookie for u.
func (sm *SessionManager) IssueCookie(w http.ResponseWriter, u *store.User) {
	payload := u.ID + "." + u.SessionSecret
	sig := sm.sign(payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().AddDate(1, 0, 0),
	})
}

func (sm *SessionManager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

var ErrInvalidSession = errors.New("invalid session")

// UserFromRequest resolves the authenticated user from the request's session cookie,
// verifying the signature and that the embedded session secret matches the user's
// *current* one in the store (so rotated/logged-out secrets are rejected).
func (sm *SessionManager) UserFromRequest(r *http.Request) (*store.User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, ErrInvalidSession
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidSession
	}
	payloadRaw, sig := parts[0], parts[1]
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadRaw)
	if err != nil {
		return nil, ErrInvalidSession
	}
	payload := string(payloadBytes)
	expectedSig := sm.sign(payload)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) != 1 {
		return nil, ErrInvalidSession
	}
	pieces := strings.SplitN(payload, ".", 2)
	if len(pieces) != 2 {
		return nil, ErrInvalidSession
	}
	userID, sessionSecret := pieces[0], pieces[1]
	u, err := sm.st.GetUserByID(userID)
	if err != nil || u.Deleted {
		return nil, ErrInvalidSession
	}
	if subtle.ConstantTimeCompare([]byte(sessionSecret), []byte(u.SessionSecret)) != 1 {
		return nil, ErrInvalidSession
	}
	return u, nil
}
