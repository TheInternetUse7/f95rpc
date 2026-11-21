package watcher

import (
	"path/filepath"
	"testing"

	"f95rpc/internal/config"
	"f95rpc/internal/presence"
	"f95rpc/internal/store"
)

type mockEnumerator struct {
	procs         []ProcessInfo
	foregroundPID uint32
}

func (m *mockEnumerator) EnumerateProcesses() ([]ProcessInfo, uint32, error) {
	return m.procs, m.foregroundPID, nil
}

type mockPresenceProvider struct {
	active   *presence.ActiveGame
	clears   int
	syncs    int
	clearErr error
}

func (m *mockPresenceProvider) Sync(game *presence.ActiveGame) error {
	m.active = game
	m.syncs++
	return nil
}

func (m *mockPresenceProvider) Clear() error {
	m.clears++
	if m.clearErr != nil {
		return m.clearErr
	}
	m.active = nil
	return nil
}

func (m *mockPresenceProvider) Close() error {
	return nil
}

func TestIsNoiseProcess(t *testing.T) {
	tests := []struct {
		path  string
		noise bool
	}{
		{`C:\Games\GameA\UnityCrashHandler64.exe`, true},
		{`C:\Games\GameA\crashreportclient.exe`, true},
		{`C:\Games\GameA\unins000.exe`, true},
		{`C:\Games\GameA\vc_redist.x64.exe`, true},
		{`C:\Games\GameA\Eternum.exe`, false},
		{`D:\Games\Ravenous\Ravenous.exe`, false},
		{`C:\Program Files\Steam\steam.exe`, true},
	}

	for _, tt := range tests {
		got := IsNoiseProcess(tt.path)
		if got != tt.noise {
			t.Errorf("IsNoiseProcess(%q) = %v; want %v", tt.path, got, tt.noise)
		}
	}
}

func TestMatchProcess(t *testing.T) {
	roots := []string{`C:\Games`, `D:\VN`}
	excludes := []string{`_mods`, `backups`}
	approved := map[string]string{
		`d:\scattered\eternum`: `D:\Scattered\Eternum`,
	}

	// 1. Matched approved folder (even outside roots)
	folder, isAppr, isRoot := MatchProcess(`D:\Scattered\Eternum\Eternum.exe`, roots, excludes, approved)
	if !isAppr || isRoot || folder != `D:\Scattered\Eternum` {
		t.Errorf("expected approved match for scattered Eternum, got %q, isAppr=%v, isRoot=%v", folder, isAppr, isRoot)
	}

	// 2. Matched root game
	folder, isAppr, isRoot = MatchProcess(`C:\Games\Ravenous\bin\Ravenous.exe`, roots, excludes, approved)
	if isAppr || !isRoot || !filepath.IsAbs(folder) || filepath.Base(folder) != "ravenous" {
		t.Errorf("expected root match for Ravenous, got %q, isAppr=%v, isRoot=%v", folder, isAppr, isRoot)
	}

	// 3. Excluded path
	folder, _, _ = MatchProcess(`C:\Games\_mods\mod.exe`, roots, excludes, approved)
	if folder != "" {
		t.Errorf("expected empty folder for excluded path, got %q", folder)
	}

	// 4. Outside both
	folder, _, _ = MatchProcess(`C:\Windows\explorer.exe`, roots, excludes, approved)
	if folder != "" {
		t.Errorf("expected empty folder for explorer.exe, got %q", folder)
	}
}

func TestDeduplicateProcesses(t *testing.T) {
	folder := `C:\Games\Eternum`
	procs := []ProcessInfo{
		{PID: 1000, ParentPID: 500, ExePath: `C:\Games\Eternum\Eternum.exe`, WindowTitle: "Eternum"},
		{PID: 1001, ParentPID: 1000, ExePath: `C:\Games\Eternum\python.exe`, WindowTitle: ""},
	}
	procFolderMap := map[uint32]string{
		1000: folder,
		1001: folder,
	}

	// Parent (1000) should be preferred over child (1001)
	res := DeduplicateProcesses(procs, procFolderMap, nil)
	best, ok := res[folder]
	if !ok {
		t.Fatalf("expected result for folder %q", folder)
	}
	if best.PID != 1000 {
		t.Errorf("expected PID 1000 (parent with title), got %d", best.PID)
	}
}

