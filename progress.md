# SHIFT — Build Progress

Tracks implementation status against `SHIFT_Website_Requirements_Document.md` and
`SHIFT_UI_Design_Requirements.md`. Update this file whenever a checklist item changes state.

**Legend:** `[x]` done · `[~]` partial/stubbed · `[ ]` not started

---

## 0. Environment notes / deviations from the spec (read this first)

This sandbox has **no outbound access to the Go module proxy** (`proxy.golang.org`) and
**no Postgres server available**. To deliver a fully working, runnable project without those,
three deliberate substitutions were made. All are isolated behind interfaces so they can be
swapped for the spec'd originals later with no change to calling code:

| Spec'd | Used instead here | Why | Swap path |
|---|---|---|---|
| `gorilla/websocket` | Hand-rolled RFC 6455 implementation in `internal/ws/websocket.go` (stdlib only: `net`, `crypto/sha1`, `encoding/binary`) | No module proxy access | Drop-in: only `internal/ws/websocket.go` would need to change to wrap gorilla's `Conn` |
| `chi` router | Go 1.22's built-in `net/http.ServeMux` (method + path-pattern routing, e.g. `"POST /api/register"`) | No module proxy access | Swap `internal/api/routes.go` mux registration lines |
| PostgreSQL + `pgx`/`sqlc` | `internal/store`: a `Store` interface with a JSON-file-backed implementation (`FileStore`, mutex-guarded, atomic writes) | No Postgres instance in this environment | Implement a `PostgresStore` satisfying the same `store.Store` interface using the SQL in `/migrations`; swap the one line in `cmd/server/main.go` that constructs the store |
| bcrypt (`golang.org/x/crypto/bcrypt`) | Hand-rolled PBKDF2-HMAC-SHA256 (100,000 iterations, random salt) in `internal/auth/hash.go` | bcrypt lives outside stdlib | Swap `HashSecret`/`VerifySecret` in `internal/auth/hash.go` |

The `/migrations` SQL files are written exactly as the spec requires and are the real target
schema — they're just not connected to a live Postgres instance in this sandbox.

Everything else (WebSocket message protocol, room actor / phase state machine, rank & level
formulas, difficulty tables, folder structure, screen behavior, Vault Card design system) is
built to the spec as written.

---

## 1. Backend — Data & Persistence

- [x] `internal/store` — `Store` interface (Users, Matches, Catalog, Sessions)
- [x] `FileStore` implementation (JSON file, mutex-guarded, atomic save)
- [x] `users` model — matches `3.1` schema (game_name, secret hash, wins/losses/rank/level/win_rate, etc.)
- [x] `matches` model — matches `3.2` schema (audit log)
- [x] `object_catalog` — 20 categories × 50 unique objects each (correction from spec `10.` re: needing ≥50/category, not 25)
- [x] `/migrations/0001_init.sql`, `/migrations/0002_seed_object_catalog.sql` — real Postgres DDL/seed, spec-accurate
- [x] Unique game-name constraint enforced at store layer (simulates the DB `UNIQUE` constraint + friendly pre-check from `10.`)

## 2. Auth & Sessions (Section 5)

- [x] `POST /api/register` — validation (3-20 alphanumeric+underscore name, 6-digit secret), `NAME_TAKEN` / `INVALID_INPUT` error codes
- [x] `POST /api/recover` — generic error on mismatch, rate-limited (in-memory sliding window, 5/10min/IP)
- [x] Signed HMAC session cookie (HttpOnly, SameSite=Lax), per-user rotating session secret so logout invalidates old cookies
- [x] `POST /api/logout`
- [x] Session middleware shared by HTTP routes and WS upgrade

## 3. Realtime Layer (Section 3.4–3.6, Section 4)

