package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type GameRecord struct {
	ID               int64     `json:"id"`
	FolderPath       string    `json:"folder_path"`
	ThreadID         int64     `json:"thread_id"`
	Approved         bool      `json:"approved"`
	Source           string    `json:"source"`
	Title            string    `json:"title"`
	Creator          string    `json:"creator"`
	Engine           string    `json:"engine"`
	Status           string    `json:"status"`
	Rating           float64   `json:"rating"`
	CoverURL         string    `json:"cover_url"`
	BannerURL        string    `json:"banner_url"`
	ThreadURL        string    `json:"thread_url"`
	CatalogVersion   string    `json:"catalog_version"`
	InstalledVersion string    `json:"installed_version"`
	DetectedEngine   string    `json:"detected_engine"`
	ExeHint          string    `json:"exe_hint"`
	CoverMode        string    `json:"cover_mode"` // "inherit", "f95_logo", "game_cover"
	CustomTitle      string    `json:"custom_title"`
	Ignored          bool      `json:"ignored"`
	LastFetchedAt    time.Time `json:"last_fetched_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if dbPath == "" {
		// In development mode (if config.yaml exists in cwd), use local ./f95rpc.db
		if fi, err := os.Stat("config.yaml"); err == nil && !fi.IsDir() {
			dbPath = "f95rpc.db"
		} else {
			appData := os.Getenv("APPDATA")
			if appData == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, err
				}
				appData = filepath.Join(home, "AppData", "Roaming")
			}
			dir := filepath.Join(appData, "f95rpc")
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
			dbPath = filepath.Join(dir, "f95rpc.db")
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for high concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite pragma: %w", err)
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS games (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		folder_path TEXT UNIQUE NOT NULL,
		thread_id INTEGER,
		approved INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL,
		title TEXT,
		creator TEXT,
		engine TEXT,
		status TEXT,
		rating REAL DEFAULT 0,
		cover_url TEXT,
		banner_url TEXT,
		thread_url TEXT,
		catalog_version TEXT,
		installed_version TEXT,
		detected_engine TEXT,
		exe_hint TEXT,
		cover_mode TEXT DEFAULT 'inherit',
		custom_title TEXT,
		ignored INTEGER NOT NULL DEFAULT 0,
		last_fetched_at INTEGER DEFAULT 0,
		created_at INTEGER DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER DEFAULT (strftime('%s', 'now'))
	);

	CREATE INDEX IF NOT EXISTS idx_games_folder_path ON games(folder_path);
	CREATE INDEX IF NOT EXISTS idx_games_thread_id ON games(thread_id);
	CREATE INDEX IF NOT EXISTS idx_games_approved ON games(approved);

	CREATE TABLE IF NOT EXISTS pending_matches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		folder_path TEXT UNIQUE NOT NULL,
		window_title TEXT,
		detected_engine TEXT,
		candidates_json TEXT,
		created_at INTEGER DEFAULT (strftime('%s', 'now'))
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

func NormalizePath(p string) string {
	cleaned := filepath.Clean(p)
	return strings.ToLower(cleaned)
}

func (s *Store) GetGameByFolder(folderPath string) (*GameRecord, error) {
	norm := NormalizePath(folderPath)
	row := s.db.QueryRow(`
		SELECT id, folder_path, thread_id, approved, source, title, creator, engine, status, rating,
		       cover_url, banner_url, thread_url, catalog_version, installed_version, detected_engine,
		       exe_hint, cover_mode, custom_title, ignored, last_fetched_at, created_at, updated_at
		FROM games WHERE folder_path = ?
	`, norm)

	var g GameRecord
	var approved, ignored int
	var lastFetched, createdAt, updatedAt int64

	err := row.Scan(
		&g.ID, &g.FolderPath, &g.ThreadID, &approved, &g.Source, &g.Title, &g.Creator, &g.Engine, &g.Status, &g.Rating,
		&g.CoverURL, &g.BannerURL, &g.ThreadURL, &g.CatalogVersion, &g.InstalledVersion, &g.DetectedEngine,
		&g.ExeHint, &g.CoverMode, &g.CustomTitle, &ignored, &lastFetched, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	g.Approved = approved == 1
	g.Ignored = ignored == 1
	g.LastFetchedAt = time.Unix(lastFetched, 0)
	g.CreatedAt = time.Unix(createdAt, 0)
	g.UpdatedAt = time.Unix(updatedAt, 0)

	return &g, nil
}

func (s *Store) UpsertGame(g *GameRecord) error {
	norm := NormalizePath(g.FolderPath)
	g.FolderPath = norm

	approvedVal := 0
	if g.Approved {
		approvedVal = 1
	}
	ignoredVal := 0
	if g.Ignored {
		ignoredVal = 1
	}

	now := time.Now().Unix()
	lastFetched := g.LastFetchedAt.Unix()
	if lastFetched < 0 {
		lastFetched = 0
	}

	// Sparse updates retain same-thread metadata; rebinding invalidates the previous catalog and fetch time.
	_, err := s.db.Exec(`
		INSERT INTO games (
			folder_path, thread_id, approved, source, title, creator, engine, status, rating,
			cover_url, banner_url, thread_url, catalog_version, installed_version, detected_engine,
			exe_hint, cover_mode, custom_title, ignored, last_fetched_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_path) DO UPDATE SET
			thread_id = excluded.thread_id,
			approved = excluded.approved,
			source = excluded.source,
			title = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.title != '' THEN excluded.title ELSE games.title END,
			creator = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.creator != '' THEN excluded.creator ELSE games.creator END,
			engine = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.engine != '' THEN excluded.engine ELSE games.engine END,
			status = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.status != '' THEN excluded.status ELSE games.status END,
			rating = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.rating > 0 THEN excluded.rating ELSE games.rating END,
			cover_url = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.cover_url != '' THEN excluded.cover_url ELSE games.cover_url END,
			banner_url = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.banner_url != '' THEN excluded.banner_url ELSE games.banner_url END,
			thread_url = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.thread_url != '' THEN excluded.thread_url ELSE games.thread_url END,
			catalog_version = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.catalog_version != '' THEN excluded.catalog_version ELSE games.catalog_version END,
			installed_version = CASE WHEN excluded.installed_version != '' THEN excluded.installed_version ELSE games.installed_version END,
			detected_engine = CASE WHEN excluded.detected_engine != '' THEN excluded.detected_engine ELSE games.detected_engine END,
			exe_hint = CASE WHEN excluded.exe_hint != '' THEN excluded.exe_hint ELSE games.exe_hint END,
			cover_mode = CASE WHEN excluded.cover_mode != '' THEN excluded.cover_mode ELSE games.cover_mode END,
			custom_title = CASE WHEN excluded.custom_title != '' THEN excluded.custom_title ELSE games.custom_title END,
			ignored = excluded.ignored,
			last_fetched_at = CASE WHEN games.thread_id IS NOT excluded.thread_id OR excluded.last_fetched_at > 0 THEN excluded.last_fetched_at ELSE games.last_fetched_at END,
			updated_at = excluded.updated_at
	`,
		norm, g.ThreadID, approvedVal, g.Source, g.Title, g.Creator, g.Engine, g.Status, g.Rating,
		g.CoverURL, g.BannerURL, g.ThreadURL, g.CatalogVersion, g.InstalledVersion, g.DetectedEngine,
		g.ExeHint, g.CoverMode, g.CustomTitle, ignoredVal, lastFetched, now, now,
	)

	return err
}

func (s *Store) ListApprovedFolderPaths() ([]string, error) {
	rows, err := s.db.Query("SELECT folder_path FROM games WHERE approved = 1 AND ignored = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func (s *Store) SavePendingMatch(folderPath, windowTitle, detectedEngine string, candidates interface{}) error {
	norm := NormalizePath(folderPath)
	data, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO pending_matches (folder_path, window_title, detected_engine, candidates_json, created_at)
		VALUES (?, ?, ?, ?, strftime('%s', 'now'))
		ON CONFLICT(folder_path) DO UPDATE SET
			window_title = excluded.window_title,
			detected_engine = excluded.detected_engine,
			candidates_json = excluded.candidates_json
	`, norm, windowTitle, detectedEngine, string(data))
	return err
}

func (s *Store) GetPendingMatchCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM pending_matches").Scan(&count)
	return count, err
}
