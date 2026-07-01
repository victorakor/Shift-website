package webui

import (
	"html/template"
	"net/http"
	"path/filepath"

	"shift/internal/game"
	"shift/internal/store"
)

type PageData struct {
	Title     string
	LoggedIn  bool
	User      *store.User
	RankColor string
	RankSlug  string
	RoomID    string
}

func NewPageData(u *store.User, title string) PageData {
	pd := PageData{Title: title}
	if u != nil {
		pd.LoggedIn = true
		pd.User = u
		for _, rk := range game.Ranks {
			if rk.Name == u.Rank {
				pd.RankColor = rk.Color
			}
		}
		pd.RankSlug = slugify(u.Rank)
	}
	return pd
}

func slugify(rank string) string {
	out := ""
	for _, c := range rank {
		if c == ' ' {
			out += "-"
		} else {
			out += string(c)
		}
	}
	return toLower(out)
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

type Renderer struct {
	templates map[string]*template.Template
}

func NewRenderer(dir string) (*Renderer, error) {
	pages := []string{"register", "home", "lobby", "room", "leaderboard", "profile", "settings", "play-computer"}
	r := &Renderer{templates: map[string]*template.Template{}}
	for _, p := range pages {
		t, err := template.ParseFiles(filepath.Join(dir, "layout.html"), filepath.Join(dir, p+".html"))
		if err != nil {
			return nil, err
		}
		r.templates[p] = t
	}
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, page string, data PageData) {
	t, ok := r.templates[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