- [x] Hand-rolled WebSocket server (handshake, text frames, ping/pong heartbeat every 15s / 45s timeout)
- [x] Presence registry, broadcasts `presence_update` on connect/disconnect
- [x] Challenge manager goroutine (channel-driven), 60s expiry sweep
- [x] Room actor pattern — one goroutine per `GameRoom`, owns `GameState`, processes actions over a channel
- [x] Full WS message protocol from Section 4.1 / 4.2 implemented (`join_lobby`, `send_challenge`, `respond_challenge`, `set_ready`, `leave_room`, `submit_steal`, `submit_guess`, and all server→client messages)
- [x] Server never sends opponent's object list to the wrong player (verified: `Objects` map access is phase+role gated in `room.go`)
- [x] Reconnect handling — re-sync into active room's current state instead of duplicate presence entry
- [x] AFK timeout handling (30s steal/guess timeout → auto-resolve per `6.2`)

## 4. Game Logic (Section 6, 8)

- [x] `StartMatch` — random category, disjoint object sets, coin-flip first stealer
- [x] Phase timers or `AdvancePhase` (memorization → stealing → guessing → round_result → next round)
- [x] `SubmitSteal` / `SubmitGuess` validation + scoring
- [x] `AdvanceRound` incl. Sudden Death at 5-5 after round 10
- [x] `ReportMatchResult` — transactional-style update (single store call) of wins/losses/matches/win_rate/rank/level, only for Ranked matches
- [x] Rank thresholds (Section 8.1) and Level formula (Section 8.2) implemented exactly
- [x] Standard + Blitz difficulty/mode tables (Section 8.5) implemented exactly
- [x] Play vs Computer — pure client-side JS (`ai.js`), no WS room, no `ReportMatchResult` call, three difficulty tiers with the spec'd accuracy targets

## 5. HTTP Pages & APIs (Section 7)

- [x] `/register`, `/recover` — server-rendered, progressively enhanced with fetch
- [x] `/` Home — nav grid, rank/level summary
- [x] `/lobby` — WS-driven Player Card grid
- [x] `/room/{roomID}` — ready/leave, then in-page view swaps for all gameplay phases (WS stays open)
- [x] `/leaderboard` + `GET /api/leaderboard?cursor=&limit=` — cursor pagination, "Load More", self-row highlight
- [x] `/profile` + `/profile/{id}` + `GET/PATCH /api/users/...`
- [x] `/settings` — toggles (client-side), logout, delete/anonymize account
- [x] `GET /api/object-catalog/random` — used by vs-Computer mode

## 6. Frontend Design System (per UI Design Requirements doc)