func TestWatcher_SingleGameLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	gamePath := `C:\Games\Eternum`
	err = s.UpsertGame(&store.GameRecord{
		FolderPath:       gamePath,
		Title:            "Eternum",
		ThreadID:         93340,
		Approved:         true,
		Creator:          "Caribdis",
		Engine:           "Ren'Py",
		Status:           "Ongoing",
		Rating:           4.9,
		InstalledVersion: "0.4",
	})
	if err != nil {
		t.Fatalf("upsert game: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Roots = []string{`C:\Games`}

	mockEnum := &mockEnumerator{}
	mockProv := &mockPresenceProvider{}

	w := NewWatcher(cfg, s, nil, mockProv, mockEnum)

	// Step 1: No processes running -> presence remains clear
	if err := w.PollOnce(); err != nil {
		t.Fatalf("poll error: %v", err)
	}
	if mockProv.active != nil {
		t.Errorf("expected nil active game, got %v", mockProv.active)
	}

	// Step 2: Game starts running
	mockEnum.procs = []ProcessInfo{
		{PID: 1234, ExePath: `C:\Games\Eternum\Eternum.exe`, WindowTitle: "Eternum v0.4"},
	}
	if err := w.PollOnce(); err != nil {
		t.Fatalf("poll error: %v", err)
	}
	if mockProv.active == nil {
		t.Fatalf("expected active presence, got nil")
	}
	if mockProv.active.Title != "Eternum" {
		t.Errorf("expected title 'Eternum', got %q", mockProv.active.Title)
	}
	if mockProv.active.PID != 1234 {
		t.Errorf("expected PID 1234, got %d", mockProv.active.PID)
	}

	// Step 3: Game closes
	mockEnum.procs = nil
	if err := w.PollOnce(); err != nil {
		t.Fatalf("poll error: %v", err)
	}
	if mockProv.active != nil {
		t.Errorf("expected presence cleared after game closes, got %v", mockProv.active)
	}
}

func TestWatcher_MultiGameConflict_LockFirst(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Game1`,
		Title:      "Game One",
		Approved:   true,
	})
	s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Game2`,
		Title:      "Game Two",
		Approved:   true,
	})

	cfg := config.DefaultConfig()
	cfg.Roots = []string{`C:\Games`}
	cfg.SelectionMode = "lock_first"

	mockEnum := &mockEnumerator{}
	mockProv := &mockPresenceProvider{}
	w := NewWatcher(cfg, s, nil, mockProv, mockEnum)

	// Start Game 1 first
	mockEnum.procs = []ProcessInfo{
		{PID: 100, ExePath: `C:\Games\Game1\Game1.exe`},
	}
	_ = w.PollOnce()
	if mockProv.active == nil || mockProv.active.Title != "Game One" {
		t.Fatalf("expected Game One, got %v", mockProv.active)
	}

	// Start Game 2 while Game 1 is still open (Game 2 has foreground)
	mockEnum.procs = []ProcessInfo{
		{PID: 100, ExePath: `C:\Games\Game1\Game1.exe`},
		{PID: 200, ExePath: `C:\Games\Game2\Game2.exe`, IsForeground: true},
	}
	mockEnum.foregroundPID = 200
	_ = w.PollOnce()

	// In lock_first mode, should STILL be Game One
	if mockProv.active == nil || mockProv.active.Title != "Game One" {
		t.Fatalf("expected Game One to stay locked, got %v", mockProv.active)
	}

	// Game 1 exits -> switches to Game 2
	mockEnum.procs = []ProcessInfo{
		{PID: 200, ExePath: `C:\Games\Game2\Game2.exe`},
	}
	_ = w.PollOnce()
	if mockProv.active == nil || mockProv.active.Title != "Game Two" {
		t.Fatalf("expected switch to Game Two after Game One exit, got %v", mockProv.active)
	}
}

func TestWatcher_MultiGameConflict_Foreground(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Game1`,
		Title:      "Game One",
		Approved:   true,
	})
	s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Game2`,
		Title:      "Game Two",
		Approved:   true,
	})

	cfg := config.DefaultConfig()
	cfg.Roots = []string{`C:\Games`}
	cfg.SelectionMode = "foreground"

	mockEnum := &mockEnumerator{}
	mockProv := &mockPresenceProvider{}
	w := NewWatcher(cfg, s, nil, mockProv, mockEnum)

	// Both running, Game 2 is foreground
	mockEnum.procs = []ProcessInfo{
		{PID: 100, ExePath: `C:\Games\Game1\Game1.exe`},
		{PID: 200, ExePath: `C:\Games\Game2\Game2.exe`},
	}
	mockEnum.foregroundPID = 200
	_ = w.PollOnce()

	if mockProv.active == nil || mockProv.active.Title != "Game Two" {
		t.Fatalf("expected Game Two to be selected by foreground focus, got %v", mockProv.active)
	}

	// Switch foreground to Game 1
	mockEnum.foregroundPID = 100
	_ = w.PollOnce()

	if mockProv.active == nil || mockProv.active.Title != "Game One" {
		t.Fatalf("expected Game One to be selected after focus shift, got %v", mockProv.active)
	}
}

func TestWatcher_StealthMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Game1`,
		Title:      "Game One",
		Approved:   true,
	})

	cfg := config.DefaultConfig()
	cfg.Roots = []string{`C:\Games`}

	mockEnum := &mockEnumerator{
		procs: []ProcessInfo{{PID: 100, ExePath: `C:\Games\Game1\Game1.exe`}},
	}
	mockProv := &mockPresenceProvider{}
	w := NewWatcher(cfg, s, nil, mockProv, mockEnum)

	_ = w.PollOnce()
	if mockProv.active == nil {
		t.Fatalf("expected active presence before stealth mode")
	}

	// Toggle stealth mode
	_ = w.SetStealthMode(true)
	if mockProv.active != nil {
		t.Errorf("expected presence cleared immediately upon entering stealth mode")
	}

	// Polling while stealth mode is on should keep presence cleared
	_ = w.PollOnce()
	if mockProv.active != nil {
		t.Errorf("expected presence to stay cleared during stealth mode")
	}
}
