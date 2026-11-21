package importers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type F95CheckerImporter struct {
	dbPath       string
	roots        []string
	excludePaths []string
	rootsOnly    bool
}

func NewF95CheckerImporter(dbPath string, roots []string, excludePaths []string, rootsOnly bool) *F95CheckerImporter {
	if dbPath == "" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, _ := os.UserHomeDir()
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		dbPath = filepath.Join(appData, "f95checker", "db.sqlite3")
	}
	return &F95CheckerImporter{
		dbPath:       dbPath,
		roots:        roots,
		excludePaths: excludePaths,
		rootsOnly:    rootsOnly,
	}
}

func (imp *F95CheckerImporter) Import() ([]ImportedGame, error) {
	if _, err := os.Stat(imp.dbPath); err != nil {
		return nil, fmt.Errorf("F95Checker database not found at '%s': %w", imp.dbPath, err)
	}

	// Open read-only to avoid blocking or interfering with F95Checker
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(imp.dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open f95checker sqlite db: %w", err)
	}
	defer db.Close()

	// 1. Get default_exe_dir from settings
	var defaultExeDir string
	row := db.QueryRow("SELECT default_exe_dir FROM settings LIMIT 1")
	_ = row.Scan(&defaultExeDir)

	// 2. Query games table
	rows, err := db.Query("SELECT id, name, version, developer, url, installed, executables FROM games WHERE archived = 0")
	if err != nil {
		return nil, fmt.Errorf("query f95checker games: %w", err)
	}
	defer rows.Close()

	var imported []ImportedGame

	for rows.Next() {
		var id int64
		var name, version, developer, urlStr, installed, execsJSON string

		if err := rows.Scan(&id, &name, &version, &developer, &urlStr, &installed, &execsJSON); err != nil {
			continue
		}

		var execs []string
		if execsJSON != "" && execsJSON != "[]" {
			_ = json.Unmarshal([]byte(execsJSON), &execs)
		}

		var folderPath string
		var exeHint string

		if len(execs) > 0 && execs[0] != "" {
			execPath := execs[0]
			if filepath.IsAbs(execPath) {
				exeHint = execPath
				folderPath = filepath.Dir(execPath)
			} else if defaultExeDir != "" {
				full := filepath.Join(defaultExeDir, execPath)
				exeHint = full
				folderPath = filepath.Dir(full)
			}
		} else if defaultExeDir != "" && name != "" {
			// Fallback: check if subfolder with game name exists in default_exe_dir
			candidate := filepath.Join(defaultExeDir, name)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				folderPath = candidate
			}
		}

		// Skip games without a resolvable local folder
		if folderPath == "" {
			continue
		}

		// If rootsOnly is requested, filter against configured roots
		if imp.rootsOnly {
			if !IsInsideRoots(folderPath, imp.roots, imp.excludePaths) {
				continue
			}
		}

		var threadID int64
		hasLink := false
		if id > 0 {
			threadID = id
			hasLink = true
		}

		instVer := installed
		if instVer == "" {
			instVer = version
		}

		imported = append(imported, ImportedGame{
			FolderPath:       folderPath,
			ExeHint:          exeHint,
			ThreadID:         threadID,
			Name:             name,
			InstalledVersion: instVer,
			Developer:        developer,
			Source:           "f95checker",
			HasF95Link:       hasLink,
			Approved:         hasLink, // Games with valid positive F95 thread IDs are approved
		})
	}

	return imported, nil
}