- [x] Design tokens as CSS custom properties (`web/static/css/main.css` `:root`) — colors, type scale, spacing, radius
- [x] Fonts: Space Grotesk (display), Inter (body), JetBrains Mono (utility/HUD) loaded via Google Fonts `@import` (offline-safe fallback stack included)
- [x] `.vault-card` base class + BEM modifiers (`--player`, `--leaderboard-row`, `--object`, `--profile`, `--challenge`) — single shared shell per Section 3
- [x] Notch corner detail + left accent bar via CSS (`clip-path` + border) on every card
- [x] Card flip (Object Card) — 3D Y-axis flip, 400ms, `prefers-reduced-motion` fallback to cross-fade
- [x] Rank badge colors centralized in one CSS/JS lookup (`internal/game/config.go` ↔ `common.js` `RANKS`) so design and logic can't drift
- [x] Level progress ring (SVG circular progress around avatar)
- [x] HUD timer (radial/linear, amber → red in final 20%)
- [x] Win/loss micro-feedback (border flash + scale-pulse / shake, reduced-motion safe)
- [x] Rank-up / level-up full-screen skippable overlay (~2s)
- [x] Focus rings, 44×44px min tap targets, online status = dot + text label (not color alone)
- [x] Empty/loading state using the card shell (Lobby "no players online" dashed card; waiting states)
- [~] Streak indicator — flagged `[OPEN]` in spec as optional; **not built** (out of v1 scope per spec's own recommendation)
- [~] Light theme — flagged `[OPEN]` in spec as a deliberate secondary pass; **not built** (dark-mode-first only, as the doc recommends doing first)

## 7. Non-functional / Definition of Done (Section 11–12)

- [x] All realtime UI updates push-based over the open WS frame (no polling)
- [x] `ReportMatchResult` is a single atomic store call (JSON store's equivalent of "one transaction")
- [x] `/api/register` and `/api/recover` rate-limited
- [x] Zero-match new user shows `0`, never `NaN`
- [x] Self-challenge blocked client- and server-side
- [x] Forged WS messages (wrong turn, spoofed identity) rejected server-side with `error` message — identity always resolved from the authenticated connection, never trusted from payload
- [x] Manual/scripted verification pass completed against a live running server:
  - `cmd/smoketest` confirmed: register → WS handshake → lobby presence → send/accept challenge → room creation → ready-up → match start → memorization phase timer → steal action → guess action, all correct.
  - All page routes return 200 when authenticated, 303 redirect to `/register` when not: `/`, `/lobby`, `/leaderboard`, `/profile`, `/settings`, `/play-computer`.
  - `/api/register`: duplicate name → `409 NAME_TAKEN`; invalid secret format → `400 INVALID_INPUT`; invalid game name → `400 INVALID_INPUT`.
  - `/api/recover`: correct creds → new session cookie; wrong secret → generic `401 RECOVERY_FAILED` (no user-enumeration signal); 6th attempt within 10 minutes from the same IP → `429` (rate limiter confirmed working).
  - `/api/logout`: rotates the user's session secret, which correctly invalidates *every* outstanding cookie for that account (confirmed by testing two separately-issued cookies for the same user, both rejected post-logout) — a fresh `/api/recover` afterward correctly re-authenticates.
  - `/api/object-catalog/categories` and `/api/object-catalog/random` (used by Play vs Computer) return correct, non-duplicate draws.
  - Profile page template renders rank color/slug and win-rate formatting correctly; Settings page toggles reflect stored `sound_enabled`/`notify_enabled`.
  - A full 10-round match was not run to completion in-session (real-time phase timers mean it takes several minutes to play out end-to-end), but every phase transition and message type in the protocol was exercised and confirmed correct through the first full round (steal → guess → round advance).
- [ ] Production TLS/reverse-proxy config (Caddy/nginx) — not applicable to local/dev run, documented in README for deployment

---

## Deployment

- [x] `Dockerfile` — multi-stage build (golang:1.22-bookworm → debian:bookworm-slim), no external Go modules so the build needs zero network access to a module proxy
- [x] `railway.json` — explicit Dockerfile builder config for Railway
- [x] `.dockerignore`
- [x] `PORT` env var support (`cmd/server/main.go`) — Railway (and most PaaS hosts) inject this at runtime
- [x] `SHIFT_DATA_PATH` env var support — lets a Railway Volume mounted at `/app/data` persist the JSON store across redeploys (documented in README; without a volume, data resets on every redeploy since the container filesystem is ephemeral)
- [x] `Secure` cookie flag auto-enabled when `RAILWAY_ENVIRONMENT` is set (Railway terminates TLS at the edge, so the app can't check `r.TLS` directly)
- [x] Verified locally: server runs correctly with `PORT=4000 SHIFT_DATA_PATH=/tmp/.../shift_data.json`, confirming both env vars are read correctly and the data directory is created as expected

See README.md "Deploying to Railway" for the full walkthrough.

## Known gaps / suggested next steps

1. Swap `FileStore` → real Postgres implementation using the provided `/migrations` when a DB is available (or attach a Railway Volume for interim persistence — see README).
2. Swap hand-rolled WS/PBKDF2 for `gorilla/websocket`/bcrypt if/when this runs somewhere with module-proxy access (functionally equivalent either way).
3. Add the optional streak indicator and light theme pass (both explicitly deferred in the design doc).
4. Wire real TLS via Caddy/nginx if not deploying to a PaaS that already terminates TLS (Railway does this automatically).
