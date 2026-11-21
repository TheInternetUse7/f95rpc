package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_f95rpc.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	folder := `D:\Games\Eternum`
	game := &GameRecord{
		FolderPath:       folder,
		ThreadID:         82285,
		Approved:         true,
		Source:           "test",
		Title:            "Eternum",
		Creator:          "Caribdis",
		Engine:           "Ren'Py",
		Status:           "Ongoing",
		Rating:           4.9,
		CoverURL:         "https://example.com/cover.jpg",
		InstalledVersion: "0.8.5",
		LastFetchedAt:    time.Now(),
	}

	if err := s.UpsertGame(game); err != nil {
		t.Fatalf("UpsertGame failed: %v", err)
	}

	// Test retrieval with different casing (case-insensitivity on Windows)
	retrieved, err := s.GetGameByFolder(`d:\games\eternum`)
	if err != nil {
		t.Fatalf("GetGameByFolder failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected retrieved game, got nil")
	}
	if retrieved.Title != "Eternum" {
		t.Errorf("expected Title Eternum, got %s", retrieved.Title)
	}
	if retrieved.ThreadID != 82285 {
		t.Errorf("expected ThreadID 82285, got %d", retrieved.ThreadID)
	}
	if !retrieved.Approved {
		t.Errorf("expected Approved true")
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`D:\Games\GameA\`, `d:\games\gamea`},
		{`d:\GAMES\GameA`, `d:\games\gamea`},
		{`C:\folder\..\folder\test`, `c:\folder\test`},
	}

	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizePath(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestListApprovedFolderPaths(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_approved.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	_ = s.UpsertGame(&GameRecord{
		FolderPath: `D:\Games\Approved1`,
		Approved:   true,
		Source:     "test",
	})
	_ = s.UpsertGame(&GameRecord{
		FolderPath: `D:\Games\Unapproved`,
		Approved:   false,
		Source:     "test",
	})
	_ = s.UpsertGame(&GameRecord{
		FolderPath: `D:\Games\IgnoredGame`,
		Approved:   true,
		Ignored:    true,
		Source:     "test",
	})

	paths, err := s.ListApprovedFolderPaths()
	if err != nil {
		t.Fatalf("ListApprovedFolderPaths failed: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 approved path, got %d: %v", len(paths), paths)
	}
	if paths[0] != `d:\games\approved1` {
		t.Errorf("expected d:\\games\\approved1, got %s", paths[0])
	}
}
