package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanFolderName(t *testing.T) {
	tests := []struct {
		input       string
		wantTitle   string
		wantVersion string
	}{
		{"Eternum-Multi-Mod-0.8.5", "Eternum", "0.8.5"},
		{"[CD] [RUS] Pale Carnations (Final) [Portable]", "Pale Carnations", ""},
		{"Ripples-0.7.0-public-Multi-Mod", "Ripples", "0.7.0"},
		{"BeingADIK-v0.12.2.9-pc", "BeingADIK", "v0.12.2.9"},
		{"Ravenous", "Ravenous", ""},
	}

	for _, tt := range tests {
		got := CleanFolderName(tt.input)
		if got.Title != tt.wantTitle {
			t.Errorf("CleanFolderName(%q).Title = %q; want %q", tt.input, got.Title, tt.wantTitle)
		}
		if tt.wantVersion != "" && got.Version != tt.wantVersion {
			t.Errorf("CleanFolderName(%q).Version = %q; want %q", tt.input, got.Version, tt.wantVersion)
		}
	}
}

func TestFingerprintFolder(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create Ren'Py game folder structure
	renpyDir := filepath.Join(tmpDir, "RenPyGame")
	_ = os.MkdirAll(filepath.Join(renpyDir, "game"), 0755)
	_ = os.WriteFile(filepath.Join(renpyDir, "game", "script.rpy"), []byte("label start:"), 0644)
	_ = os.WriteFile(filepath.Join(renpyDir, "Game.exe"), []byte("exe"), 0755)

	fp := FingerprintFolder(renpyDir)
	if fp.Engine != "Ren'Py" {
		t.Errorf("expected engine Ren'Py, got %s", fp.Engine)
	}
	if fp.ExeHint != filepath.Join(renpyDir, "Game.exe") {
		t.Errorf("expected ExeHint %s, got %s", filepath.Join(renpyDir, "Game.exe"), fp.ExeHint)
	}

	// 2. Create Unity game folder structure
	unityDir := filepath.Join(tmpDir, "UnityGame")
	_ = os.MkdirAll(filepath.Join(unityDir, "Game_Data"), 0755)
	_ = os.WriteFile(filepath.Join(unityDir, "Game_Data", "app.info"), []byte("CompanyName\nProductName\n"), 0644)

	fpUnity := FingerprintFolder(unityDir)
	if fpUnity.Engine != "Unity" {
		t.Errorf("expected engine Unity, got %s", fpUnity.Engine)
	}
	if fpUnity.TitleHint != "ProductName" {
		t.Errorf("expected TitleHint ProductName, got %s", fpUnity.TitleHint)
	}
}

func TestScanRoots(t *testing.T) {
	tmpDir := t.TempDir()
	root1 := filepath.Join(tmpDir, "Root1")
	_ = os.MkdirAll(filepath.Join(root1, "Eternum-0.8.5"), 0755)
	_ = os.MkdirAll(filepath.Join(root1, "_mods"), 0755)

	games, err := ScanRoots([]string{root1}, []string{"_mods"})
	if err != nil {
		t.Fatalf("ScanRoots failed: %v", err)
	}

	if len(games) != 1 {
		t.Fatalf("expected 1 game (excluding _mods), got %d", len(games))
	}
	if games[0].CleanTitle != "Eternum" {
		t.Errorf("expected CleanTitle Eternum, got %s", games[0].CleanTitle)
	}
}
