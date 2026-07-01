// Package store defines the persistence interface used by the rest of the app.
//
// Deviation from spec (documented in progress.md): the spec calls for PostgreSQL
// accessed via pgx/sqlc. This sandbox has no Postgres instance and no access to the
// Go module proxy, so FileStore below implements the same Store interface backed by
// a mutex-guarded JSON file with atomic writes. Swapping in a real PostgresStore that
// satisfies this interface (using the DDL in /migrations) requires no changes to any
// caller — only the one line in cmd/server/main.go that constructs the store.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrNameTaken  = errors.New("NAME_TAKEN")
)

// Store is the persistence interface every handler/room actor depends on.
type Store interface {
	// Users
	CreateUser(u *User) error
	GetUserByID(id string) (*User, error)
	GetUserByGameName(name string) (*User, error)
	UpdateUser(u *User) error
	ListLeaderboard(offset, limit int) ([]*User, int, error)

	// Matches
	InsertMatch(m *Match) error

	// ReportMatchResult atomically updates both users' stats/rank/level and inserts
	// the match row, all under a single lock + single save (Section 6.6's "single
	// transaction" requirement, adapted to the file-store backend).
	ReportMatchResult(m *Match, winner, loser *User) error


	// Catalog
	RandomObjects(category string, count int, exclude map[int]bool) ([]CatalogObject, error)
	RandomCategory() (string, error)
	Categories() []string
}

// FileStore is a JSON-file-backed Store implementation.
type FileStore struct {
	mu       sync.RWMutex
	path     string
	Users    map[string]*User        `json:"users"`
	Matches  []*Match                 `json:"matches"`
	Catalog  map[string][]CatalogObject `json:"catalog"`
}

func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{
		path:    path,
		Users:   map[string]*User{},
		Matches: []*Match{},
		Catalog: map[string][]CatalogObject{},
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, fs); err != nil {
			return nil, err
		}
	}
	return fs, nil
}

func (fs *FileStore) save() error {
	data, err := json.MarshalIndent(fs, "", "  ")
	if err != nil {
		return err
	}
	tmp := fs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, fs.path)
}

func (fs *FileStore) CreateUser(u *User) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	lname := strings.ToLower(u.GameName)
	for _, existing := range fs.Users {
		if strings.ToLower(existing.GameName) == lname {
			return ErrNameTaken
		}
	}
	fs.Users[u.ID] = u
	return fs.save()
}

func (fs *FileStore) GetUserByID(id string) (*User, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	u, ok := fs.Users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (fs *FileStore) GetUserByGameName(name string) (*User, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	lname := strings.ToLower(name)
	for _, u := range fs.Users {
		if strings.ToLower(u.GameName) == lname {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (fs *FileStore) UpdateUser(u *User) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.Users[u.ID]; !ok {
		return ErrNotFound
	}
	fs.Users[u.ID] = u
	return fs.save()
}

func (fs *FileStore) ListLeaderboard(offset, limit int) ([]*User, int, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	all := make([]*User, 0, len(fs.Users))
	for _, u := range fs.Users {
		if u.Deleted {
			continue
		}
		cp := *u
		all = append(all, &cp)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Wins != all[j].Wins {
			return all[i].Wins > all[j].Wins
		}
		return all[i].GameName < all[j].GameName
	})
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

func (fs *FileStore) InsertMatch(m *Match) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.Matches = append(fs.Matches, m)
	return fs.save()
}

// ReportMatchResult — see Store interface doc. winner/loser must be the caller's
// working copies; they are written back to fs.Users wholesale under the same lock
// as the match insert, and only saved to disk once.
func (fs *FileStore) ReportMatchResult(m *Match, winner, loser *User) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.Users[winner.ID]; !ok {
		return ErrNotFound
	}
	if _, ok := fs.Users[loser.ID]; !ok {
		return ErrNotFound
	}
	fs.Users[winner.ID] = winner
	fs.Users[loser.ID] = loser
	fs.Matches = append(fs.Matches, m)
	return fs.save()
}

func (fs *FileStore) RandomObjects(category string, count int, exclude map[int]bool) ([]CatalogObject, error) {
	fs.mu.RLock()
	pool := fs.Catalog[category]
	fs.mu.RUnlock()
	if len(pool) == 0 {
		return nil, ErrNotFound
	}
	available := make([]CatalogObject, 0, len(pool))
	for _, o := range pool {
		if exclude == nil || !exclude[o.ID] {
			available = append(available, o)
		}
	}
	if len(available) < count {
		return nil, errors.New("not enough objects in category pool")
	}
	shuffled := shuffleCatalog(available)
	return shuffled[:count], nil
}

func (fs *FileStore) RandomCategory() (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if len(fs.Catalog) == 0 {
		return "", ErrNotFound
	}
	cats := fs.Categories()
	return cats[randIntn(len(cats))], nil
}

func (fs *FileStore) Categories() []string {
	cats := make([]string, 0, len(fs.Catalog))
	for c := range fs.Catalog {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	return cats
}

// SeedCatalog loads the static object catalog (Section 3.3). Only writes if empty,
// so it's safe to call on every boot.
func (fs *FileStore) SeedCatalog(catalog map[string][]CatalogObject) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.Catalog) > 0 {
		return nil
	}
	fs.Catalog = catalog
	return fs.save()
}
