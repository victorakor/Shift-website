// SHIFT web server entrypoint. See progress.md for the full status and the
// documented substitutions made for this sandbox (no Postgres, no Go module proxy).
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"shift/internal/api"
	"shift/internal/auth"
	"shift/internal/game"
	"shift/internal/store"
	"shift/internal/webui"
	"shift/internal/ws"
)

func main() {
	dataPath := os.Getenv("SHIFT_DATA_PATH")
	if dataPath == "" {
		dataPath = "shift_data.json"
	}
	st, err := store.NewFileStore(dataPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	if err := st.SeedCatalog(game.SeedCatalog()); err != nil {
		log.Fatalf("failed to seed catalog: %v", err)
	}

	sm, err := auth.NewSessionManager(st)
	if err != nil {
		log.Fatalf("failed to init session manager: %v", err)
	}
	authH := auth.NewHandlers(st, sm)
	apiH := &api.Handlers{Store: st, Auth: authH}
	hub := ws.NewHub(st)

	renderer, err := webui.NewRenderer("web/templates")
	if err != nil {
		log.Fatalf("failed to load templates: %v", err)
	}

	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Auth API
	mux.HandleFunc("POST /api/register", authH.Register)
	mux.HandleFunc("POST /api/recover", authH.Recover)
	mux.HandleFunc("POST /api/logout", authH.Logout)

	// User/leaderboard/catalog API (auth required)
	mux.HandleFunc("GET /api/leaderboard", authH.RequireAuthAPI(apiH.Leaderboard))
	mux.HandleFunc("GET /api/users/", authH.RequireAuthAPI(apiH.GetUser))
	mux.HandleFunc("PATCH /api/users/me", authH.RequireAuthAPI(apiH.UpdateMe))
	mux.HandleFunc("DELETE /api/users/me", authH.RequireAuthAPI(apiH.DeleteMe))
	mux.HandleFunc("GET /api/object-catalog/random", authH.RequireAuthAPI(apiH.RandomObjects))
	mux.HandleFunc("GET /api/object-catalog/categories", authH.RequireAuthAPI(apiH.Categories))

	// WebSocket (auth resolved from cookie, never trusted from payload)
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		u, err := sm.UserFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		hub.ServeWS(w, r, u.ID)
	})

	// Pages
	mux.HandleFunc("GET /register", func(w http.ResponseWriter, r *http.Request) {
		if _, err := sm.UserFromRequest(r); err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		renderer.Render(w, "register", webui.NewPageData(nil, "Register"))
	})

	mux.HandleFunc("GET /", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "home", webui.NewPageData(u, "Home"))
	}))

	mux.HandleFunc("GET /lobby", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "lobby", webui.NewPageData(u, "Lobby"))
	}))

	mux.HandleFunc("GET /play-computer", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "play-computer", webui.NewPageData(u, "Play vs Computer"))
	}))

	mux.HandleFunc("GET /room/", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		roomID := strings.TrimPrefix(r.URL.Path, "/room/")
		pd := webui.NewPageData(u, "Game Room")
		pd.RoomID = roomID
		renderer.Render(w, "room", pd)
	}))

	mux.HandleFunc("GET /leaderboard", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "leaderboard", webui.NewPageData(u, "Leaderboard"))
	}))

	mux.HandleFunc("GET /profile", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "profile", webui.NewPageData(u, "Profile"))
	}))

	mux.HandleFunc("GET /settings", authH.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r)
		renderer.Render(w, "settings", webui.NewPageData(u, "Settings"))
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("SHIFT server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
