package scanner

import (
	"os"
	"path/filepath"
	"strings"

	"f95rpc/internal/importers"
)

type ScannedGame struct {
	FolderPath    string `json:"folder_path"`
	RawFolderName string `json:"raw_folder_name"`
	CleanTitle    string `json:"clean_title"`
	Version       string `json:"version"`
	Engine        string `json:"engine"`
	ExeHint       string `json:"exe_hint"`
	OwningRoot    string `json:"owning_root"`
	SteamAppID    string `json:"steam_appid"`
}

func ScanRoots(roots []string, excludePaths []string) ([]ScannedGame, error) {
	var games []ScannedGame
	seenFolders := make(map[string]bool)

	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(absRoot)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			folderName := entry.Name()
			fullPath := filepath.Join(absRoot, folderName)
			norm := importers.NormalizePath(fullPath)

			if seenFolders[norm] {
				continue
			}

			// Check exclusion patterns
			if !importers.IsInsideRoots(fullPath, roots, excludePaths) {
				continue
			}

			// Longest-prefix matching: only scan this folder if absRoot is its most specific owning root
			bestRoot := importers.FindLongestPrefixRoot(fullPath, roots)
			if !strings.EqualFold(bestRoot, absRoot) && !strings.EqualFold(importers.NormalizePath(bestRoot), importers.NormalizePath(absRoot)) {
				continue
			}

			seenFolders[norm] = true

			// 1. Clean folder name
			cleaned := CleanFolderName(folderName)

			// 2. Engine fingerprinting
			fp := FingerprintFolder(fullPath)

			title := cleaned.Title
			if fp.TitleHint != "" {
				title = fp.TitleHint
			}

			version := cleaned.Version
			if fp.VersionHint != "" {
				version = fp.VersionHint
			}

			games = append(games, ScannedGame{
				FolderPath:    fullPath,
				RawFolderName: folderName,
				CleanTitle:    title,
				Version:       version,
				Engine:        fp.Engine,
				ExeHint:       fp.ExeHint,
				OwningRoot:    absRoot,
				SteamAppID:    fp.SteamAppID,
			})
		}
	}

	return games, nil
}
