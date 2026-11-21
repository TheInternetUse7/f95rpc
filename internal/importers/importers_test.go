package importers

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestIsInsideRoots(t *testing.T) {
	roots := []string{`D:\Games`, `E:\VisualNovels`}
	excludes := []string{"_mods", "backups", ".git"}

	tests := []struct {
		folder   string
		expected bool
	}{
		{`D:\Games\Eternum`, true},
		{`d:\games\eternum`, true},
		{`D:\Games\Eternum\subfolder`, true},
		{`D:\GamesOld\Eternum`, false}, // boundary check: Games vs GamesOld
		{`E:\VisualNovels\GameA`, true},
		{`D:\Games\_mods\ModFolder`, false}, // excluded
		{`C:\Windows\System32`, false},
	}

	for _, tt := range tests {
		got := IsInsideRoots(tt.folder, roots, excludes)
		if got != tt.expected {
			t.Errorf("IsInsideRoots(%q) = %t; want %t", tt.folder, got, tt.expected)
		}
	}
}

func TestFindLongestPrefixRoot(t *testing.T) {
	roots := []string{`D:\Games`, `D:\Games\RenPy`}

	best := FindLongestPrefixRoot(`D:\Games\RenPy\GameA`, roots)
	if best != `D:\Games\RenPy` {
		t.Errorf("expected D:\\Games\\RenPy, got %s", best)
	}

	best2 := FindLongestPrefixRoot(`D:\Games\Other\GameB`, roots)
	if best2 != `D:\Games` {
		t.Errorf("expected D:\\Games, got %s", best2)
	}
}

func TestF95CheckerImporter_Mock(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "f95checker_mock.sqlite3")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE settings (default_exe_dir TEXT);
		INSERT INTO settings VALUES ('D:\\Games');

		CREATE TABLE games (
			id INTEGER,
			name TEXT,
			version TEXT,
			developer TEXT,
			url TEXT,
			installed TEXT,
			executables TEXT,
			archived INTEGER DEFAULT 0
		);

		INSERT INTO games VALUES (
			82285, 'Eternum', '0.8.5', 'Caribdis', 'https://f95zone.to/threads/82285', '0.8.5', '["Eternum/Eternum.exe"]', 0
		);
		INSERT INTO games VALUES (
			-1, 'Custom Game', '1.0', 'Anon', '', '1.0', '["Custom/Custom.exe"]', 0
		);
		INSERT INTO games VALUES (
			12345, 'Archived Game', '1.0', 'Anon', '', '1.0', '["Archived/Archived.exe"]', 1
		);
	`)
	if err != nil {
		t.Fatalf("insert mock data: %v", err)
	}
	db.Close()

	imp := NewF95CheckerImporter(dbPath, []string{`D:\Games`}, nil, false)
	games, err := imp.Import()
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if len(games) != 2 { // Archived game should be skipped
		t.Fatalf("expected 2 games, got %d", len(games))
	}

	if games[0].ThreadID != 82285 {
		t.Errorf("expected ThreadID 82285, got %d", games[0].ThreadID)
	}
	if games[0].FolderPath != filepath.Join(`D:\Games`, "Eternum") {
		t.Errorf("expected D:\\Games\\Eternum, got %s", games[0].FolderPath)
	}
	if !games[0].Approved {
		t.Errorf("expected F95 linked game to be approved")
	}

	// Custom non-F95 game (id = -1)
	if games[1].ThreadID != 0 || games[1].Approved {
		t.Errorf("expected custom game to have threadID 0 and approved false")
	}
}

func TestExtractThreadID(t *testing.T) {
	tests := []struct {
		url      string
		expected int64
	}{
		{"https://f95zone.to/threads/82285", 82285},
		{"https://f95zone.to/threads/82285/", 82285},
		{"https://f95zone.to/threads/eternum-v0-8-5.82285/", 82285},
		{"http://f95zone.to/threads/game-name.12345", 12345},
		{"https://f95zone.to/search/12345", 0},
		{"https://store.steampowered.com/app/12345", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := ExtractThreadID(tt.url)
		if got != tt.expected {
			t.Errorf("ExtractThreadID(%q) = %d; want %d", tt.url, got, tt.expected)
		}
	}
}

func TestPlayniteImporter_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	gamesDir := filepath.Join(tmpDir, "games")
	catsDir := filepath.Join(tmpDir, "categories")
	_ = os.MkdirAll(gamesDir, 0755)
	_ = os.MkdirAll(catsDir, 0755)

	// Create a category
	catJSON := `{"Id":"cat1","Name":"Visual Novels"}`
	_ = os.WriteFile(filepath.Join(catsDir, "cat1.json"), []byte(catJSON), 0644)

	// Create real installed directory on disk
	installDir := filepath.Join(tmpDir, "InstalledGame")
	_ = os.MkdirAll(installDir, 0755)

	// Create game JSON
	gameJSON := `{
		"Id": "g1",
		"Name": "Carnal Contract",
		"InstallDirectory": "` + filepath.ToSlash(installDir) + `",
		"Version": "1.0",
		"Hidden": false,
		"CategoryIds": ["cat1"],
		"Links": [{"Name": "F95", "Url": "https://f95zone.to/threads/carnal-contract.153997/"}],
		"GameActions": [{"Path": "{InstallDir}/CarnalContract.exe"}]
	}`
	_ = os.WriteFile(filepath.Join(gamesDir, "g1.json"), []byte(gameJSON), 0644)

	imp := NewPlayniteImporter(
		tmpDir,
		[]string{tmpDir},
		nil,
		[]string{"Visual Novels"},
		nil,
		false,
		true,
		false,
		false,
		true,
	)

	games, err := imp.Import()
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if len(games) != 1 {
		t.Fatalf("expected 1 imported game, got %d", len(games))
	}
	if games[0].ThreadID != 153997 {
		t.Errorf("expected ThreadID 153997, got %d", games[0].ThreadID)
	}
	if !games[0].Approved {
		t.Errorf("expected game to be approved")
	}
	expectedExe := filepath.Join(installDir, "CarnalContract.exe")
	if games[0].ExeHint != expectedExe {
		t.Errorf("expected ExeHint %s, got %s", expectedExe, games[0].ExeHint)
	}
}
