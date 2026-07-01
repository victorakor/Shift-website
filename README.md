# SHIFT — Web Server & Client

A full implementation of SHIFT (the "steal and guess" multiplayer game) per
`SHIFT_Website_Requirements_Document.md` and `SHIFT_UI_Design_Requirements.md`,
built in Go (stdlib-heavy backend) with a server-rendered + vanilla-JS frontend
implementing the "Vault Card" design system.

**Start here:** `progress.md` — full checklist of what's built, what's stubbed, and
why (this sandbox has no live Postgres and no Go-module-proxy access, so a few
spec'd third-party pieces were reimplemented on the stdlib — all documented there
with exact swap-back instructions).

## Requirements

- Go 1.22+ (no external modules needed — the whole backend is stdlib-only)

## Run it

```bash
cd shift-website
go run ./cmd/server
```

Then open **http://localhost:8080/register** in two different browser profiles
(or one normal + one incognito window) to register two accounts and play a real
match against yourself, or open one and use "Play vs Computer" from the home page.

Data persists to `shift_data.json` in the working directory (see progress.md —
this stands in for PostgreSQL in this environment). Delete that file to reset all
accounts/matches; the object catalog reseeds itself automatically on next boot.

## Project layout

```
cmd/server/main.go        — entrypoint, HTTP route wiring (net/http ServeMux)
cmd/smoketest/main.go     — scripted two-client WS test (see below)
internal/store/           — Store interface + JSON file-backed implementation
internal/auth/            — registration, recovery, sessions, hashing, rate limiting
internal/game/            — rank/level/difficulty config, object catalog, room actor
internal/lobby/           — challenge manager (create/respond/expire)
internal/ws/              — hand-rolled WebSocket server + connection hub
internal/api/             — leaderboard/profile/settings/catalog HTTP handlers
internal/webui/           — HTML template rendering
web/templates/            — server-rendered pages (register/home/lobby/room/…)
web/static/css/main.css   — full design-token system + Vault Card component
web/static/js/            — common.js, wsclient.js, ai.js (vs-Computer engine)
migrations/                — real PostgreSQL DDL + seed data (see progress.md)
```

## Testing the realtime flow

`cmd/smoketest` drives two real WebSocket connections through registration →
lobby → challenge → accept → ready-up → a full round of steal/guess, asserting
the server responds correctly at each step (no mocking — it's the same protocol
a browser tab uses). Run it against a live server:

```bash
go run ./cmd/server &        # start the server first
go run ./cmd/smoketest       # then run the scripted client
```

A full 10-round match takes a couple of minutes to play out automatically at
`easy` difficulty timings; the smoke test's own internal deadline is generous
enough to let a full match finish if you want to watch `match_complete` fire.

## Deploying to Railway

The app is a single dependency-free Go binary (see "no external modules" below),
so the Docker build is fast and needs no network access to a Go module proxy.

**Steps:**

1. Push this project to a GitHub repo (or use `railway up` from this directory
   with the Railway CLI).
2. In Railway, create a new project → **Deploy from GitHub repo** (or run
   `railway init` then `railway up` from this folder). Railway will detect the
   `Dockerfile` and `railway.json` automatically and build with Docker — no
   other config needed.
3. Railway sets `$PORT` automatically; the app already reads it
   (`cmd/server/main.go`), so no env var setup is required to get it running.
4. Once deployed, open the generated `*.up.railway.app` URL — `/register` is
   the entry point.

**About data persistence (important):** this build uses `internal/store.FileStore`
(a JSON file) in place of PostgreSQL — see progress.md Section 0 for why. Railway's
container filesystem is **ephemeral**: every redeploy wipes `/app/data`, so
accounts/matches will reset each time you push a new version. Two ways to handle
this while testing:

- **Fine for quick functional testing** — just re-register test accounts after
  each deploy, nothing else to configure.
- **Want persistence across deploys?** Attach a [Railway Volume](https://docs.railway.com/reference/volumes)
  mounted at `/app/data` in the service settings. The Dockerfile already sets
  `SHIFT_DATA_PATH=/app/data/shift_data.json` and creates that directory, so once
  a volume is mounted there, data survives redeploys automatically — no code
  changes needed.
- **Want the real thing?** Add a Railway PostgreSQL plugin and implement
  `store.Store` against it using `/migrations` (see "Moving to production" below)
  — this is the actual long-term path, the file store is a sandbox stand-in.

Session cookies automatically get the `Secure` flag when `RAILWAY_ENVIRONMENT`
is set (Railway sets this for you), since Railway terminates TLS at the edge.

WebSockets work out of the box on Railway — no special proxy config needed.

## No external modules

`go.mod` has zero `require` lines — the entire backend is Go standard library
only (see progress.md Section 0 for what was hand-rolled and why: a WebSocket
server, PBKDF2 password hashing, UUID generation). This means `go build` never
touches the network, which is convenient for Docker builds and CI alike.



1. **Database:** stand up PostgreSQL, run `migrations/0001_init.sql` then
   `migrations/0002_seed_object_catalog.sql`, implement `store.Store` against it
   (e.g. with `pgx`), and swap the one line in `cmd/server/main.go` that
   constructs `store.NewFileStore(...)`.
2. **WebSocket library:** swap `internal/ws/websocket.go`'s hand-rolled framing
   for `gorilla/websocket` if desired — same external behavior, just less code
   to maintain long-term. Not required; the current implementation is a full
   RFC 6455 server (text frames, fragmentation, ping/pong, masking).
3. **Password hashing:** swap `internal/auth/hash.go`'s PBKDF2 implementation for
   `golang.org/x/crypto/bcrypt` if you'd like to match the spec's exact library
   choice — functionally equivalent either way (see progress.md).
4. **TLS / reverse proxy:** put this behind Caddy or nginx for TLS termination in
   production; the app itself serves plain HTTP on `:8080`.

See `progress.md` for the complete, itemized checklist against both source spec
documents.
